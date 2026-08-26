package service

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/hkdf"
	"gorm.io/gorm"

	"x-ui/database"
	"x-ui/database/model"
	"x-ui/util/random"
)

// 面板管理员的两步验证（TOTP，RFC 6238）。
//
// 威胁模型（说清楚它挡什么、不挡什么）：
//
//   - 挡住"口令泄露即失守"。口令会被复用、被钓鱼、被写进运维文档；
//     TOTP 让攻击者还必须实时拿到管理员手里的那台设备。
//   - 不挡住"数据库整体失窃"。密钥虽以 AES-GCM 存储，但派生密钥的材料
//     （session secret）与密文在同一个库里；能整库拖走的人也拿得到密钥。
//     加密在这里的真实收益是：密钥不会以明文出现在备份片段、误贴的
//     SQL 输出、以及任何按字符串搜库的场景里。真正的边界仍是数据库
//     文件的 0600 权限。

const (
	// totpPeriod 是标准的 30 秒时间步。改这个值会让所有已注册的验证器失效。
	totpPeriod = 30
	// totpSkew 允许前后各一个时间步，用来吸收手机与服务器之间的时钟偏差。
	totpSkew = 1
	// totpDigits 固定 6 位：几乎所有验证器 App 只认这个。
	totpDigits = otp.DigitsSix

	// recoveryCodeCount 是每次启用时生成的找回码数量。
	recoveryCodeCount = 10
	// recoveryCodeLength 是单个找回码的字符数（分两段展示）。
	recoveryCodeLength = 10
	// recoveryCodeAlphabet 去掉了 0/O、1/I/L 这些抄错率最高的字符。
	// 32 个字符 × 10 位 = 50 bit，对一次性使用的码绰绰有余。
	recoveryCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

	// twoFactorIssuer 是验证器 App 里显示的账号分组名。
	twoFactorIssuer = "x-ui"
)

var (
	// ErrTwoFactorNotEnrolled 表示该用户还没有开始注册流程。
	ErrTwoFactorNotEnrolled = errors.New("two-factor authentication is not set up")
	// ErrTwoFactorAlreadyEnabled 表示重复启用。
	ErrTwoFactorAlreadyEnabled = errors.New("two-factor authentication is already enabled")
	// ErrTwoFactorInvalidCode 是所有"码不对"的统一出口：
	// TOTP 错、找回码错、码被重放过——对外都是同一句话。
	ErrTwoFactorInvalidCode = errors.New("invalid verification code")
	// ErrTwoFactorCodeRequired 表示账号开了 2FA 但请求里没带验证码。
	ErrTwoFactorCodeRequired = errors.New("a verification code is required")
)

// TwoFactorService 管理管理员账号的 TOTP 注册、校验与关闭。
type TwoFactorService struct {
	settingService SettingService
}

// Enrollment 是一次注册流程的产物，只在注册那一刻返回给浏览器。
type Enrollment struct {
	// Secret 是 base32 的共享密钥，供无法扫码的用户手动录入。
	Secret string `json:"secret"`
	// OtpauthURL 是 otpauth:// 链接（二维码的原始内容）。
	OtpauthURL string `json:"otpauthUrl"`
	// QRCode 是 data:image/png;base64,... 形式的二维码，前端直接塞进 <img src>。
	QRCode string `json:"qrcode"`
}

// TwoFactorStatus 是设置页展示的两步验证状态。
type TwoFactorStatus struct {
	Enabled bool `json:"enabled"`
	// Pending 表示已扫码但尚未用验证码确认。
	Pending bool `json:"pending"`
	// RecoveryCodesLeft 是尚未被使用的找回码数量。
	RecoveryCodesLeft int64 `json:"recoveryCodesLeft"`
}

