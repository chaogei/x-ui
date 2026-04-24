package service

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// bcrypt 哈希固定 60 字符，以 $2a$ / $2b$ / $2y$ 开头，用此快速与历史明文密码区分。
// 若未来升级 bcrypt cost，只需保证前缀仍属于上面三种即可兼容。
const bcryptHashLen = 60

// IsBcryptHash 判断字符串是否已是 bcrypt 哈希格式。
// 面板 v1.0.0 之前将密码明文存 DB，需要在登录路径上按需升级。
func IsBcryptHash(s string) bool {
	if len(s) != bcryptHashLen {
		return false
	}
	return strings.HasPrefix(s, "$2a$") ||
		strings.HasPrefix(s, "$2b$") ||
		strings.HasPrefix(s, "$2y$")
}

// HashPassword 使用 bcrypt 默认 cost 生成密码哈希。
// 空密码一律拒绝（fail-fast），避免误将空密码落库。
func HashPassword(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("password can not be empty")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword 兼容历史明文与 bcrypt 哈希两种存储方式校验密码。
// 返回值：
//
//	ok          : 密码是否正确
//	needUpgrade : 是否需要把明文密码升级为 bcrypt 哈希（仅在校验通过且原存储为明文时为 true）
//
// 调用方负责在 needUpgrade=true 时执行一次密码字段 UPDATE，实现无感平滑迁移。
func VerifyPassword(stored, provided string) (ok bool, needUpgrade bool) {
	if IsBcryptHash(stored) {
		err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(provided))
		return err == nil, false
	}
	match := stored == provided
	return match, match
}
