package web

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"x-ui/database"
	"x-ui/database/model"
	"x-ui/web/service"
)

// 两步验证的端到端行为：走真实的 HTTP 登录表单，不绕过 CSRF、限流与 session。

// enrollTwoFactor 用已登录的会话完成注册，返回 TOTP 密钥与找回码。
func (p *panel) enrollTwoFactor() (secret string, recoveryCodes []string) {
	p.t.Helper()

	msg := p.decode(p.postForm("xui/2fa/enroll", nil))
	if !msg.Success {
		p.t.Fatalf("start two-factor enrollment: %s", msg.Msg)
	}
	var enrollment service.Enrollment
	if err := json.Unmarshal(msg.Obj, &enrollment); err != nil {
		p.t.Fatalf("decode enrollment: %v", err)
	}

	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		p.t.Fatalf("generate totp code: %v", err)
	}
	msg = p.decode(p.postForm("xui/2fa/confirm", url.Values{"code": {code}}))
	if !msg.Success {
		p.t.Fatalf("confirm two-factor enrollment: %s", msg.Msg)
	}
	var confirmed struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	if err := json.Unmarshal(msg.Obj, &confirmed); err != nil {
		p.t.Fatalf("decode recovery codes: %v", err)
	}
	if len(confirmed.RecoveryCodes) == 0 {
		p.t.Fatal("enrollment returned no recovery codes")
	}
	// 确认动作消费掉了当前时间步；清掉重放游标，后续用例才能立刻用
	// 当前时刻的验证码，而不必真的等 30 秒。
	p.clearReplayCursor()
	return enrollment.Secret, confirmed.RecoveryCodes
}

func (p *panel) clearReplayCursor() {
	p.t.Helper()
	if err := database.GetDB().Model(model.TwoFactor{}).
		Where("1 = 1").Update("last_used_step", 0).Error; err != nil {
		p.t.Fatalf("clear the replay cursor: %v", err)
	}
}

// logout 结束当前会话。
func (p *panel) logout() {
	p.t.Helper()
	readBody(p.t, p.get("logout"))
	if p.jarHasSession() {
		p.t.Fatal("the browser still holds a session cookie after logout")
	}
}

// loginWithCode 提交带验证码的登录表单。
func (p *panel) loginWithCode(username, password, code string) apiMsg {
	p.t.Helper()
	return p.decode(p.postForm("login", url.Values{
		"username":      {username},
		"password":      {password},
		"twoFactorCode": {code},
	}))
}

func TestE2ETwoFactorGatesTheLogin(t *testing.T) {
	p := newPanel(t)
	p.login()
	secret, _ := p.enrollTwoFactor()
	p.logout()

	t.Run("password alone is no longer enough", func(t *testing.T) {
		msg := p.loginAs(p.username, p.password, false)
		if !strings.Contains(strings.ToLower(msg.Msg), "code") &&
			!strings.Contains(msg.Msg, "验证码") {
			t.Errorf("message = %q, want it to ask for a verification code", msg.Msg)
		}
		if p.isLoggedIn() {
			t.Fatal("the panel handed out a session without the second factor")
		}
	})

	t.Run("a wrong code is rejected", func(t *testing.T) {
		if msg := p.loginWithCode(p.username, p.password, "000000"); msg.Success {
			t.Fatal("an invalid verification code was accepted")
		}
		if p.isLoggedIn() {
			t.Fatal("a session was created despite the wrong code")
		}
	})

	t.Run("the right code gets in", func(t *testing.T) {
		p.clearReplayCursor()
		code, err := totp.GenerateCode(secret, time.Now())
		if err != nil {
			t.Fatalf("generate totp code: %v", err)
		}
		if msg := p.loginWithCode(p.username, p.password, code); !msg.Success {
			t.Fatalf("a valid verification code was rejected: %s", msg.Msg)
		}
		if !p.isLoggedIn() {
			t.Fatal("the session was not established after a successful two-factor login")
		}
	})
}

