package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"x-ui/database"
	"x-ui/database/model"
	"x-ui/testutil"
)

// firstUser 返回首启时自动建出来的管理员。
func firstUser(t *testing.T) *model.User {
	t.Helper()
	user, err := (&UserService{}).GetFirstUser()
	if err != nil {
		t.Fatalf("read first user: %v", err)
	}
	return user
}

// enroll 走一遍完整的注册流程，返回明文密钥与找回码。
func enroll(t *testing.T, s *TwoFactorService, user *model.User) (secret string, recoveryCodes []string) {
	t.Helper()

	enrollment, err := s.BeginEnrollment(user.Id, user.Username)
	if err != nil {
		t.Fatalf("begin enrollment: %v", err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	codes, err := s.ConfirmEnrollment(user.Id, code)
	if err != nil {
		t.Fatalf("confirm enrollment: %v", err)
	}
	return enrollment.Secret, codes
}

// clearReplayCursor 把重放游标清零，等价于"时间往前走了一个步长"。
//
// 确认注册会消费掉当前时间步，之后再用同一个码就会被重放保护挡下。
// 真等 30 秒会让整个测试套件慢一个数量级，而按 now+30s 生成码又会在
// 恰好跨越步长边界时落到容差窗口之外，变成一个偶发失败。
func clearReplayCursor(t *testing.T, userID int) {
	t.Helper()
	if err := database.GetDB().Model(model.TwoFactor{}).
		Where("user_id = ?", userID).Update("last_used_step", 0).Error; err != nil {
		t.Fatalf("clear the replay cursor: %v", err)
	}
}

// currentCode 生成此刻有效的 TOTP 码。
func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	return code
}

func TestTwoFactorEnrollmentLifecycle(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}

	if enabled, err := s.IsEnabled(user.Id); err != nil || enabled {
		t.Fatalf("IsEnabled = %v, %v on a fresh install; want false, nil", enabled, err)
	}

	enrollment, err := s.BeginEnrollment(user.Id, user.Username)
	if err != nil {
		t.Fatalf("begin enrollment: %v", err)
	}
	if enrollment.Secret == "" {
		t.Fatal("the enrollment carries no secret")
	}
	if !strings.HasPrefix(enrollment.OtpauthURL, "otpauth://totp/") {
		t.Errorf("otpauth URL = %q, want an otpauth:// link", enrollment.OtpauthURL)
	}
	if !strings.HasPrefix(enrollment.QRCode, "data:image/png;base64,") {
		t.Errorf("QR code = %.40q, want a PNG data URL", enrollment.QRCode)
	}

	// 扫了码还没确认时 2FA 绝不能生效：手机时间不准的管理员会被
	// 永久关在面板外面。
	if enabled, err := s.IsEnabled(user.Id); err != nil || enabled {
		t.Fatalf("two-factor became active before the code was confirmed (enabled=%v, err=%v)", enabled, err)
	}
	status, err := s.GetStatus(user.Id)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status.Enabled || !status.Pending {
		t.Errorf("status = %+v, want pending and not enabled", status)
	}

	if _, err := s.ConfirmEnrollment(user.Id, "000000"); !errors.Is(err, ErrTwoFactorInvalidCode) {
		t.Errorf("confirming with a wrong code: err = %v, want ErrTwoFactorInvalidCode", err)
	}
	if enabled, _ := s.IsEnabled(user.Id); enabled {
		t.Fatal("a failed confirmation enabled two-factor anyway")
	}

	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	codes, err := s.ConfirmEnrollment(user.Id, code)
	if err != nil {
		t.Fatalf("confirm enrollment: %v", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Errorf("got %d recovery codes, want %d", len(codes), recoveryCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Errorf("recovery code %q was handed out twice", c)
		}
		seen[c] = true
	}

	if enabled, err := s.IsEnabled(user.Id); err != nil || !enabled {
		t.Fatalf("IsEnabled = %v, %v after confirmation; want true, nil", enabled, err)
	}
	status, err = s.GetStatus(user.Id)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if !status.Enabled || status.Pending || status.RecoveryCodesLeft != recoveryCodeCount {
		t.Errorf("status = %+v, want enabled with %d recovery codes", status, recoveryCodeCount)
	}
}

// 已启用的账号不能被"重新注册"覆盖：任何拿到活动会话的人都能借此
// 把 2FA 换绑到自己的设备上。
func TestBeginEnrollmentRefusesToRebindAnActiveSecret(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}
	enroll(t, s, user)

	if _, err := s.BeginEnrollment(user.Id, user.Username); !errors.Is(err, ErrTwoFactorAlreadyEnabled) {
		t.Errorf("err = %v, want ErrTwoFactorAlreadyEnabled", err)
	}
}

