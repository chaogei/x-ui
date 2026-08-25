package service

import (
	"strings"
	"testing"

	"x-ui/database/model"
)

func validInbound() *model.Inbound {
	return &model.Inbound{
		Port:     10000,
		Protocol: model.VMess,
		Settings: `{"users":[{"uuid":"11111111-1111-1111-1111-111111111111"}]}`,
		Sniffing: `{"sniff":true}`,
	}
}

func TestValidateInboundAcceptsWellFormed(t *testing.T) {
	if err := ValidateInbound(validInbound()); err != nil {
		t.Fatalf("a well-formed inbound was rejected: %v", err)
	}
}

func TestValidateInboundRejectsNil(t *testing.T) {
	if err := ValidateInbound(nil); err == nil {
		t.Fatal("nil inbound must be rejected")
	}
}

func TestValidateInboundRejectsUnknownProtocol(t *testing.T) {
	ib := validInbound()
	ib.Protocol = model.Protocol("wireguard-lite")
	err := ValidateInbound(ib)
	if err == nil {
		t.Fatal("unknown protocol must be rejected")
	}
	assertFieldError(t, err, "protocol")
}

func TestValidateInboundRejectsBadPort(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 999999} {
		ib := validInbound()
		ib.Port = port
		err := ValidateInbound(ib)
		if err == nil {
			t.Errorf("port %d must be rejected", port)
			continue
		}
		assertFieldError(t, err, "port")
	}
}

// TestValidateInboundRejectsNonObjectSettings 覆盖 M-4 的"必须是对象"要求。
func TestValidateInboundRejectsNonObjectSettings(t *testing.T) {
	cases := map[string]string{
		"array":   `[1,2,3]`,
		"string":  `"hello"`,
		"number":  `42`,
		"boolean": `true`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			ib := validInbound()
			ib.Settings = raw
			err := ValidateInbound(ib)
			if err == nil {
				t.Fatalf("settings %s must be rejected", raw)
			}
			assertFieldError(t, err, "settings")
			if !strings.Contains(err.Error(), "must be a JSON object") {
				t.Errorf("error = %v, want it to explain the object requirement", err)
			}
		})
	}
}

func TestValidateInboundRejectsInvalidJSON(t *testing.T) {
	ib := validInbound()
	ib.Settings = `{"users":[`
	err := ValidateInbound(ib)
	if err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
	assertFieldError(t, err, "settings")
}

// TestValidateInboundRejectsReservedKeys 是 M-4 的核心：
// settings 里的 listen_port 会在生成的配置里覆盖真正的监听端口。
func TestValidateInboundRejectsReservedKeys(t *testing.T) {
	for _, key := range []string{"type", "tag", "listen", "listen_port"} {
		t.Run(key, func(t *testing.T) {
			ib := validInbound()
			ib.Settings = `{"users":[],"` + key + `":"evil"}`
			err := ValidateInbound(ib)
			if err == nil {
				t.Fatalf("settings carrying reserved key %q must be rejected", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q should name the offending key %q", err, key)
			}
		})
	}
}

func TestValidateInboundRejectsReservedKeysInSniffing(t *testing.T) {
	ib := validInbound()
	ib.Sniffing = `{"sniff":true,"listen_port":1}`
	err := ValidateInbound(ib)
	if err == nil {
		t.Fatal("sniffing carrying a reserved key must be rejected")
	}
	assertFieldError(t, err, "sniffing")
}

func TestValidateInboundAllowsEmptySettings(t *testing.T) {
	for _, raw := range []string{"", "   ", "null", "{}"} {
		ib := validInbound()
		ib.Protocol = model.Direct
		ib.Settings = raw
		ib.Sniffing = ""
		if err := ValidateInbound(ib); err != nil {
			t.Errorf("settings %q should be accepted, got %v", raw, err)
		}
	}
}

// TestValidateInboundRealityRequiresBothKeys 覆盖 Reality 分享链接修复。
func TestValidateInboundRealityRequiresBothKeys(t *testing.T) {
	pair, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("missing public key", func(t *testing.T) {
		ib := validInbound()
		ib.Protocol = model.VLESS
		ib.Settings = `{"users":[{"uuid":"u"}],"tls":{"enabled":true,"reality":{"enabled":true,"private_key":"` + pair.PrivateKey + `"}}}`
		err := ValidateInbound(ib)
		if err == nil {
			t.Fatal("reality without public_key must be rejected; the share link would carry an empty pbk")
		}
		if !strings.Contains(err.Error(), "public_key") {
			t.Errorf("error = %v, want it to name public_key", err)
		}
	})

	t.Run("missing private key", func(t *testing.T) {
		ib := validInbound()
		ib.Protocol = model.VLESS
		ib.Settings = `{"users":[{"uuid":"u"}],"tls":{"enabled":true,"reality":{"enabled":true,"public_key":"` + pair.PublicKey + `"}}}`
		if err := ValidateInbound(ib); err == nil {
			t.Fatal("reality without private_key must be rejected")
		}
	})

	t.Run("mismatched pair", func(t *testing.T) {
		other, err := GenerateRealityKeyPair()
		if err != nil {
			t.Fatal(err)
		}
		ib := validInbound()
		ib.Protocol = model.VLESS
		ib.Settings = `{"users":[{"uuid":"u"}],"tls":{"enabled":true,"reality":{"enabled":true,"private_key":"` +
			pair.PrivateKey + `","public_key":"` + other.PublicKey + `"}}}`
		if err := ValidateInbound(ib); err == nil {
			t.Fatal("a public_key that does not derive from private_key must be rejected")
		}
	})

	t.Run("matching pair", func(t *testing.T) {
		ib := validInbound()
		ib.Protocol = model.VLESS
		ib.Settings = `{"users":[{"uuid":"u"}],"tls":{"enabled":true,"reality":{"enabled":true,"private_key":"` +
			pair.PrivateKey + `","public_key":"` + pair.PublicKey + `"}}}`
		if err := ValidateInbound(ib); err != nil {
			t.Fatalf("a matching key pair must be accepted: %v", err)
		}
	})

	t.Run("reality disabled needs no keys", func(t *testing.T) {
		ib := validInbound()
		ib.Protocol = model.VLESS
		ib.Settings = `{"users":[{"uuid":"u"}],"tls":{"enabled":true,"reality":{"enabled":false}}}`
		if err := ValidateInbound(ib); err != nil {
			t.Fatalf("a disabled reality block must not require keys: %v", err)
		}
	})
}

func assertFieldError(t *testing.T, err error, wantField string) {
	t.Helper()
	fe, ok := err.(*FieldError)
	if !ok {
		t.Fatalf("error %v is a %T, want *FieldError so the API can report the offending field", err, err)
	}
	if fe.Field != wantField {
		t.Errorf("FieldError.Field = %q, want %q", fe.Field, wantField)
	}
}
