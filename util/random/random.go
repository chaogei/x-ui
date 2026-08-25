// Package random 提供面板内所有随机串的生成入口。
//
// 安全约定：本包只使用 crypto/rand。历史实现基于 math/rand + UnixNano 种子，
// 生成的 session secret 与初始口令可被攻击者按启动时间穷举，属于 Critical 缺陷。
package random

import (
	"crypto/rand"
	"encoding/base64"
	"math/big"
)

// alphabet 是可打印且无歧义转义问题的字符集（数字 + 大小写字母）。
const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// Seq 生成长度为 n 的随机字符串，字符取自 alphabet。
//
// 采用 crypto/rand.Int 做无偏采样（拒绝取模偏置）。
// n <= 0 返回空串；熵源不可用时返回错误，调用方必须处理而不是静默降级。
func Seq(n int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	max := big.NewInt(int64(len(alphabet)))
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[idx.Int64()]
	}
	return string(buf), nil
}

// MustSeq 与 Seq 相同，但熵源失败时 panic。
// 仅用于「没有熵就不该继续运行」的启动路径。
func MustSeq(n int) string {
	s, err := Seq(n)
	if err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return s
}

// Bytes 返回 n 字节的密码学安全随机数据。
func Bytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// SecretString 生成用于 cookie store key 等场景的随机密钥，
// 以 base64(RawURL) 编码，便于存进 settings 表的 TEXT 列。
func SecretString(nBytes int) (string, error) {
	b, err := Bytes(nBytes)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
