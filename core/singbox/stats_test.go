package singbox

import (
	"testing"

	"x-ui/core"
	"x-ui/core/singbox/statspb"
)

// TestAggregateTraffic 是流量名解析的表驱动护栏。
//
// 这段逻辑的每一种错法都很安静：多算一个 api 的字节、把 user 维度当成
// inbound、uplink/downlink 装反——面板照样出数，只是数是错的，
// 而多用户配额与限速全建立在这几个数上。
func TestAggregateTraffic(t *testing.T) {
	tests := map[string]struct {
		stats []*statspb.Stat
		want  []core.Traffic
	}{
		"inbound uplink and downlink fold into one row": {
			stats: []*statspb.Stat{
				{Name: "inbound>>>inbound-443-vmess>>>traffic>>>uplink", Value: 100},
				{Name: "inbound>>>inbound-443-vmess>>>traffic>>>downlink", Value: 200},
			},
			want: []core.Traffic{
				{IsInbound: true, Tag: "inbound-443-vmess", Up: 100, Down: 200},
			},
		},
		"user counters become the user dimension": {
			stats: []*statspb.Stat{
				{Name: "user>>>alice@example.com>>>traffic>>>uplink", Value: 11},
				{Name: "user>>>alice@example.com>>>traffic>>>downlink", Value: 22},
			},
			want: []core.Traffic{
				{IsUser: true, Tag: "alice@example.com", Up: 11, Down: 22},
			},
		},
		"outbound counters are neither inbound nor user": {
			stats: []*statspb.Stat{
				{Name: "outbound>>>direct>>>traffic>>>uplink", Value: 5},
			},
			want: []core.Traffic{
				{Tag: "direct", Up: 5},
			},
		},
		"the api inbound is skipped": {
			stats: []*statspb.Stat{
				{Name: "inbound>>>api>>>traffic>>>uplink", Value: 999},
				{Name: "inbound>>>api>>>traffic>>>downlink", Value: 999},
			},
			want: nil,
		},
		"a user literally named api is still counted": {
			stats: []*statspb.Stat{
				{Name: "user>>>api>>>traffic>>>uplink", Value: 3},
			},
			want: []core.Traffic{
				{IsUser: true, Tag: "api", Up: 3},
			},
		},
		"same tag on different dimensions stays separate": {
			stats: []*statspb.Stat{
				{Name: "inbound>>>shared>>>traffic>>>uplink", Value: 1},
				{Name: "user>>>shared>>>traffic>>>uplink", Value: 2},
				{Name: "outbound>>>shared>>>traffic>>>uplink", Value: 3},
			},
			want: []core.Traffic{
				{IsInbound: true, Tag: "shared", Up: 1},
				{IsUser: true, Tag: "shared", Up: 2},
				{Tag: "shared", Up: 3},
			},
		},
		"tags containing separators are preserved": {
			stats: []*statspb.Stat{
				{Name: "user>>>a>>>b>>>traffic>>>uplink", Value: 8},
			},
			want: []core.Traffic{
				{IsUser: true, Tag: "a>>>b", Up: 8},
			},
		},
		"malformed names are dropped": {
			stats: []*statspb.Stat{
				{Name: ""},
				{Name: "inbound>>>x>>>traffic"},
				{Name: "garbage"},
				{Name: "inbound>>>x>>>traffic>>>sideways"},
				{Name: "prefixinbound>>>x>>>traffic>>>uplink"},
				nil,
			},
			want: nil,
		},
		"zero-valued counters still produce a row": {
			stats: []*statspb.Stat{
				{Name: "inbound>>>idle>>>traffic>>>uplink", Value: 0},
			},
			want: []core.Traffic{
				{IsInbound: true, Tag: "idle"},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := aggregateTraffic(tc.stats)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d rows %s, want %d", len(got), formatTraffic(got), len(tc.want))
			}
			for i, want := range tc.want {
				g := got[i]
				if g.IsInbound != want.IsInbound || g.IsUser != want.IsUser ||
					g.Tag != want.Tag || g.Up != want.Up || g.Down != want.Down {
					t.Errorf("row %d = %+v, want %+v", i, *g, want)
				}
			}
		})
	}
}

// TestAggregateTrafficKeepsInputOrder 顺序是隐含契约：
// 上层按返回顺序写库，抖动会让测试与日志难以比对。
func TestAggregateTrafficKeepsInputOrder(t *testing.T) {
	stats := []*statspb.Stat{
		{Name: "inbound>>>c>>>traffic>>>uplink", Value: 1},
		{Name: "inbound>>>a>>>traffic>>>uplink", Value: 1},
		{Name: "inbound>>>b>>>traffic>>>uplink", Value: 1},
		{Name: "inbound>>>a>>>traffic>>>downlink", Value: 2},
	}
	got := aggregateTraffic(stats)
	want := []string{"c", "a", "b"}
	if len(got) != len(want) {
		t.Fatalf("got %s", formatTraffic(got))
	}
	for i := range want {
		if got[i].Tag != want[i] {
			t.Errorf("row %d tag = %q, want %q", i, got[i].Tag, want[i])
		}
	}
}

func formatTraffic(rows []*core.Traffic) string {
	out := "["
	for i, r := range rows {
		if i > 0 {
			out += " "
		}
		out += r.Tag
	}
	return out + "]"
}
