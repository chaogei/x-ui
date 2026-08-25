package singbox

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-ui/util/json_util"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata/")

// protocolCase 是一个协议的最小可信 inbound 样例。
// 覆盖 spec 注册表里全部 14 种协议，settings 片段按 sing-box 官方 schema 书写。
type protocolCase struct {
	name      string
	inbound   InboundConfig
	skipSniff bool
}

func allProtocolCases() []protocolCase {
	sniff := json_util.RawMessage(`{"sniff":true,"sniff_override_destination":true}`)

	return []protocolCase{
		{
			name: "vmess",
			inbound: InboundConfig{
				Type: "vmess", Tag: "inbound-10001-vmess", Listen: "0.0.0.0", ListenPort: 10001,
				Settings: json_util.RawMessage(`{"users":[{"name":"u1","uuid":"11111111-1111-1111-1111-111111111111","alterId":0}],"tls":{"enabled":false}}`),
				Sniff:    sniff,
			},
		},
		{
			name: "vless_reality",
			inbound: InboundConfig{
				Type: "vless", Tag: "inbound-10002-vless", Listen: "0.0.0.0", ListenPort: 10002,
				Settings: json_util.RawMessage(`{"users":[{"name":"u1","uuid":"22222222-2222-2222-2222-222222222222","flow":"xtls-rprx-vision"}],"tls":{"enabled":true,"server_name":"www.example.com","reality":{"enabled":true,"handshake":{"server":"www.example.com","server_port":443},"private_key":"UIJEyLnaMj3vRJJHVPz5WSPTvJvVJqQ7Yq0kZUuAHFk","public_key":"5rSVKp0zC0lTBSjJPjnQZmM8t2GJyCkFvFsGYPuSMH0","short_id":["0123abcd"]}}}`),
				Sniff:    sniff,
			},
		},
		{
			name: "trojan",
			inbound: InboundConfig{
				Type: "trojan", Tag: "inbound-10003-trojan", ListenPort: 10003,
				Settings: json_util.RawMessage(`{"users":[{"name":"u1","password":"s3cret"}],"tls":{"enabled":true,"server_name":"trojan.example.com"}}`),
				Sniff:    sniff,
			},
		},
		{
			name: "shadowsocks",
			inbound: InboundConfig{
				Type: "shadowsocks", Tag: "inbound-10004-shadowsocks", ListenPort: 10004,
				Settings: json_util.RawMessage(`{"method":"2022-blake3-aes-128-gcm","password":"c2hvcnQta2V5LTEyMzQ1Ng==","network":"tcp"}`),
				Sniff:    sniff,
			},
		},
		{
			name: "hysteria2",
			inbound: InboundConfig{
				Type: "hysteria2", Tag: "inbound-10005-hysteria2", ListenPort: 10005,
				Settings: json_util.RawMessage(`{"up_mbps":100,"down_mbps":100,"users":[{"name":"u1","password":"hy2pass"}],"ignore_client_bandwidth":false,"tls":{"enabled":true,"server_name":"hy2.example.com"}}`),
				Sniff:    sniff,
			},
		},
		{
			name: "tuic",
			inbound: InboundConfig{
				Type: "tuic", Tag: "inbound-10006-tuic", ListenPort: 10006,
				Settings: json_util.RawMessage(`{"users":[{"name":"u1","uuid":"33333333-3333-3333-3333-333333333333","password":"tuicpass"}],"congestion_control":"bbr","auth_timeout":"3s","zero_rtt_handshake":false,"heartbeat":"10s","tls":{"enabled":true}}`),
				Sniff:    sniff,
			},
		},
		{
			name: "anytls",
			inbound: InboundConfig{
				Type: "anytls", Tag: "inbound-10007-anytls", ListenPort: 10007,
				Settings: json_util.RawMessage(`{"users":[{"name":"u1","password":"anytlspass"}],"tls":{"enabled":true}}`),
				Sniff:    sniff,
			},
		},
		{
			name: "shadowtls",
			inbound: InboundConfig{
				Type: "shadowtls", Tag: "inbound-10008-shadowtls", ListenPort: 10008,
				Settings: json_util.RawMessage(`{"version":3,"users":[{"name":"u1","password":"stlspass"}],"handshake":{"server":"www.microsoft.com","server_port":443},"strict_mode":false}`),
				Sniff:    sniff,
			},
		},
		{
			name: "naive",
			inbound: InboundConfig{
				Type: "naive", Tag: "inbound-10009-naive", ListenPort: 10009,
				Settings: json_util.RawMessage(`{"users":[{"username":"nuser","password":"npass"}],"tls":{"enabled":true}}`),
				Sniff:    sniff,
			},
		},
		{
			name: "wireguard",
			inbound: InboundConfig{
				Type: "wireguard", Tag: "inbound-10010-wireguard", ListenPort: 10010,
				Settings: json_util.RawMessage(`{"system":false,"mtu":1420,"address":["10.0.0.1/32"],"private_key":"wgprivkey","peers":[{"address":"1.2.3.4","port":51820,"public_key":"wgpubkey","allowed_ips":["0.0.0.0/0"],"persistent_keepalive_interval":0}]}`),
			},
			skipSniff: true,
		},
		{
			name: "socks",
			inbound: InboundConfig{
				Type: "socks", Tag: "inbound-10011-socks", ListenPort: 10011,
				Settings: json_util.RawMessage(`{"users":[{"username":"suser","password":"spass"}]}`),
				Sniff:    sniff,
			},
		},
		{
			name: "http",
			inbound: InboundConfig{
				Type: "http", Tag: "inbound-10012-http", ListenPort: 10012,
				Settings: json_util.RawMessage(`{"users":[{"username":"huser","password":"hpass"}]}`),
				Sniff:    sniff,
			},
		},
		{
			name: "mixed",
			inbound: InboundConfig{
				Type: "mixed", Tag: "inbound-10013-mixed", ListenPort: 10013,
				Settings: json_util.RawMessage(`{"users":[{"username":"muser","password":"mpass"}]}`),
				Sniff:    sniff,
			},
		},
		{
			name: "direct",
			inbound: InboundConfig{
				Type: "direct", Tag: "inbound-10014-direct", ListenPort: 10014,
				Settings: json_util.RawMessage(`{"override_address":"1.1.1.1","override_port":53,"network":"udp"}`),
			},
			skipSniff: true,
		},
	}
}

