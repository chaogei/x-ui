package singbox

import (
	"encoding/json"
	"testing"

	"x-ui/util/json_util"
)

func TestConfigEquals(t *testing.T) {
	newConfig := func() *Config {
		return &Config{
			Log:   json_util.RawMessage(`{"level":"info"}`),
			DNS:   json_util.RawMessage(`{"servers":[]}`),
			Route: json_util.RawMessage(`{"final":"direct"}`),
			Inbounds: []InboundConfig{
				{Type: "vmess", Tag: "a", ListenPort: 1, Settings: json_util.RawMessage(`{"x":1}`)},
			},
			Endpoints: []InboundConfig{
				{Type: "wireguard", Tag: "wg", ListenPort: 2},
			},
		}
	}

	a, b := newConfig(), newConfig()
	if !a.Equals(b) {
		t.Fatal("identical configs should compare equal")
	}

	t.Run("nil and wrong type", func(t *testing.T) {
		if a.Equals(nil) {
			t.Error("config should not equal nil core.Config")
		}
		var typed *Config
		if a.Equals(typed) {
			t.Error("config should not equal a typed nil")
		}
	})

	mutations := map[string]func(*Config){
		"log":              func(c *Config) { c.Log = json_util.RawMessage(`{"level":"debug"}`) },
		"dns":              func(c *Config) { c.DNS = json_util.RawMessage(`{"servers":[1]}`) },
		"route":            func(c *Config) { c.Route = json_util.RawMessage(`{"final":"block"}`) },
		"ntp":              func(c *Config) { c.NTP = json_util.RawMessage(`{"enabled":true}`) },
		"certificate":      func(c *Config) { c.Certificate = json_util.RawMessage(`{"store":"system"}`) },
		"outbounds":        func(c *Config) { c.Outbounds = json_util.RawMessage(`[{"type":"direct"}]`) },
		"experimental":     func(c *Config) { c.Experimental = json_util.RawMessage(`{"v2ray_api":{}}`) },
		"inbound field":    func(c *Config) { c.Inbounds[0].ListenPort = 9 },
		"inbound count":    func(c *Config) { c.Inbounds = append(c.Inbounds, InboundConfig{Type: "http"}) },
		"endpoint field":   func(c *Config) { c.Endpoints[0].Tag = "wg2" },
		"endpoint count":   func(c *Config) { c.Endpoints = nil },
		"inbound settings": func(c *Config) { c.Inbounds[0].Settings = json_util.RawMessage(`{"x":2}`) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			left, right := newConfig(), newConfig()
			mutate(right)
			if left.Equals(right) {
				t.Errorf("configs differing in %s should not compare equal", name)
			}
		})
	}
}

// TestDefaultTemplateIsValidSingBoxConfig 守住新装面板拿到的默认模板。
//
// 这个常量现在是唯一来源（web/service 直接引用它），一旦写坏，
// 所有新部署的面板都起不了内核，或者起来了但流量统计恒为 0。
func TestDefaultTemplateIsValidSingBoxConfig(t *testing.T) {
	cfg := &Config{}
	if err := json.Unmarshal([]byte(DefaultTemplate), cfg); err != nil {
		t.Fatalf("DefaultTemplate is not a valid sing-box config: %v", err)
	}
	if len(cfg.Experimental) == 0 {
		t.Error("DefaultTemplate must enable experimental.v2ray_api for traffic stats")
	}
	var exp struct {
		V2RayAPI struct {
			Listen string `json:"listen"`
		} `json:"v2ray_api"`
	}
	if err := json.Unmarshal(cfg.Experimental, &exp); err != nil {
		t.Fatalf("experimental block invalid: %v", err)
	}
	if exp.V2RayAPI.Listen == "" {
		t.Error("experimental.v2ray_api.listen must be set, traffic stats depend on it")
	}
}
