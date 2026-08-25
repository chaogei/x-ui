package service

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// RealityKeyPair 是一对 X25519 密钥，编码方式与 sing-box / Xray 的
// reality 配置一致（base64 URL-safe，无 padding）。
type RealityKeyPair struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

// ErrRealityKeyMismatch 表示提交的 public_key 不是 private_key 派生出来的。
var ErrRealityKeyMismatch = errors.New("reality public_key does not match private_key")

// GenerateRealityKeyPair 生成一对全新的 Reality 密钥。
//
// 面板提供此接口是为了从根上消除"用户手工填的 public_key 与 private_key 不匹配"
// 这类只能在客户端连不上时才暴露的故障：两个字段总是一次性成对产生。
func GenerateRealityKeyPair() (*RealityKeyPair, error) {
	priv := make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(priv); err != nil {
		return nil, err
	}
	// RFC 7748 规定的标量夹紧（clamping）。
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	enc := base64.RawURLEncoding
	return &RealityKeyPair{
		PrivateKey: enc.EncodeToString(priv),
		PublicKey:  enc.EncodeToString(pub),
	}, nil
}

// decodeRealityKey 兼容 base64 的四种常见变体解出 32 字节密钥。
func decodeRealityKey(s string) ([]byte, bool) {
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding, base64.URLEncoding,
		base64.RawStdEncoding, base64.StdEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == curve25519.ScalarSize {
			return b, true
		}
	}
	return nil, false
}

// DeriveRealityPublicKey 由 private_key 推导 public_key。
func DeriveRealityPublicKey(privateKey string) (string, error) {
	priv, ok := decodeRealityKey(privateKey)
	if !ok {
		return "", fmt.Errorf("reality private_key is not a base64-encoded 32-byte key")
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(pub), nil
}

// ValidateRealityKeys 校验一对 Reality 密钥。
//
// 规则：
//  1. private_key 必填——sing-box 没有它根本起不来。
//  2. public_key 必填——它不进 sing-box 配置，但分享链接的 pbk 参数要用；
//     缺失会生成 `pbk=` 为空的链接，客户端一律握手失败（这正是修复前的现象）。
//  3. 两者都能解码时，必须满足 public == derive(private)，否则直接拒绝，
//     不让"看起来填了"的错配置流到用户手上。
func ValidateRealityKeys(privateKey, publicKey string) error {
	if privateKey == "" {
		return &FieldError{Field: "settings.tls.reality.private_key", Reason: "required when reality is enabled"}
	}
	if publicKey == "" {
		return &FieldError{Field: "settings.tls.reality.public_key", Reason: "required when reality is enabled (used by the share link)"}
	}
	derived, err := DeriveRealityPublicKey(privateKey)
	if err != nil {
		// 无法解码时不阻断：用户可能用了非标准编码的外部密钥。
		return nil
	}
	if _, ok := decodeRealityKey(publicKey); !ok {
		return nil
	}
	if derived != normalizeRealityKey(publicKey) {
		return &FieldError{
			Field:  "settings.tls.reality.public_key",
			Reason: ErrRealityKeyMismatch.Error(),
		}
	}
	return nil
}

// normalizeRealityKey 把任意 base64 变体重新编码为 RawURL 形式以便比较。
func normalizeRealityKey(s string) string {
	b, ok := decodeRealityKey(s)
	if !ok {
		return s
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
