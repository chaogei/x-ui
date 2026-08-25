// Package statspb 是 sing-box `experimental.v2ray_api` StatsService 的最小客户端。
//
// 为什么不直接依赖 xtls/xray-core：
//
//	x-ui 只需要一个 RPC（QueryStats）和三个消息，为此拉进整个 Xray 内核
//	（连同它自己的 protobuf 生成代码、路由、传输层依赖）是不成比例的。
//	更糟的是它其实是错的：xray-core 的 stub 调用的是
//	/xray.app.stats.command.StatsService/QueryStats，而 sing-box 在
//	experimental/v2rayapi/stats.go 的 init() 里把服务名改写成了
//	v2ray.core.app.stats.command.StatsService —— 两者对不上，sing-box 会回
//	codes.Unimplemented，流量统计一直是 0。
//
// 为什么手写线格式而不是跑 protoc：
//
//	三个消息一共七个标量字段，用 protowire 手写编解码只有百来行，且不需要
//	在 CI / Dockerfile 里引入 protoc + protoc-gen-go 工具链。配套的
//	stats.proto 留在同目录作为权威 schema，字段编号与 sing-box 逐字对齐；
//	stats_test.go 用 protowire 反向解析校验编码结果。
//
// 消息类型不实现 proto.Message，所以调用时通过 grpc.ForceCodec 注入本包的
// codec；content-subtype 仍是标准的 "proto"，服务端无感知。
package statspb

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
)

// QueryStatsFullMethod 是 sing-box 实际注册的 gRPC 方法全名。
// 注意 package 段是 v2ray.core.app.stats.command 而非 sing-box 自己的
// experimental.v2rayapi，原因见包注释与 stats.proto 的说明。
const QueryStatsFullMethod = "/v2ray.core.app.stats.command.StatsService/QueryStats"

// Stat 对应 proto message Stat：一个命名计数器。
type Stat struct {
	// Name 形如 inbound>>>inbound-443-vmess>>>traffic>>>uplink
	Name string
	// Value 是自上次 reset 以来累计的字节数。
	Value int64
}

// QueryStatsRequest 对应 proto message QueryStatsRequest。
type QueryStatsRequest struct {
	// Pattern 为空表示返回全部计数器。
	Pattern string
	// Reset_ 为 true 时服务端返回后清零计数器（读取即重置）。
	//
	// 字段名带下划线是为了不和 Go 的内建 reset 语义混淆，也与 xray/v2ray
	// 生成代码的习惯一致。
	Reset_ bool
	// Patterns / Regexp 是 sing-box 的扩展匹配参数，x-ui 不使用，
	// 保留字段以便将来按 tag 精确拉取时无需改线格式。
	Patterns []string
	Regexp   bool
}

// QueryStatsResponse 对应 proto message QueryStatsResponse。
type QueryStatsResponse struct {
	Stat []*Stat
}

// GetStat 提供与生成代码一致的 nil-safe 读取器。
func (r *QueryStatsResponse) GetStat() []*Stat {
	if r == nil {
		return nil
	}
	return r.Stat
}

/* ===================== 线格式编解码 ===================== */

// 字段编号，必须与 stats.proto / sing-box 保持一致。
const (
	fieldStatName  = 1
	fieldStatValue = 2

	fieldRequestPattern  = 1
	fieldRequestReset    = 2
	fieldRequestPatterns = 3
	fieldRequestRegexp   = 4

	fieldResponseStat = 1
)

// marshalQueryStatsRequest 按 proto3 规则编码请求。
// proto3 的标量默认值不上线，所以空 pattern / false 会被整体省略。
func marshalQueryStatsRequest(m *QueryStatsRequest) []byte {
	var b []byte
	if m == nil {
		return b
	}
	if m.Pattern != "" {
		b = protowire.AppendTag(b, fieldRequestPattern, protowire.BytesType)
		b = protowire.AppendString(b, m.Pattern)
	}
	if m.Reset_ {
		b = protowire.AppendTag(b, fieldRequestReset, protowire.VarintType)
		b = protowire.AppendVarint(b, 1)
	}
	for _, p := range m.Patterns {
		b = protowire.AppendTag(b, fieldRequestPatterns, protowire.BytesType)
		b = protowire.AppendString(b, p)
	}
	if m.Regexp {
		b = protowire.AppendTag(b, fieldRequestRegexp, protowire.VarintType)
		b = protowire.AppendVarint(b, 1)
	}
	return b
}