func TestVerifyAcceptsTOTPAndRejectsGarbage(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}
	secret, _ := enroll(t, s, user)
	clearReplayCursor(t, user.Id)

	usedRecovery, err := s.Verify(user.Id, currentCode(t, secret))
	if err != nil {
		t.Fatalf("verify a valid code: %v", err)
	}
	if usedRecovery {
		t.Error("a TOTP code was reported as a recovery code")
	}

	for _, bad := range []string{"000000", "abcdef", "12345", "1234567"} {
		if _, err := s.Verify(user.Id, bad); !errors.Is(err, ErrTwoFactorInvalidCode) {
			t.Errorf("Verify(%q) = %v, want ErrTwoFactorInvalidCode", bad, err)
		}
	}
	if _, err := s.Verify(user.Id, ""); !errors.Is(err, ErrTwoFactorCodeRequired) {
		t.Errorf("an empty code should be reported as missing, not invalid")
	}
}

// TOTP 码在整个 30 秒窗口里都有效。不记住用过的时间步，一个被肩窥
// 或从代理日志里捞到的码就能在窗口内被重放一次。
func TestVerifyRefusesToReplayTheSameCode(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}
	secret, _ := enroll(t, s, user)
	clearReplayCursor(t, user.Id)

	code := currentCode(t, secret)
	if _, err := s.Verify(user.Id, code); err != nil {
		t.Fatalf("first use of the code failed: %v", err)
	}
	if _, err := s.Verify(user.Id, code); !errors.Is(err, ErrTwoFactorInvalidCode) {
		t.Errorf("the same code was accepted twice (err = %v)", err)
	}
}

func TestRecoveryCodesAreSingleUse(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}
	_, codes := enroll(t, s, user)

	usedRecovery, err := s.Verify(user.Id, codes[0])
	if err != nil {
		t.Fatalf("verify with a recovery code: %v", err)
	}
	if !usedRecovery {
		t.Error("using a recovery code was not reported as such")
	}
	if _, err := s.Verify(user.Id, codes[0]); !errors.Is(err, ErrTwoFactorInvalidCode) {
		t.Errorf("a recovery code was accepted a second time (err = %v)", err)
	}
	// 用掉一张之后，其余的还得能用。
	if _, err := s.Verify(user.Id, codes[1]); err != nil {
		t.Errorf("the remaining recovery codes stopped working: %v", err)
	}

	status, err := s.GetStatus(user.Id)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status.RecoveryCodesLeft != recoveryCodeCount-2 {
		t.Errorf("recovery codes left = %d, want %d", status.RecoveryCodesLeft, recoveryCodeCount-2)
	}
}

// 找回码在纸上通常带着连字符，用户会连着一起粘贴进来。
func TestRecoveryCodesToleratePunctuation(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}
	_, codes := enroll(t, s, user)

	if !strings.Contains(codes[0], "-") {
		t.Fatalf("recovery code %q has no separator; this test guards the normalisation", codes[0])
	}
	stripped := strings.ReplaceAll(codes[0], "-", "")
	if _, err := s.Verify(user.Id, " "+strings.ToLower(stripped)+" "); err != nil {
		t.Errorf("a recovery code typed without the hyphen was rejected: %v", err)
	}
}