// TestInboundConfigMarshalJSONGolden 锁定 14 种协议的序列化输出。
//
// MarshalJSON 走的是 byte-level 拼接而非 map 合并，任何拼接边界的改动
// （逗号、空 settings、保留键处理）都会在这里暴露。
func TestInboundConfigMarshalJSONGolden(t *testing.T) {
	for _, tc := range allProtocolCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(&tc.inbound)
			if err != nil {
				t.Fatalf("marshal %s: %v", tc.name, err)
			}

			// 输出必须是合法 JSON 且能重新解析回等价的 key 集合。
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(got, &parsed); err != nil {
				t.Fatalf("marshalled %s is not valid JSON: %v\n%s", tc.name, err, got)
			}
			if string(parsed["type"]) != `"`+tc.inbound.Type+`"` {
				t.Errorf("type = %s, want %q", parsed["type"], tc.inbound.Type)
			}

			var indented []byte
			indented, err = json.MarshalIndent(json.RawMessage(got), "", "  ")
			if err != nil {
				t.Fatalf("indent %s: %v", tc.name, err)
			}
			indented = append(indented, '\n')

			goldenPath := filepath.Join("testdata", "inbound_"+tc.name+".golden.json")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath, indented, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run `go test ./core/singbox -update` to create): %v", err)
			}
			if string(want) != string(indented) {
				t.Errorf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", tc.name, want, indented)
			}
		})
	}
}

