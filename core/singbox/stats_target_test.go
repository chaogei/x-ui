package singbox

import (
	"encoding/json"
	"testing"

	"x-ui/util/json_util"
)

// statsOf 取出 experimental.v2ray_api.stats 供断言。
func statsOf(t *testing.T, raw json_util.RawMessage) map[string]interface{} {
	t.Helper()

	var probe struct {
		V2RayAPI struct {
			Listen string                 `json:"listen"`
			Stats  map[string]interface{} `json:"stats"`
		} `json:"v2ray_api"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("parse experimental %s: %v", raw, err)
	}
	return probe.V2RayAPI.Stats
}

func stringsOf(t *testing.T, v interface{}) []string {
	t.Helper()
	list, ok := v.([]interface{})
	if !ok {
		t.Fatalf("value %v is not a JSON array", v)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("array item %v is not a string", item)
		}
		out = append(out, s)
	}
	return out
}

// TestSetStatsTargetsPopulatesTheWhitelist 是流量统计能否工作的关键断言。
//
// sing-box 只为 stats.inbounds / stats.users 里列出的名字建计数器。
// 默认模板里 inbounds 是空数组、users 根本不存在，所以在这个方法出现之前
// 面板拉到的永远是一个空的统计列表。
func TestSetStatsTargetsPopulatesTheWhitelist(t *testing.T) {
	cfg := &Config{}
	if err := json.Unmarshal([]byte(DefaultTemplate), cfg); err != nil {
		t.Fatalf("parse the default template: %v", err)
	}

	if err := cfg.SetStatsTargets([]string{"inbound-443-vmess", "inbound-8388-shadowsocks"}, []string{"alice@x"}); err != nil {
		t.Fatalf("SetStatsTargets: %v", err)
	}

	stats := statsOf(t, cfg.Experimental)
	if got := stringsOf(t, stats["inbounds"]); len(got) != 2 || got[0] != "inbound-443-vmess" {
		t.Errorf("stats.inbounds = %v", got)
	}
	if got := stringsOf(t, stats["users"]); len(got) != 1 || got[0] != "alice@x" {
		t.Errorf("stats.users = %v", got)
	}
	if stats["enabled"] != true {
		t.Errorf("stats.enabled = %v, want true", stats["enabled"])
	}

	// listen 是运维可配的，重写统计白名单不能把它弄丢。
	var probe struct {
		V2RayAPI struct {
			Listen string `json:"listen"`
		} `json:"v2ray_api"`
	}
	if err := json.Unmarshal(cfg.Experimental, &probe); err != nil {
		t.Fatalf("parse experimental: %v", err)
	}
	if probe.V2RayAPI.Listen != "127.0.0.1:62789" {
		t.Errorf("v2ray_api.listen = %q, want it preserved", probe.V2RayAPI.Listen)
	}
}

func TestSetStatsTargetsWritesEmptyArraysNotNull(t *testing.T) {
	cfg := &Config{Experimental: json_util.RawMessage(`{"v2ray_api":{"listen":"127.0.0.1:1","stats":{"enabled":true}}}`)}
	if err := cfg.SetStatsTargets(nil, nil); err != nil {
		t.Fatalf("SetStatsTargets: %v", err)
	}
	stats := statsOf(t, cfg.Experimental)
	if got := stringsOf(t, stats["inbounds"]); len(got) != 0 {
		t.Errorf("stats.inbounds = %v, want an empty array", got)
	}
	if got := stringsOf(t, stats["users"]); len(got) != 0 {
		t.Errorf("stats.users = %v, want an empty array", got)
	}
}

// TestSetStatsTargetsRespectsAnAbsentAPI 运维显式移除 v2ray_api 时
// 面板不能偷偷把它加回来——那会重新打开一个本地监听端口。
func TestSetStatsTargetsRespectsAnAbsentAPI(t *testing.T) {
	cases := map[string]json_util.RawMessage{
		"no experimental block": nil,
		"empty object":          json_util.RawMessage(`{}`),
		"other experimental":    json_util.RawMessage(`{"cache_file":{"enabled":true}}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{Experimental: raw}
			before := string(cfg.Experimental)
			if err := cfg.SetStatsTargets([]string{"a"}, []string{"b"}); err != nil {
				t.Fatalf("SetStatsTargets: %v", err)
			}
			if string(cfg.Experimental) != before {
				t.Errorf("experimental = %s, want it untouched (%s)", cfg.Experimental, before)
			}
		})
	}
}

func TestSetStatsTargetsAddsStatsWhenTheAPIHasNone(t *testing.T) {
	cfg := &Config{Experimental: json_util.RawMessage(`{"v2ray_api":{"listen":"127.0.0.1:1"}}`)}
	if err := cfg.SetStatsTargets([]string{"tag"}, nil); err != nil {
		t.Fatalf("SetStatsTargets: %v", err)
	}
	stats := statsOf(t, cfg.Experimental)
	if stats["enabled"] != true {
		t.Errorf("stats.enabled = %v, want the block to be created enabled", stats["enabled"])
	}
	if got := stringsOf(t, stats["inbounds"]); len(got) != 1 || got[0] != "tag" {
		t.Errorf("stats.inbounds = %v", got)
	}
}

func TestSetStatsTargetsRejectsMalformedExperimental(t *testing.T) {
	cfg := &Config{Experimental: json_util.RawMessage(`{"v2ray_api": "not an object"}`)}
	if err := cfg.SetStatsTargets(nil, nil); err == nil {
		t.Error("a malformed v2ray_api block was accepted")
	}
}

// TestDefaultTemplateShipsAStatsBlock 模板必须留着 v2ray_api，
// 否则面板拿不到任何流量数据，配额与限速全部失效。
func TestDefaultTemplateShipsAStatsBlock(t *testing.T) {
	cfg := &Config{}
	if err := json.Unmarshal([]byte(DefaultTemplate), cfg); err != nil {
		t.Fatalf("parse the default template: %v", err)
	}
	if len(cfg.Experimental) == 0 {
		t.Fatal("the default template has no experimental block")
	}
	if stats := statsOf(t, cfg.Experimental); stats == nil {
		t.Fatal("the default template has no v2ray_api.stats block")
	}
}