// IsEnabled 报告某个用户是否已启用两步验证。
//
// 读库失败时返回 error，调用方（登录路径）必须按"启用"处理：
// 因为一次数据库抖动就把二次验证跳过去，等于给攻击者一个可触发的降级开关。
func (s *TwoFactorService) IsEnabled(userID int) (bool, error) {
	record, err := s.get(userID)
	if errors.Is(err, ErrTwoFactorNotEnrolled) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return record.Enabled, nil
}

// GetStatus 返回设置页需要的状态。
func (s *TwoFactorService) GetStatus(userID int) (*TwoFactorStatus, error) {
	status := &TwoFactorStatus{}
	record, err := s.get(userID)
	if errors.Is(err, ErrTwoFactorNotEnrolled) {
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	status.Enabled = record.Enabled
	status.Pending = !record.Enabled

	db := database.GetDB()
	if err := db.Model(model.RecoveryCode{}).
		Where("user_id = ? and used_at = 0", userID).
		Count(&status.RecoveryCodesLeft).Error; err != nil {
		return nil, err
	}
	return status, nil
}

// BeginEnrollment 生成一个新的 TOTP 密钥并存为未启用状态。
//
// 重复调用会覆盖尚未确认的密钥（用户可能换了手机、或关掉了对话框），
// 但对已经启用的账号直接拒绝：否则任何拿到活动会话的人都能把 2FA
// 悄悄换绑到自己的设备上。
func (s *TwoFactorService) BeginEnrollment(userID int, username string) (*Enrollment, error) {
	existing, err := s.get(userID)
	if err != nil && !errors.Is(err, ErrTwoFactorNotEnrolled) {
		return nil, err
	}
	if err == nil && existing.Enabled {
		return nil, ErrTwoFactorAlreadyEnabled
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      twoFactorIssuer,
		AccountName: username,
		Period:      totpPeriod,
		Digits:      totpDigits,
		Algorithm:   otp.AlgorithmSHA1,
		Rand:        rand.Reader,
	})
	if err != nil {
		return nil, err
	}

	encrypted, err := s.encryptSecret(key.Secret())
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	record := &model.TwoFactor{
		UserId:     userID,
		Secret:     encrypted,
		Enabled:    false,
		EnrolledAt: now,
		// 换密钥时把重放游标清零：它是按旧密钥的时间步记的，留着没有意义。
		LastUsedStep: 0,
	}
	db := database.GetDB()
	if existing != nil {
		record.Id = existing.Id
		if err := db.Save(record).Error; err != nil {
			return nil, err
		}
	} else if err := db.Create(record).Error; err != nil {
		return nil, err
	}

	qr, err := renderQRCode(key)
	if err != nil {
		return nil, err
	}
	return &Enrollment{
		Secret:     key.Secret(),
		OtpauthURL: key.URL(),
		QRCode:     qr,
	}, nil
}