func TestE2ERecoveryCodeLogsInExactlyOnce(t *testing.T) {
	p := newPanel(t)
	p.login()
	_, codes := p.enrollTwoFactor()
	p.logout()

	if msg := p.loginWithCode(p.username, p.password, codes[0]); !msg.Success {
		t.Fatalf("a recovery code was rejected: %s", msg.Msg)
	}
	if !p.isLoggedIn() {
		t.Fatal("no session after logging in with a recovery code")
	}
	p.logout()

	if msg := p.loginWithCode(p.username, p.password, codes[0]); msg.Success {
		t.Fatal("the same recovery code logged in a second time")
	}
	// 其余的码不受影响。
	if msg := p.loginWithCode(p.username, p.password, codes[1]); !msg.Success {
		t.Fatalf("the remaining recovery codes stopped working: %s", msg.Msg)
	}
}

// 验证码猜测同样受登录限流约束。否则攻击者拿到口令后可以慢慢撞
// 六位数字，一百万次就能穿过去——比口令本身还弱。
func TestE2ETwoFactorFailuresCountTowardsTheLockout(t *testing.T) {
	p := newPanel(t)
	p.login()
	secret, _ := p.enrollTwoFactor()
	p.logout()

	limit := p.server.loginLimiter.MaxFailures
	for i := 0; i < limit; i++ {
		if msg := p.loginWithCode(p.username, p.password, "000000"); msg.Success {
			t.Fatalf("attempt %d with a bogus code succeeded", i)
		}
	}

	p.clearReplayCursor()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	msg := p.loginWithCode(p.username, p.password, code)
	if msg.Success {
		t.Fatal("a locked-out IP got in with a valid code; brute-forcing the 6 digits is unbounded")
	}
	if !strings.Contains(strings.ToLower(msg.Msg), "lock") && !strings.Contains(msg.Msg, "锁") {
		t.Errorf("message = %q, want it to mention the lockout", msg.Msg)
	}
}

func TestE2ETwoFactorManagementRequiresAuth(t *testing.T) {
	p := newPanel(t)

	// 未登录时所有 2FA 接口都必须拒绝。
	for _, path := range []string{"xui/2fa/status", "xui/2fa/enroll", "xui/2fa/confirm", "xui/2fa/disable"} {
		if msg := p.decode(p.postForm(path, nil)); msg.Success {
			t.Errorf("%s served an unauthenticated request", path)
		}
	}

	p.login()

	// 登录之后，缺少 CSRF token 的请求同样必须被挡下：
	// 一个能被跨站触发的 /2fa/disable 等于没有 2FA。
	resp := p.postFormWithToken("xui/2fa/enroll", nil, "")
	if msg := p.decode(resp); msg.Success {
		t.Error("the enrollment endpoint accepted a request with no CSRF token")
	}
}

func TestE2ETwoFactorDisableNeedsPasswordAndCode(t *testing.T) {
	p := newPanel(t)
	p.login()
	secret, _ := p.enrollTwoFactor()

	if msg := p.decode(p.postForm("xui/2fa/disable", url.Values{
		"password": {"not-the-password"},
		"code":     {"000000"},
	})); msg.Success {
		t.Fatal("two-factor was disabled with the wrong password")
	}

	if msg := p.decode(p.postForm("xui/2fa/disable", url.Values{
		"password": {p.password},
		"code":     {"000000"},
	})); msg.Success {
		t.Fatal("two-factor was disabled with a wrong verification code")
	}

	p.clearReplayCursor()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	if msg := p.decode(p.postForm("xui/2fa/disable", url.Values{
		"password": {p.password},
		"code":     {code},
	})); !msg.Success {
		t.Fatalf("disabling with the right credentials failed: %s", msg.Msg)
	}

	// 关掉之后，登录又只需要口令。
	p.logout()
	p.loginAs(p.username, p.password, true)
}

// 状态接口不能把密钥或找回码带出去。
func TestE2ETwoFactorStatusLeaksNothing(t *testing.T) {
	p := newPanel(t)
	p.login()
	secret, codes := p.enrollTwoFactor()

	resp := p.postForm("xui/2fa/status", nil)
	body := string(readBody(t, resp))
	if strings.Contains(body, secret) {
		t.Fatal("the status endpoint returns the TOTP secret")
	}
	for _, code := range codes {
		if strings.Contains(body, strings.ReplaceAll(code, "-", "")) || strings.Contains(body, code) {
			t.Fatal("the status endpoint returns a recovery code")
		}
	}
	if !strings.Contains(body, `"recoveryCodesLeft"`) {
		t.Errorf("the status response does not report how many recovery codes are left: %s", body)
	}
}