func TestDisableRequiresPasswordAndCode(t *testing.T) {
	_, banner := testutil.InitDB(t)
	username, password := testutil.ParseInitialCredentials(t, banner)
	user := firstUser(t)
	s := &TwoFactorService{}
	secret, codes := enroll(t, s, user)
	clearReplayCursor(t, user.Id)

	if err := s.Disable(user.Id, username, "not-the-password", currentCode(t, secret)); err == nil {
		t.Error("two-factor was disabled with a wrong password")
	}
	if err := s.Disable(user.Id, username, password, "000000"); !errors.Is(err, ErrTwoFactorInvalidCode) {
		t.Errorf("disabling with a bad code: err = %v, want ErrTwoFactorInvalidCode", err)
	}
	if enabled, _ := s.IsEnabled(user.Id); !enabled {
		t.Fatal("a failed disable attempt turned two-factor off anyway")
	}

	if err := s.Disable(user.Id, username, password, currentCode(t, secret)); err != nil {
		t.Fatalf("disable with the right credentials: %v", err)
	}
	if enabled, _ := s.IsEnabled(user.Id); enabled {
		t.Error("two-factor is still enabled after a successful disable")
	}

	// 找回码必须跟着一起作废，否则关了又开之后，老纸条还能用。
	var leftovers int64
	if err := database.GetDB().Model(model.RecoveryCode{}).
		Where("user_id = ?", user.Id).Count(&leftovers).Error; err != nil {
		t.Fatalf("count recovery codes: %v", err)
	}
	if leftovers != 0 {
		t.Errorf("%d recovery codes survived the disable; %q would still let someone in",
			leftovers, codes[0])
	}
}

// 密钥以密文落库。这挡不住"整库被拖走"（派生材料就在同一个库里），
// 但它保证密钥不会以明文出现在备份片段、误贴的 SQL 输出、
// 或任何按字符串搜库的场景里。
func TestTheStoredSecretIsNotPlaintext(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}
	secret, _ := enroll(t, s, user)

	record := &model.TwoFactor{}
	if err := database.GetDB().Model(model.TwoFactor{}).
		Where("user_id = ?", user.Id).First(record).Error; err != nil {
		t.Fatalf("read the stored record: %v", err)
	}
	if record.Secret == secret {
		t.Fatal("the TOTP secret is stored in plaintext")
	}
	if strings.Contains(record.Secret, secret) {
		t.Fatal("the stored ciphertext contains the plaintext secret")
	}

	decrypted, err := s.decryptSecret(record.Secret)
	if err != nil {
		t.Fatalf("decrypt the stored secret: %v", err)
	}
	if decrypted != secret {
		t.Errorf("the decrypted secret does not round-trip")
	}
}

// 密文被改过一个字节就必须整体拒绝，而不是把乱码喂给 TOTP 校验。
func TestATamperedSecretIsRejected(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}
	enroll(t, s, user)

	record := &model.TwoFactor{}
	if err := database.GetDB().Model(model.TwoFactor{}).
		Where("user_id = ?", user.Id).First(record).Error; err != nil {
		t.Fatalf("read the stored record: %v", err)
	}
	tampered := "A" + record.Secret[1:]
	if err := database.GetDB().Model(model.TwoFactor{}).
		Where("id = ?", record.Id).Update("secret", tampered).Error; err != nil {
		t.Fatalf("tamper with the secret: %v", err)
	}

	if _, err := s.Verify(user.Id, "123456"); err == nil {
		t.Fatal("a tampered secret still verified codes")
	}
}

func TestVerifyOnAnUnenrolledAccount(t *testing.T) {
	testutil.InitDB(t)
	user := firstUser(t)
	s := &TwoFactorService{}

	if _, err := s.Verify(user.Id, "123456"); !errors.Is(err, ErrTwoFactorNotEnrolled) {
		t.Errorf("err = %v, want ErrTwoFactorNotEnrolled", err)
	}
	if _, err := s.ConfirmEnrollment(user.Id, "123456"); !errors.Is(err, ErrTwoFactorNotEnrolled) {
		t.Errorf("confirming without enrolling: err = %v, want ErrTwoFactorNotEnrolled", err)
	}
}

func TestNormalizeCode(t *testing.T) {
	cases := map[string]string{
		"123456":       "123456",
		" 123 456 ":    "123456",
		"abcde-fghij":  "ABCDEFGHIJ",
		"\tABCDE FGHI": "ABCDEFGHI",
		"":             "",
	}
	for raw, want := range cases {
		if got := normalizeCode(raw); got != want {
			t.Errorf("normalizeCode(%q) = %q, want %q", raw, got, want)
		}
	}
}
