package singbox

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"
	"x-ui/core"

	statsservice "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// trafficRegex 匹配 V2Ray/Xray/sing-box StatsService 返回的流量名。
// 示例：inbound>>>inbound-443>>>traffic>>>uplink
var trafficRegex = regexp.MustCompile(`(inbound|outbound)>>>([^>]+)>>>traffic>>>(downlink|uplink)`)

// statsQueryTimeout 单次 QueryStats RPC 的最长等待时间。
const statsQueryTimeout = 10 * time.Second

// statsClient 负责与 sing-box 内置的 V2Ray API gRPC 服务通信。
//
// 设计为 Process 生命周期内复用一条连接：避免每次拉流量（默认 10s 一次）都
// 重新进行 TCP 握手 + HTTP/2 初始化造成无谓的 CPU/socket 开销。
//
// sing-box 的 experimental.v2ray_api 在 protobuf 层与 Xray StatsService 完全兼容，
// 所以直接复用 xray-core 的 grpc stub 生成代码。
type statsClient struct {
	mu     sync.Mutex
	conn   *grpc.ClientConn
	client statsservice.StatsServiceClient
}

// newStatsClient 建立一条到本地 API 端口的 gRPC 懒连接。
// grpc.NewClient 不会立即拨号；实际连接在首次 RPC 时建立，失败会在 QueryTraffic 返回。
func newStatsClient(apiPort int) (*statsClient, error) {
	if apiPort <= 0 {
		return nil, fmt.Errorf("invalid v2ray_api port: %d", apiPort)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", apiPort)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &statsClient{
		conn:   conn,
		client: statsservice.NewStatsServiceClient(conn),
	}, nil
}

// Close 关闭底层连接，调用后再用 QueryTraffic 会返回错误。
func (c *statsClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.client = nil
	return err
}

// QueryTraffic 拉取 V2Ray 流量统计并按 tag 聚合上下行。
// reset=true 时服务端会在返回后清零计数器。
func (c *statsClient) QueryTraffic(reset bool) ([]*core.Traffic, error) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return nil, errors.New("stats client is closed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), statsQueryTimeout)
	defer cancel()
	resp, err := client.QueryStats(ctx, &statsservice.QueryStatsRequest{Reset_: reset})
	if err != nil {
		return nil, err
	}

	tagTrafficMap := make(map[string]*core.Traffic)
	result := make([]*core.Traffic, 0)
	for _, stat := range resp.GetStat() {
		matches := trafficRegex.FindStringSubmatch(stat.Name)
		if len(matches) != 4 {
			continue
		}
		isInbound := matches[1] == "inbound"
		tag := matches[2]
		if tag == "api" {
			continue
		}
		isDown := matches[3] == "downlink"

		t, ok := tagTrafficMap[tag]
		if !ok {
			t = &core.Traffic{IsInbound: isInbound, Tag: tag}
			tagTrafficMap[tag] = t
			result = append(result, t)
		}
		if isDown {
			t.Down = stat.Value
		} else {
			t.Up = stat.Value
		}
	}
	return result, nil
}

// GetTraffic 是 core.Core 接口的实现：
// 调用方不感知 gRPC 连接的生命周期管理。
func (p *Process) GetTraffic(reset bool) ([]*core.Traffic, error) {
	p.mu.RLock()
	client := p.stats
	p.mu.RUnlock()
	if client == nil {
		return nil, errors.New("sing-box stats client not initialized")
	}
	return client.QueryTraffic(reset)
}