// ConfirmEnrollment 用一个真实的验证码确认注册，成功后启用 2FA 并返回找回码。
//
// 找回码只在此刻返回一次，之后只剩哈希：面板自己也无法再次显示它们。
func (s *TwoFactorService) ConfirmEnrollment(userID int, code string) ([]string, error) {
	record, err := s.get(userID)
	if err != nil {
		return nil, err
	}
	if record.Enabled {
		return nil, ErrTwoFactorAlreadyEnabled
	}
	secret, err := s.decryptSecret(record.Secret)
	if err != nil {
		return nil, err
	}
	step, ok := validateTOTP(secret, code, time.Now())
	if !ok {
		return nil, ErrTwoFactorInvalidCode
	}

	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	db := database.GetDB()
	err = db.Transaction(func(tx *gorm.DB) error {
		// 重新生成时先清掉旧码，避免"关掉 2FA 又重开"之后老纸条还能用。
		if err := tx.Where("user_id = ?", userID).Delete(model.RecoveryCode{}).Error; err != nil {
			return err
		}
		for _, hash := range hashes {
			if err := tx.Create(&model.RecoveryCode{UserId: userID, CodeHash: hash}).Error; err != nil {
				return err
			}
		}
		return tx.Model(model.TwoFactor{}).Where("id = ?", record.Id).
			Updates(map[string]interface{}{
				"enabled":        true,
				"confirmed_at":   now,
				"last_used_step": step,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// Verify 校验一个登录验证码：先试 TOTP，再试找回码。
//
// 成功返回是否用掉了一张找回码，供调用方提示用户"你的备用码少了一张"。
func (s *TwoFactorService) Verify(userID int, code string) (usedRecoveryCode bool, err error) {
	code = normalizeCode(code)
	if code == "" {
		return false, ErrTwoFactorCodeRequired
	}
	record, err := s.get(userID)
	if err != nil {
		return false, err
	}
	if !record.Enabled {
		return false, ErrTwoFactorNotEnrolled
	}

	secret, err := s.decryptSecret(record.Secret)
	if err != nil {
		return false, err
	}
	if step, ok := validateTOTP(secret, code, time.Now()); ok {
		// 同一个时间步里的码只认一次。TOTP 在整整 30 秒内都有效，
		// 不记游标的话，肩窥或中间人拿到的码可以在窗口内被重放。
		//
		// 游标的推进必须由数据库来裁决，理由和找回码那边一模一样：
		// 两个请求带着同一个码同时进来，都会读到同一个旧游标，
		// 都判定"比它大"，然后都放行。把比较写进 WHERE，只有一条
		// UPDATE 能命中，另一条 RowsAffected 为 0 —— 那就是重放。
		if step <= record.LastUsedStep {
			return false, ErrTwoFactorInvalidCode
		}
		consumed, err := s.consumeTOTPStep(record.Id, step)
		if err != nil {
			return false, err
		}
		if !consumed {
			return false, ErrTwoFactorInvalidCode
		}
		return false, nil
	}

	ok, err := s.consumeRecoveryCode(userID, code)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, ErrTwoFactorInvalidCode
	}
	return true, nil
}

// Disable 关闭两步验证。必须同时提供口令与一个有效验证码。
//
// 只要口令的话，一个被劫持的会话（或一台没锁屏的电脑）就能把 2FA 摘掉，
// 那这层防护等于不存在。
func (s *TwoFactorService) Disable(userID int, username, password, code string) error {
	if (&UserService{}).CheckUser(username, password) == nil {
		return errors.New("the password is incorrect")
	}
	record, err := s.get(userID)
	if err != nil {
		return err
	}
	if !record.Enabled {
		return ErrTwoFactorNotEnrolled
	}
	if _, err := s.Verify(userID, code); err != nil {
		return err
	}

	db := database.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(model.RecoveryCode{}).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(model.TwoFactor{}).Error
	})
}

// consumeTOTPStep 把重放游标推进到 step，返回这次推进是否由本调用完成。
//
// 条件写在 WHERE 里：last_used_step < step 的行只存在一次，抢到它的那条
// UPDATE 就是唯一被承认的登录。
func (s *TwoFactorService) consumeTOTPStep(recordID int, step int64) (bool, error) {
	result := database.GetDB().Model(model.TwoFactor{}).
		Where("id = ? and last_used_step < ?", recordID, step).
		Update("last_used_step", step)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// consumeRecoveryCode 消费一张找回码，返回它是否有效。
//
// 用带条件的 UPDATE 而不是"先查后写"：两个并发请求拿着同一张码时，
// 数据库层面只有一条能把 used_at 从 0 改成非 0，另一条 RowsAffected 为 0。
func (s *TwoFactorService) consumeRecoveryCode(userID int, code string) (bool, error) {
	hash := hashRecoveryCode(code)
	result := database.GetDB().Model(model.RecoveryCode{}).
		Where("user_id = ? and code_hash = ? and used_at = 0", userID, hash).
		Update("used_at", time.Now().UnixMilli())
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (s *TwoFactorService) get(userID int) (*model.TwoFactor, error) {
	record := &model.TwoFactor{}
	err := database.GetDB().Model(model.TwoFactor{}).
		Where("user_id = ?", userID).First(record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTwoFactorNotEnrolled
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

// validateTOTP 校验验证码并返回命中的时间步。
//
// 自己遍历时间步而不是直接用 totp.Validate，是因为需要知道"命中的是哪一步"
// 才能实现重放保护；totp.Validate 只回一个 bool。
func validateTOTP(secret, code string, now time.Time) (step int64, ok bool) {
	code = normalizeCode(code)
	if code == "" {
		return 0, false
	}
	opts := totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      0,
		Digits:    totpDigits,
		Algorithm: otp.AlgorithmSHA1,
	}
	for offset := -totpSkew; offset <= totpSkew; offset++ {
		at := now.Add(time.Duration(offset) * totpPeriod * time.Second)
		expected, err := totp.GenerateCodeCustom(secret, at, opts)
		if err != nil {
			return 0, false
		}
		// 常数时间比较：验证码只有 6 位，逐字节短路的比较会漏出可测量的时序差异。
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return at.Unix() / totpPeriod, true
		}
	}
	return 0, false
}

// normalizeCode 去掉用户从纸条或短信里带过来的空格与连字符。
func normalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch r {
		case ' ', '-', '\t':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// generateRecoveryCodes 生成一批找回码，返回明文与对应哈希。
func generateRecoveryCodes() (codes []string, hashes []string, err error) {
	codes = make([]string, 0, recoveryCodeCount)
	hashes = make([]string, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		code, err := random.SeqFrom(recoveryCodeLength, recoveryCodeAlphabet)
		if err != nil {
			return nil, nil, err
		}
		// 分成两段展示，抄写时不容易串行。
		display := code[:recoveryCodeLength/2] + "-" + code[recoveryCodeLength/2:]
		codes = append(codes, display)
		hashes = append(hashes, hashRecoveryCode(code))
	}
	return codes, hashes, nil
}

// hashRecoveryCode 归一化后取 SHA-256，使带不带连字符都能验证通过。
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeCode(code)))
	return hex.EncodeToString(sum[:])
}

// renderQRCode 把 otpauth 链接渲染成 data URL 形式的 PNG。
//
// 在服务端渲染，前端就不必再引一个二维码库；密钥也不会为了画图
// 而多经过一层 JS 代码。
func renderQRCode(key *otp.Key) (string, error) {
	img, err := key.Image(256, 256)
	if err != nil {
		return "", err
	}
	buf := &bytes.Buffer{}
	if err := png.Encode(buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// twoFactorKeyInfo 是 HKDF 的 info 参数，把 2FA 加密密钥与 session
// 签名密钥彻底分开——同一份材料派生出的两把钥匙不该互相等价。
const twoFactorKeyInfo = "x-ui two-factor secret encryption v1"

// secretKeyMaterial 派生 AES-256 密钥。
func (s *TwoFactorService) secretKeyMaterial() ([]byte, error) {
	seed, err := s.settingService.GetSecret()
	if err != nil {
		return nil, err
	}
	if len(seed) == 0 {
		return nil, errors.New("the panel secret is empty; refusing to encrypt with a null key")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, seed, nil, []byte(twoFactorKeyInfo)), key); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *TwoFactorService) encryptSecret(plain string) (string, error) {
	key, err := s.secretKeyMaterial()
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *TwoFactorService) decryptSecret(stored string) (string, error) {
	key, err := s.secretKeyMaterial()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return "", fmt.Errorf("the stored TOTP secret is not valid base64: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("the stored TOTP secret is truncated")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// GCM 校验失败意味着密文被改过，或 session secret 变了。
		// 无论哪种，继续拿它去验码都毫无意义。
		return "", fmt.Errorf("the stored TOTP secret cannot be decrypted: %w", err)
	}
	// 顺手确认它确实还是个 base32 密钥，免得把一段乱码喂给 TOTP 库。
	if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(string(plain)); err != nil {
		return "", errors.New("the decrypted TOTP secret is not base32")
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
