package model

// TwoFactor 保存一个管理员账号的 TOTP 配置。
//
// 每个用户至多一行（UserId 上的唯一索引）。行存在但 Enabled=false 表示
// "已经扫过码、还没输对第一个验证码"的中间态：此时登录不受影响，
// 直到用户证明自己的验证器确实能算出正确的码为止。这个中间态是必须的——
// 先启用再验证会把一个手机时间不准的管理员永久关在面板外面。
type TwoFactor struct {
	Id     int `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId int `json:"userId" gorm:"uniqueIndex"`

	// Secret 是 AES-GCM 密文（base64），明文为 base32 的 TOTP 共享密钥。
	// 任何情况下都不得出现在 JSON 响应、日志或审计里，故打上 json:"-"。
	Secret string `json:"-"`

	Enabled bool `json:"enabled"`

	// EnrolledAt / ConfirmedAt 为毫秒时间戳，仅用于展示与排障。
	EnrolledAt  int64 `json:"enrolledAt"`
	ConfirmedAt int64 `json:"confirmedAt"`

	// LastUsedStep 是最近一次被接受的 TOTP 时间步（Unix 秒 / 30）。
	// TOTP 码在整个 30 秒窗口内都有效，不记住它的话，一个被旁观者看到
	// （或从代理日志里捞到）的验证码可以在同一窗口里被重放一次。
	LastUsedStep int64 `json:"-"`
}

// RecoveryCode 是一次性的两步验证找回码。
//
// 只存哈希：找回码等价于口令，明文落库意味着任何能读到数据库的人
// 都能绕过 2FA。UsedAt 非零表示已被消费，不再接受第二次。
type RecoveryCode struct {
	Id     int `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId int `json:"userId" gorm:"index"`

	// CodeHash 是找回码的 SHA-256 十六进制值。
	//
	// 这里刻意不用 bcrypt：找回码是 CSPRNG 生成的高熵串（不是人选的口令），
	// 慢哈希挡不住任何现实攻击，反而让一次登录尝试要串行验算十个 bcrypt，
	// 白送一个放大倍数十倍的 CPU 消耗面。
	CodeHash string `json:"-" gorm:"uniqueIndex"`

	UsedAt int64 `json:"usedAt"`
}
