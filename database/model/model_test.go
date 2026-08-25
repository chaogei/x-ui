package model

import (
	"encoding/json"
	"testing"

	"x-ui/core/singbox/spec"
)

// TestEveryProtocolConstantIsRegistered 与 model 包的 init 自检重复，
// 但 init 的 panic 只在包被加载时触发，测试里显式断言可以给出更好的失败信息。
func TestEveryProtocolConstantIsRegistered(t *testing.T) {
	protocols := allProtocols()
	if len(protocols) != 14 {
		t.Fatalf("model declares %d protocols, want 14", len(protocols))
	}
	for _, p := range protocols {
		if _, ok := spec.Get(string(p)); !ok {
			t.Errorf("model.Protocol %q is not registered in core/singbox/spec", p)
		}
	}
}

// TestRegistryHasNoUnusedProtocols 反向检查：spec 里注册的每个协议
// 都必须有对应的 model 常量，否则前端能选、后端建不出。
func TestRegistryHasNoUnusedProtocols(t *testing.T) {
	declared := map[string]bool{}
	for _, p := range allProtocols() {
		declared[string(p)] = true
	}
	for _, s := range spec.All() {
		if !declared[s.Key] {
			t.Errorf("spec registers %q but model has no Protocol constant for it", s.Key)
		}
	}
}

func TestProtocolNetwork(t *testing.T) {
	cases := map[Protocol]string{
		VMess: "tcp", VLESS: "tcp", Trojan: "tcp", Shadowsocks: "tcp",
		Hysteria2: "udp", TUIC: "udp", WireGuard: "udp",
		Socks: "both", Mixed: "both",
		HTTP: "tcp", Direct: "tcp", AnyTLS: "tcp", ShadowTLS: "tcp", Naive: "tcp",
	}
	for p, want := range cases {
		if got := p.Network(); got != want {
			t.Errorf("%s.Network() = %q, want %q", p, got, want)
		}
	}

	// 数据库里的历史脏数据必须保守回退到 tcp（最严格的冲突语义）。
	if got := Protocol("some-removed-protocol").Network(); got != "tcp" {
		t.Errorf("unknown protocol Network() = %q, want tcp fallback", got)
	}
}

// TestConflictsWithMatrix 覆盖 tcp/udp/both 的完整组合。
// 端口冲突判定直接决定用户能否在同一端口共存 TCP 与 UDP 入站。
func TestConflictsWithMatrix(t *testing.T) {
	tcp, udp, both := VMess, Hysteria2, Socks

	cases := []struct {
		a, b Protocol
		want bool
		why  string
	}{
		{tcp, tcp, true, "two tcp protocols cannot share a port"},
		{udp, udp, true, "two udp protocols cannot share a port"},
		{tcp, udp, false, "tcp and udp may share a port"},
		{udp, tcp, false, "udp and tcp may share a port"},
		{both, tcp, true, "a dual-stack listener conflicts with tcp"},
		{both, udp, true, "a dual-stack listener conflicts with udp"},
		{tcp, both, true, "tcp conflicts with a dual-stack listener"},
		{udp, both, true, "udp conflicts with a dual-stack listener"},
		{both, both, true, "two dual-stack listeners conflict"},
	}
	for _, tc := range cases {
		if got := tc.a.ConflictsWith(tc.b); got != tc.want {
			t.Errorf("%s.ConflictsWith(%s) = %v, want %v (%s)", tc.a, tc.b, got, tc.want, tc.why)
		}
	}
}

func TestIsEndpoint(t *testing.T) {
	if !WireGuard.IsEndpoint() {
		t.Error("wireguard must be an endpoint on sing-box 1.11+")
	}
	for _, p := range allProtocols() {
		if p == WireGuard {
			continue
		}
		if p.IsEndpoint() {
			t.Errorf("%s should not be an endpoint", p)
		}
	}
	if Protocol("unknown").IsEndpoint() {
		t.Error("unknown protocols must not be treated as endpoints")
	}
}

func TestBuildSingBoxInbound(t *testing.T) {
	ib := &Inbound{
		Listen:   "127.0.0.1",
		Port:     8443,
		Protocol: VLESS,
		Tag:      "inbound-8443-vless",
		Settings: `{"users":[{"uuid":"x"}]}`,
		Sniffing: `{"sniff":true}`,
	}
	built := ib.BuildSingBoxInbound()
	if built.Type != "vless" || built.Tag != ib.Tag || built.Listen != ib.Listen || built.ListenPort != ib.Port {
		t.Fatalf("typed fields not carried over: %+v", built)
	}

	out, err := json.Marshal(built)
	if err != nil {
		t.Fatalf("marshal built inbound: %v", err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("built inbound is not valid JSON: %v", err)
	}
	for _, key := range []string{"type", "tag", "listen", "listen_port", "users", "sniff"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("built inbound is missing key %q; got %s", key, out)
		}
	}
}
