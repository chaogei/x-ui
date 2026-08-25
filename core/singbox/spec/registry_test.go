package spec

import "testing"

// expectedProtocols 是面板承诺支持的 14 种协议。
// 这份清单在 README 与前端 defaults 里都有体现，任何增删都必须是有意为之。
var expectedProtocols = []string{
	"vmess", "vless", "trojan", "shadowsocks",
	"hysteria2", "tuic", "anytls", "shadowtls", "naive", "wireguard",
	"socks", "http", "mixed",
	"direct",
}

func TestRegistryHasFourteenProtocols(t *testing.T) {
	if len(registry) != len(expectedProtocols) {
		t.Fatalf("registry has %d protocols, want %d", len(registry), len(expectedProtocols))
	}
	if len(order) != len(registry) {
		t.Fatalf("order has %d entries but registry has %d", len(order), len(registry))
	}
}

func TestAllReturnsRegistryInOrder(t *testing.T) {
	all := All()
	if len(all) != len(expectedProtocols) {
		t.Fatalf("All() returned %d specs, want %d", len(all), len(expectedProtocols))
	}
	for i, want := range expectedProtocols {
		if all[i].Key != want {
			t.Errorf("All()[%d].Key = %q, want %q", i, all[i].Key, want)
		}
	}
}

// TestAllReturnsCopy 保证调用方修改返回值不会污染注册表。
func TestAllReturnsCopy(t *testing.T) {
	first := All()
	first[0].Key = "tampered"
	second := All()
	if second[0].Key != expectedProtocols[0] {
		t.Errorf("mutating All() result leaked into the registry: got %q", second[0].Key)
	}
}

func TestGetAndMustGet(t *testing.T) {
	for _, key := range expectedProtocols {
		s, ok := Get(key)
		if !ok {
			t.Errorf("Get(%q) reported the protocol as unregistered", key)
			continue
		}
		if s.Key != key {
			t.Errorf("Get(%q).Key = %q", key, s.Key)
		}
		if MustGet(key).Key != key {
			t.Errorf("MustGet(%q) returned the wrong spec", key)
		}
	}

	if _, ok := Get("definitely-not-a-protocol"); ok {
		t.Error("Get reported an unknown protocol as registered")
	}

	defer func() {
		if recover() == nil {
			t.Error("MustGet on an unknown protocol should panic")
		}
	}()
	MustGet("definitely-not-a-protocol")
}

func TestNetworkValuesAreValid(t *testing.T) {
	valid := map[string]bool{"tcp": true, "udp": true, "both": true}
	for _, s := range All() {
		if !valid[s.Network] {
			t.Errorf("%s has invalid network %q; port-conflict checks depend on this", s.Key, s.Network)
		}
	}
}

// TestUserSchemaConsistency 校验 UserSchema 三元组自身的语义一致性。
func TestUserSchemaConsistency(t *testing.T) {
	for _, s := range All() {
		if s.Users.Identifier == "" {
			if s.Users.Container != "" || len(s.Users.Credentials) > 0 {
				t.Errorf("%s has no identifier but declares container/credentials", s.Key)
			}
			if s.HasUsers() {
				t.Errorf("%s has no identifier but HasUsers() is true", s.Key)
			}
			continue
		}
		if !s.HasUsers() {
			t.Errorf("%s declares identifier %q but HasUsers() is false", s.Key, s.Users.Identifier)
		}
		for _, cred := range s.Users.Credentials {
			if cred == s.Users.Identifier {
				t.Errorf("%s lists its identifier %q again under credentials", s.Key, cred)
			}
		}
	}
}

// TestEndpointsAreNotSniffable 记录 sing-box 1.11+ 的约束：
// endpoint 类协议不经过 sniff 流水线。
func TestEndpointsAreNotSniffable(t *testing.T) {
	for _, s := range All() {
		if s.IsEndpoint && s.Sniffable {
			t.Errorf("%s is an endpoint, it cannot accept sniff options", s.Key)
		}
	}
}