// TestInboundConfigCoversEveryProtocol 保证上面的样例表没有漏掉任何注册协议。
func TestInboundConfigCoversEveryProtocol(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range allProtocolCases() {
		covered[tc.inbound.Type] = true
	}
	want := []string{
		"vmess", "vless", "trojan", "shadowsocks", "hysteria2", "tuic",
		"anytls", "shadowtls", "naive", "wireguard", "socks", "http", "mixed", "direct",
	}
	if len(covered) != len(want) {
		t.Fatalf("covered %d protocols, want %d", len(covered), len(want))
	}
	for _, p := range want {
		if !covered[p] {
			t.Errorf("protocol %q has no golden case", p)
		}
	}
}

func TestInboundConfigMarshalRejectsNonObjectSettings(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"array", `["a","b"]`},
		{"string", `"just a string"`},
		{"number", `42`},
		{"bool", `true`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ib := InboundConfig{Type: "vmess", Settings: json_util.RawMessage(tc.raw)}
			_, err := json.Marshal(&ib)
			if err == nil {
				t.Fatalf("expected error for settings %s", tc.raw)
			}
			if !strings.Contains(err.Error(), "expect JSON object") {
				t.Errorf("error = %v, want it to mention 'expect JSON object'", err)
			}
		})
	}
}

func TestInboundConfigMarshalSkipsEmptySettings(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", "  {  }  "} {
		ib := InboundConfig{Type: "direct", ListenPort: 1080, Settings: json_util.RawMessage(raw)}
		got, err := json.Marshal(&ib)
		if err != nil {
			t.Fatalf("marshal with settings %q: %v", raw, err)
		}
		want := `{"type":"direct","listen_port":1080}`
		if string(got) != want {
			t.Errorf("settings %q produced %s, want %s", raw, got, want)
		}
	}
}

func TestInboundConfigUnmarshalRoundTrip(t *testing.T) {
	src := `{"type":"vmess","tag":"t1","listen":"127.0.0.1","listen_port":8080,"users":[{"uuid":"u"}],"sniff":true}`
	var ib InboundConfig
	if err := json.Unmarshal([]byte(src), &ib); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ib.Type != "vmess" || ib.Tag != "t1" || ib.Listen != "127.0.0.1" || ib.ListenPort != 8080 {
		t.Fatalf("typed fields not captured: %+v", ib)
	}
	// 强类型字段必须从 Settings 里剔除，否则再次 Marshal 会产生重复键。
	var settings map[string]any
	if err := json.Unmarshal(ib.Settings, &settings); err != nil {
		t.Fatalf("settings is not an object: %v", err)
	}
	for _, reserved := range []string{"type", "tag", "listen", "listen_port"} {
		if _, ok := settings[reserved]; ok {
			t.Errorf("settings still carries reserved key %q", reserved)
		}
	}

	out, err := json.Marshal(&ib)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-marshalled output invalid: %v", err)
	}
	if _, ok := got["users"]; !ok {
		t.Error("users field lost in round trip")
	}
	if string(got["listen_port"]) != "8080" {
		t.Errorf("listen_port = %s, want 8080", got["listen_port"])
	}
}

func TestInboundConfigEquals(t *testing.T) {
	base := InboundConfig{Type: "vmess", Tag: "a", Listen: "0.0.0.0", ListenPort: 1,
		Settings: json_util.RawMessage(`{"x":1}`)}

	same := base
	if !base.Equals(&same) {
		t.Error("identical configs should be equal")
	}

	for name, mutate := range map[string]func(*InboundConfig){
		"type":     func(c *InboundConfig) { c.Type = "vless" },
		"tag":      func(c *InboundConfig) { c.Tag = "b" },
		"listen":   func(c *InboundConfig) { c.Listen = "127.0.0.1" },
		"port":     func(c *InboundConfig) { c.ListenPort = 2 },
		"settings": func(c *InboundConfig) { c.Settings = json_util.RawMessage(`{"x":2}`) },
	} {
		other := base
		mutate(&other)
		if base.Equals(&other) {
			t.Errorf("configs differing in %s should not be equal", name)
		}
	}
}
