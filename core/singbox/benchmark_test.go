package singbox

import (
	"fmt"
	"strconv"
	"testing"

	"x-ui/core"
	"x-ui/core/singbox/statspb"
	"x-ui/util/json_util"
)

var (
	benchmarkBytesSink   []byte
	benchmarkBoolSink    bool
	benchmarkMatchesSink []string
	benchmarkTrafficSink []*core.Traffic
)

// BenchmarkInboundConfigMarshalJSON exercises every protocol fixture used by
// the golden tests while measuring the custom byte-level marshaler directly.
func BenchmarkInboundConfigMarshalJSON(b *testing.B) {
	for _, tc := range allProtocolCases() {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			var (
				out []byte
				err error
			)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err = tc.inbound.MarshalJSON()
			}
			if err != nil {
				b.Fatal(err)
			}
			benchmarkBytesSink = out
		})
	}
}

func BenchmarkConfigEquals(b *testing.B) {
	for _, size := range []int{1, 16, 128} {
		size := size
		b.Run(strconv.Itoa(size)+"/equal", func(b *testing.B) {
			left, right := benchmarkConfig(size), benchmarkConfig(size)
			var equal bool
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				equal = left.Equals(right)
			}
			benchmarkBoolSink = equal
		})

		b.Run(strconv.Itoa(size)+"/tail_difference", func(b *testing.B) {
			left, right := benchmarkConfig(size), benchmarkConfig(size)
			right.Inbounds[len(right.Inbounds)-1].Settings = json_util.RawMessage(`{"changed":true}`)
			var equal bool
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				equal = left.Equals(right)
			}
			benchmarkBoolSink = equal
		})
	}
}

func BenchmarkStatsNameParse(b *testing.B) {
	cases := []struct {
		name string
		stat string
	}{
		{name: "inbound", stat: "inbound>>>inbound-443-vmess>>>traffic>>>uplink"},
		{name: "user_with_separator", stat: "user>>>tenant>>>alice@example.com>>>traffic>>>downlink"},
		{name: "malformed", stat: "inbound>>>missing-direction>>>traffic"},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			var matches []string
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				matches = trafficRegex.FindStringSubmatch(tc.stat)
			}
			benchmarkMatchesSink = matches
		})
	}
}

func BenchmarkAggregateTraffic(b *testing.B) {
	for _, groups := range []int{1, 100, 1000} {
		stats := benchmarkStats(groups)
		b.Run(strconv.Itoa(len(stats))+"_counters", func(b *testing.B) {
			var result []*core.Traffic
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result = aggregateTraffic(stats)
			}
			benchmarkTrafficSink = result
		})
	}
}

func benchmarkConfig(inboundCount int) *Config {
	cases := allProtocolCases()
	inbounds := make([]InboundConfig, inboundCount)
	for i := range inbounds {
		src := cases[i%len(cases)].inbound
		inbounds[i] = InboundConfig{
			Type:       src.Type,
			Tag:        src.Tag,
			Listen:     src.Listen,
			ListenPort: src.ListenPort,
			Settings:   append(json_util.RawMessage(nil), src.Settings...),
			Sniff:      append(json_util.RawMessage(nil), src.Sniff...),
		}
	}
	return &Config{
		Log:          json_util.RawMessage(`{"level":"info"}`),
		DNS:          json_util.RawMessage(`{"servers":[{"tag":"local","address":"local"}]}`),
		Inbounds:     inbounds,
		Outbounds:    json_util.RawMessage(`[{"type":"direct","tag":"direct"}]`),
		Route:        json_util.RawMessage(`{"final":"direct"}`),
		Experimental: json_util.RawMessage(`{"v2ray_api":{"listen":"127.0.0.1:62789"}}`),
	}
}

func benchmarkStats(groups int) []*statspb.Stat {
	stats := make([]*statspb.Stat, 0, groups*2)
	kinds := [...]string{"inbound", "user", "outbound"}
	for i := 0; i < groups; i++ {
		kind := kinds[i%len(kinds)]
		tag := fmt.Sprintf("target-%04d@example.com", i)
		stats = append(stats,
			&statspb.Stat{Name: kind + ">>>" + tag + ">>>traffic>>>uplink", Value: int64(i + 1)},
			&statspb.Stat{Name: kind + ">>>" + tag + ">>>traffic>>>downlink", Value: int64(i + 2)},
		)
	}
	return stats
}
