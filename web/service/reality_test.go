package service

import (
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/curve25519"
)

// TestGenerateRealityKeyPairIsSelfConsistent 是 Reality 分享链接修复的核心保证：
// 面板生成的 public_key 必须真的是 private_key 派生出来的，
// 否则客户端拿着 pbk 去握手只会失败，而且错误信息毫无指向性。
func TestGenerateRealityKeyPairIsSelfConsistent(t *testing.T) {
	pair, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate reality key pair: %v", err)
	}

	derived, err := DeriveRealityPublicKey(pair.PrivateKey)
	if err != nil {
		t.Fatalf("derive public key from generated private key: %v", err)
	}
	if derived != pair.PublicKey {
		t.Errorf("public key = %q, want %q derived from the private key", pair.PublicKey, derived)
	}
}

func TestGenerateRealityKeyPairEncoding(t *testing.T) {
	pair, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate reality key pair: %v", err)
	}
	for name, key := range map[string]string{"private": pair.PrivateKey, "public": pair.PublicKey} {
		raw, err := base64.RawURLEncoding.DecodeString(key)
		if err != nil {
			t.Errorf("%s key %q is not raw-url base64: %v", name, key, err)
			continue
		}
		if len(raw) != curve25519.ScalarSize {
			t.Errorf("%s key decodes to %d bytes, want %d", name, len(raw), curve25519.ScalarSize)
		}
		if strings.ContainsAny(key, "+/=") {
			t.Errorf("%s key %q must be URL-safe and unpadded", name, key)
		}
	}
}

func TestGenerateRealityKeyPairIsRandom(t *testing.T) {
	seen := make(map[string]bool, 32)
	for i := 0; i < 32; i++ {
		pair, err := GenerateRealityKeyPair()
		if err != nil {
			t.Fatalf("generate reality key pair: %v", err)
		}
		if seen[pair.PrivateKey] {
			t.Fatalf("private key %q generated twice", pair.PrivateKey)
		}
		seen[pair.PrivateKey] = true
	}
}

// TestGenerateRealityKeyPairClampsScalar 检查 RFC 7748 的标量夹紧。
// 没有夹紧的私钥在部分实现上会被拒绝或落入小子群。
func TestGenerateRealityKeyPairClampsScalar(t *testing.T) {
	for i := 0; i < 16; i++ {
		pair, err := GenerateRealityKeyPair()
		if err != nil {
			t.Fatalf("generate reality key pair: %v", err)
		}
		priv, err := base64.RawURLEncoding.DecodeString(pair.PrivateKey)
		if err != nil {
			t.Fatalf("decode private key: %v", err)
		}
		if priv[0]&7 != 0 {
			t.Errorf("private key byte 0 = %#x, low three bits must be cleared", priv[0])
		}
		if priv[31]&128 != 0 {
			t.Errorf("private key byte 31 = %#x, top bit must be cleared", priv[31])
		}
		if priv[31]&64 == 0 {
			t.Errorf("private key byte 31 = %#x, bit 6 must be set", priv[31])
		}
	}
}

// TestDeriveRealityPublicKeyAcceptsBase64Variants 覆盖用户从 Xray 迁移过来的场景：
// `xray x25519` 输出的是标准 base64，而 sing-box 文档用 URL-safe，两者都得认。
func TestDeriveRealityPublicKeyAcceptsBase64Variants(t *testing.T) {
	pair, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate reality key pair: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(pair.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}

	variants := map[string]string{
		"raw-url": base64.RawURLEncoding.EncodeToString(raw),
		"url":     base64.URLEncoding.EncodeToString(raw),
		"raw-std": base64.RawStdEncoding.EncodeToString(raw),
		"std":     base64.StdEncoding.EncodeToString(raw),
	}
	for name, encoded := range variants {
		t.Run(name, func(t *testing.T) {
			got, err := DeriveRealityPublicKey(encoded)
			if err != nil {
				t.Fatalf("derive from %s encoding: %v", name, err)
			}
			if got != pair.PublicKey {
				t.Errorf("derived %q, want %q", got, pair.PublicKey)
			}
		})
	}
}

func TestDeriveRealityPublicKeyRejectsGarbage(t *testing.T) {
	cases := map[string]string{
		"empty":      "",
		"not base64": "!!!!not-base64!!!!",
		"too short":  base64.RawURLEncoding.EncodeToString([]byte("short")),
		"too long":   base64.RawURLEncoding.EncodeToString(make([]byte, 64)),
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DeriveRealityPublicKey(key); err == nil {
				t.Fatalf("key %q must be rejected", key)
			}
		})
	}
}

func TestValidateRealityKeysRequiresBoth(t *testing.T) {
	pair, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate reality key pair: %v", err)
	}

	cases := []struct {
		name       string
		private    string
		public     string
		wantField  string
		wantAccept bool
	}{
		{name: "both present", private: pair.PrivateKey, public: pair.PublicKey, wantAccept: true},
		{name: "missing private", private: "", public: pair.PublicKey, wantField: "settings.tls.reality.private_key"},
		{name: "missing public", private: pair.PrivateKey, public: "", wantField: "settings.tls.reality.public_key"},
		{name: "both missing", private: "", public: "", wantField: "settings.tls.reality.private_key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRealityKeys(tc.private, tc.public)
			if tc.wantAccept {
				if err != nil {
					t.Fatalf("valid pair rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected a validation error")
			}
			assertFieldError(t, err, tc.wantField)
		})
	}
}

// TestValidateRealityKeysRejectsMismatch 是最有价值的一条：
// 修复前用户可以把两把不相干的密钥填进去，面板照单全收，
// 直到客户端连不上才发现，而日志里什么都没有。
func TestValidateRealityKeysRejectsMismatch(t *testing.T) {
	a, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate reality key pair: %v", err)
	}
	b, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate reality key pair: %v", err)
	}

	err = ValidateRealityKeys(a.PrivateKey, b.PublicKey)
	if err == nil {
		t.Fatal("mismatched key pair must be rejected")
	}
	assertFieldError(t, err, "settings.tls.reality.public_key")
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error = %q, want it to explain the mismatch", err)
	}
}

// TestValidateRealityKeysToleratesForeignEncoding 说明放行策略：
// 无法解码的密钥可能来自我们不认识的外部工具，此时只保证"非空"，
// 不擅自判定用户填错——拒绝的代价（面板不可用）高于放行。
func TestValidateRealityKeysToleratesForeignEncoding(t *testing.T) {
	if err := ValidateRealityKeys("some-vendor-specific-private-key", "some-vendor-specific-public-key"); err != nil {
		t.Errorf("undecodable but non-empty keys should pass through, got %v", err)
	}
}

func TestValidateRealityKeysAcceptsCrossEncodingPair(t *testing.T) {
	pair, err := GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate reality key pair: %v", err)
	}
	pub, err := base64.RawURLEncoding.DecodeString(pair.PublicKey)
	if err != nil {
		t.Fatalf("decode public key: %v", err)
	}
	// 用户可能从 Xray 复制标准 base64 的 public key，配 URL-safe 的 private key。
	if err := ValidateRealityKeys(pair.PrivateKey, base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Errorf("matching pair in mixed encodings must be accepted, got %v", err)
	}
}
