package statspb

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
)

// TestQueryStatsFullMethodMatchesSingBox 钉住服务全名。
//
// sing-box 在 experimental/v2rayapi/stats.go 的 init() 里把服务名改写成了
// v2ray.core.app.stats.command.StatsService。历史实现借用 xray-core 的 stub，
// 发出的是 /xray.app.stats.command.StatsService/QueryStats，sing-box 回
// Unimplemented，面板上的流量永远是 0 —— 而且没有任何报错弹窗。
func TestQueryStatsFullMethodMatchesSingBox(t *testing.T) {
	const want = "/v2ray.core.app.stats.command.StatsService/QueryStats"
	if QueryStatsFullMethod != want {
		t.Errorf("QueryStatsFullMethod = %q, want %q", QueryStatsFullMethod, want)
	}
}

// TestMarshalQueryStatsRequest 用 protowire 反向解析编码结果，
// 确认字段编号与 wire type 与 stats.proto 一致。
func TestMarshalQueryStatsRequest(t *testing.T) {
	tests := map[string]struct {
		in   *QueryStatsRequest
		want map[protowire.Number]any
	}{
		"empty request emits nothing": {
			in:   &QueryStatsRequest{},
			want: map[protowire.Number]any{},
		},
		"reset only": {
			in:   &QueryStatsRequest{Reset_: true},
			want: map[protowire.Number]any{2: uint64(1)},
		},
		"pattern and reset": {
			in: &QueryStatsRequest{Pattern: "inbound>>>", Reset_: true},
			want: map[protowire.Number]any{
				1: "inbound>>>",
				2: uint64(1),
			},
		},
		"patterns and regexp": {
			in: &QueryStatsRequest{Patterns: []string{"a", "b"}, Regexp: true},
			want: map[protowire.Number]any{
				3: []string{"a", "b"},
				4: uint64(1),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := decodeFields(t, marshalQueryStatsRequest(tc.in))
			if len(got) != len(tc.want) {
				t.Fatalf("encoded %d fields %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for num, want := range tc.want {
				switch expected := want.(type) {
				case string:
					if s, ok := got[num].(string); !ok || s != expected {
						t.Errorf("field %d = %v, want %q", num, got[num], expected)
					}
				case uint64:
					if v, ok := got[num].(uint64); !ok || v != expected {
						t.Errorf("field %d = %v, want %d", num, got[num], expected)
					}
				case []string:
					list, ok := got[num].([]string)
					if !ok || len(list) != len(expected) {
						t.Fatalf("field %d = %v, want %v", num, got[num], expected)
					}
					for i := range expected {
						if list[i] != expected[i] {
							t.Errorf("field %d[%d] = %q, want %q", num, i, list[i], expected[i])
						}
					}
				}
			}
		})
	}
}

// TestUnmarshalQueryStatsResponse 走一次完整的编码→解码往返。
func TestUnmarshalQueryStatsResponse(t *testing.T) {
	wire := encodeResponse([]*Stat{
		{Name: "inbound>>>inbound-443-vmess>>>traffic>>>uplink", Value: 1 << 40},
		{Name: "user>>>alice@example.com>>>traffic>>>downlink", Value: 0},
		{Name: "outbound>>>direct>>>traffic>>>uplink", Value: 7},
	})

	resp := &QueryStatsResponse{}
	if err := unmarshalQueryStatsResponse(wire, resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.GetStat()) != 3 {
		t.Fatalf("decoded %d stats, want 3", len(resp.GetStat()))
	}
	if resp.Stat[0].Name != "inbound>>>inbound-443-vmess>>>traffic>>>uplink" || resp.Stat[0].Value != 1<<40 {
		t.Errorf("stat[0] = %+v", resp.Stat[0])
	}
	// value=0 是 proto3 默认值，编码时被省略；解码必须给出 0 而不是丢掉整条。
	if resp.Stat[1].Name != "user>>>alice@example.com>>>traffic>>>downlink" || resp.Stat[1].Value != 0 {
		t.Errorf("stat[1] = %+v, want the zero-valued counter preserved", resp.Stat[1])
	}
	if resp.Stat[2].Value != 7 {
		t.Errorf("stat[2].Value = %d, want 7", resp.Stat[2].Value)
	}
}

// TestUnmarshalSkipsUnknownFields 保证 sing-box 将来给消息加字段时旧面板不炸。
func TestUnmarshalSkipsUnknownFields(t *testing.T) {
	var stat []byte
	stat = protowire.AppendTag(stat, 1, protowire.BytesType)
	stat = protowire.AppendString(stat, "inbound>>>x>>>traffic>>>uplink")
	stat = protowire.AppendTag(stat, 2, protowire.VarintType)
	stat = protowire.AppendVarint(stat, 42)
	// 一个未来才会出现的字段 9（fixed64）。
	stat = protowire.AppendTag(stat, 9, protowire.Fixed64Type)
	stat = protowire.AppendFixed64(stat, 1234)

	var wire []byte
	wire = protowire.AppendTag(wire, 1, protowire.BytesType)
	wire = protowire.AppendBytes(wire, stat)
	// 响应级别的未知字段。
	wire = protowire.AppendTag(wire, 5, protowire.BytesType)
	wire = protowire.AppendString(wire, "ignored")

	resp := &QueryStatsResponse{}
	if err := unmarshalQueryStatsResponse(wire, resp); err != nil {
		t.Fatalf("unmarshal with unknown fields: %v", err)
	}
	if len(resp.Stat) != 1 || resp.Stat[0].Value != 42 {
		t.Fatalf("decoded %+v, want a single stat with value 42", resp.Stat)
	}
}

func TestUnmarshalRejectsTruncatedInput(t *testing.T) {
	wire := encodeResponse([]*Stat{{Name: "inbound>>>x>>>traffic>>>uplink", Value: 1}})
	resp := &QueryStatsResponse{}
	if err := unmarshalQueryStatsResponse(wire[:len(wire)-2], resp); err == nil {
		t.Error("a truncated response was accepted")
	}
}

// TestCodecRejectsForeignTypes 确认 codec 不会把别的类型当成 StatsService 消息，
// 那种情况下发出去的会是一段无意义的字节流。
func TestCodecRejectsForeignTypes(t *testing.T) {
	if _, err := (codec{}).Marshal("not a request"); err == nil {
		t.Error("Marshal accepted a non-request value")
	}
	if err := (codec{}).Unmarshal(nil, &Stat{}); err == nil {
		t.Error("Unmarshal accepted a non-response target")
	}
	if name := (codec{}).Name(); name != "proto" {
		t.Errorf("codec name = %q, want proto so the content-subtype stays standard", name)
	}
}

// fakeConn 是 grpc.ClientConnInterface 的最小替身，
// 用来在不起服务端的情况下断言客户端调用的方法名与选项。
type fakeConn struct {
	method string
	opts   []grpc.CallOption
	err    error
	reply  []*Stat
}

func (f *fakeConn) Invoke(_ context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	f.method = method
	f.opts = opts
	if f.err != nil {
		return f.err
	}
	if _, ok := args.(*QueryStatsRequest); !ok {
		return errors.New("unexpected request type")
	}
	out, ok := reply.(*QueryStatsResponse)
	if !ok {
		return errors.New("unexpected reply type")
	}
	out.Stat = f.reply
	return nil
}

func (f *fakeConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, errors.New("streaming is not used")
}

func TestClientInvokesTheV2RayMethodWithOurCodec(t *testing.T) {
	conn := &fakeConn{reply: []*Stat{{Name: "inbound>>>a>>>traffic>>>uplink", Value: 5}}}
	client := NewStatsServiceClient(conn)

	resp, err := client.QueryStats(context.Background(), &QueryStatsRequest{Reset_: true})
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if conn.method != QueryStatsFullMethod {
		t.Errorf("invoked %q, want %q", conn.method, QueryStatsFullMethod)
	}
	if len(conn.opts) == 0 {
		t.Fatal("the call carries no options; without ForceCodec grpc cannot marshal our types")
	}
	if len(resp.GetStat()) != 1 || resp.Stat[0].Value != 5 {
		t.Errorf("response = %+v", resp.GetStat())
	}
}

func TestClientPropagatesErrors(t *testing.T) {
	want := errors.New("connection refused")
	client := NewStatsServiceClient(&fakeConn{err: want})
	if _, err := client.QueryStats(context.Background(), &QueryStatsRequest{}); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

/* ---------- helpers ---------- */

func encodeResponse(stats []*Stat) []byte {
	var wire []byte
	for _, s := range stats {
		var body []byte
		if s.Name != "" {
			body = protowire.AppendTag(body, 1, protowire.BytesType)
			body = protowire.AppendString(body, s.Name)
		}
		if s.Value != 0 {
			body = protowire.AppendTag(body, 2, protowire.VarintType)
			body = protowire.AppendVarint(body, uint64(s.Value))
		}
		wire = protowire.AppendTag(wire, 1, protowire.BytesType)
		wire = protowire.AppendBytes(wire, body)
	}
	return wire
}

// decodeFields 把一段编码解析成 field number → 值，供断言使用。
// 重复的 BytesType 字段聚合成 []string。
func decodeFields(t *testing.T, data []byte) map[protowire.Number]any {
	t.Helper()

	out := map[protowire.Number]any{}
	rest := data
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 {
			t.Fatalf("consume tag: %v", protowire.ParseError(n))
		}
		rest = rest[n:]
		switch typ {
		case protowire.VarintType:
			v, m := protowire.ConsumeVarint(rest)
			if m < 0 {
				t.Fatalf("consume varint: %v", protowire.ParseError(m))
			}
			out[num] = v
			rest = rest[m:]
		case protowire.BytesType:
			b, m := protowire.ConsumeBytes(rest)
			if m < 0 {
				t.Fatalf("consume bytes: %v", protowire.ParseError(m))
			}
			s := string(bytes.Clone(b))
			if prev, ok := out[num]; ok {
				switch p := prev.(type) {
				case string:
					out[num] = []string{p, s}
				case []string:
					out[num] = append(p, s)
				}
			} else {
				out[num] = s
			}
			rest = rest[m:]
		default:
			t.Fatalf("unexpected wire type %v for field %d", typ, num)
		}
	}
	return out
}
