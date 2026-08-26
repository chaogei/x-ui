package singbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"x-ui/core"
	"x-ui/core/singbox/statspb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// parseTrafficName 解析 sing-box v2ray_api StatsService 返回的流量计数器名。
//
// 三种前缀分别对应入站、出站与"按用户"维度：
//
//	inbound>>>inbound-443-vmess>>>traffic>>>uplink
//	outbound>>>direct>>>traffic>>>downlink
//	user>>>alice@example.com>>>traffic>>>uplink
//
// user 维度是多用户配额的依据：sing-box 只有在 experimental.v2ray_api.stats.users
// 里列出用户名时才建这个计数器，名字取自 inbound settings 中该用户的 name 字段。
//
// tag 本身可以包含 ">>>"，所以从两端定位结构分隔符，而不是把整串切成固定段数。
// 返回值直接引用 name 的底层存储，解析过程不分配内存。
func parseTrafficName(name string) (kind, tag, direction string, ok bool) {
	const delimiter = ">>>"

	kindEnd := strings.Index(name, delimiter)
	if kindEnd <= 0 {
		return "", "", "", false
	}
	kind = name[:kindEnd]
	switch kind {
	case "inbound", "outbound", "user":
	default:
		return "", "", "", false
	}

	directionStart := strings.LastIndex(name, delimiter)
	if directionStart <= kindEnd {
		return "", "", "", false
	}
	direction = name[directionStart+len(delimiter):]
	if direction != "downlink" && direction != "uplink" {
		return "", "", "", false
	}

	trafficStart := strings.LastIndex(name[:directionStart], delimiter)
	tagStart := kindEnd + len(delimiter)
	if trafficStart <= tagStart || name[trafficStart+len(delimiter):directionStart] != "traffic" {
		return "", "", "", false
	}
	tag = name[tagStart:trafficStart]
	// Keep the old regexp's `.` semantics: a tag containing a newline is malformed.
	if strings.IndexByte(tag, '\n') >= 0 {
		return "", "", "", false
	}
	return kind, tag, direction, true
}

// statsQueryTimeout 单次 QueryStats RPC 的最长等待时间。
const statsQueryTimeout = 10 * time.Second

// statsClient 负责与 sing-box 内置的 V2Ray API gRPC 服务通信。
//
// 设计为 Process 生命周期内复用一条连接：避免每次拉流量（默认 10s 一次）都
// 重新进行 TCP 握手 + HTTP/2 初始化造成无谓的 CPU/socket 开销。
//
// 协议 stub 来自本仓库的 core/singbox/statspb（手写线格式，无 xray-core 依赖）。
type statsClient struct {
	mu     sync.Mutex
	conn   *grpc.ClientConn
	client statspb.StatsServiceClient
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
		client: statspb.NewStatsServiceClient(conn),
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

// QueryTraffic 拉取流量统计并按维度 + tag 聚合上下行。
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
	resp, err := client.QueryStats(ctx, &statspb.QueryStatsRequest{Reset_: reset})
	if err != nil {
		return nil, err
	}
	return aggregateTraffic(resp.GetStat()), nil
}

// aggregateTraffic 把扁平的计数器列表折叠成按 (维度, tag) 分组的 Traffic。
//
// 抽成独立函数是为了能在不起 gRPC 服务的前提下用表驱动用例覆盖名字解析：
// 这段逻辑的错误（例如把 api 的自身流量算进用户配额）在生产环境很难被发现。
func aggregateTraffic(stats []*statspb.Stat) []*core.Traffic {
	type key struct {
		kind string
		tag  string
	}
	index := make(map[key]*core.Traffic, len(stats))
	result := make([]*core.Traffic, 0, len(stats))

	for _, stat := range stats {
		if stat == nil {
			continue
		}
		kind, tag, dir, ok := parseTrafficName(stat.Name)
		if !ok {
			continue
		}
		// api 是面板自己的管理入站，它的流量不属于任何用户，也不该计入配额。
		if kind == "inbound" && tag == "api" {
			continue
		}

		k := key{kind: kind, tag: tag}
		t, ok := index[k]
		if !ok {
			t = &core.Traffic{
				IsInbound: kind == "inbound",
				IsUser:    kind == "user",
				Tag:       tag,
			}
			index[k] = t
			result = append(result, t)
		}
		if dir == "downlink" {
			t.Down = stat.Value
		} else {
			t.Up = stat.Value
		}
	}
	return result
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