// unmarshalQueryStatsResponse 解析响应，未知字段按 protobuf 约定跳过。
func unmarshalQueryStatsResponse(data []byte, m *QueryStatsResponse) error {
	m.Stat = nil
	return eachField(data, func(num protowire.Number, typ protowire.Type, body []byte) error {
		if num != fieldResponseStat || typ != protowire.BytesType {
			return nil
		}
		stat := &Stat{}
		if err := unmarshalStat(body, stat); err != nil {
			return err
		}
		m.Stat = append(m.Stat, stat)
		return nil
	})
}

func unmarshalStat(data []byte, s *Stat) error {
	return eachField(data, func(num protowire.Number, typ protowire.Type, body []byte) error {
		switch {
		case num == fieldStatName && typ == protowire.BytesType:
			s.Name = string(body)
		case num == fieldStatValue && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(body)
			if n < 0 {
				return protowire.ParseError(n)
			}
			s.Value = int64(v)
		}
		return nil
	})
}

// eachField 遍历一段 protobuf 编码，对每个字段回调 (number, type, payload)。
//
// payload 的形态随 wire type 而变：
//   - BytesType  → 已剥离长度前缀的内容
//   - VarintType → 仍未解码的 varint 字节（回调侧用 ConsumeVarint 取值）
//   - 其余类型   → 原始字节，调用方按需处理
//
// 未知字段不是错误：sing-box 将来给 Stat 加字段时，旧面板必须继续工作。
func eachField(data []byte, fn func(num protowire.Number, typ protowire.Type, body []byte) error) error {
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]

		size := protowire.ConsumeFieldValue(num, typ, data)
		if size < 0 {
			return protowire.ParseError(size)
		}
		body := data[:size]
		if typ == protowire.BytesType {
			// 剥掉长度前缀，回调直接拿到内容。
			inner, hn := protowire.ConsumeBytes(data)
			if hn < 0 {
				return protowire.ParseError(hn)
			}
			body = inner
		}
		if err := fn(num, typ, body); err != nil {
			return err
		}
		data = data[size:]
	}
	return nil
}

/* ===================== gRPC codec ===================== */

// codec 让 grpc-go 能收发本包这几个非 proto.Message 的结构体。
//
// Name() 返回 "proto"：线格式确实就是 protobuf，服务端按标准
// application/grpc+proto 解析，不需要任何特殊配合。
type codec struct{}

func (codec) Name() string { return "proto" }

func (codec) Marshal(v any) ([]byte, error) {
	req, ok := v.(*QueryStatsRequest)
	if !ok {
		return nil, fmt.Errorf("statspb: cannot marshal %T", v)
	}
	return marshalQueryStatsRequest(req), nil
}

func (codec) Unmarshal(data []byte, v any) error {
	resp, ok := v.(*QueryStatsResponse)
	if !ok {
		return fmt.Errorf("statspb: cannot unmarshal into %T", v)
	}
	return unmarshalQueryStatsResponse(data, resp)
}

/* ===================== 客户端 ===================== */

// StatsServiceClient 是 QueryStats 的最小客户端接口。
// 抽成接口是为了让上层（core/singbox）能在测试里替换成假实现。
type StatsServiceClient interface {
	QueryStats(ctx context.Context, in *QueryStatsRequest, opts ...grpc.CallOption) (*QueryStatsResponse, error)
}

type statsServiceClient struct {
	cc grpc.ClientConnInterface
}

// NewStatsServiceClient 在既有连接上构造客户端；不会自行拨号。
func NewStatsServiceClient(cc grpc.ClientConnInterface) StatsServiceClient {
	return &statsServiceClient{cc: cc}
}

func (c *statsServiceClient) QueryStats(ctx context.Context, in *QueryStatsRequest, opts ...grpc.CallOption) (*QueryStatsResponse, error) {
	out := &QueryStatsResponse{}
	// ForceCodec 必须排在调用方的 opts 之前，保证调用方仍可覆盖其它选项，
	// 但默认走本包的编解码。
	all := make([]grpc.CallOption, 0, len(opts)+1)
	all = append(all, grpc.ForceCodec(codec{}))
	all = append(all, opts...)
	if err := c.cc.Invoke(ctx, QueryStatsFullMethod, in, out, all...); err != nil {
		return nil, err
	}
	return out, nil
}
