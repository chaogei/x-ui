package web

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"x-ui/database"
	"x-ui/database/model"
	"x-ui/web/service"
)

// TestE2EFirstBoot 走一遍全新部署：库自动建好，管理员存在，
// 口令不是 admin，而且只能从公告里拿到。
func TestE2EFirstBoot(t *testing.T) {
	p := newPanel(t)

	if p.username != "admin" {
		t.Errorf("initial username = %q, want admin", p.username)
	}
	if p.password == "admin" || p.password == "" {
		t.Fatalf("initial password = %q; a fresh panel must never serve admin/admin", p.password)
	}
	if len(p.password) < 16 {
		t.Errorf("initial password is %d chars, want at least 16", len(p.password))
	}

	user := &model.User{}
	if err := database.GetDB().Model(model.User{}).First(user).Error; err != nil {
		t.Fatalf("read seeded user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte("admin")); err == nil {
		t.Fatal("admin/admin still authenticates")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(p.password)); err != nil {
		t.Errorf("the announced password does not match the stored hash: %v", err)
	}

	// 首启标记必须点亮，面板才会在入站页顶部提示改密。
	if !(&service.UserService{}).UsingInitialCredentials() {
		t.Error("the initial-credentials warning flag must be on right after first boot")
	}
}

func TestE2ELoginPageIsServed(t *testing.T) {
	p := newPanel(t)

	resp := p.get("")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / returned %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", ct)
	}
	if token := p.csrfToken(); token == "" {
		t.Error("the login page must embed a CSRF token for the form to work")
	}
}

// TestE2ELoginWithoutCSRFIsRejected 是攻击者从外站提交登录表单的场景。
func TestE2ELoginWithoutCSRFIsRejected(t *testing.T) {
	p := newPanel(t)
	p.csrfToken() // 建立 session，让请求只缺 token 这一项

	resp := p.postFormWithToken("login", url.Values{
		"username": {p.username},
		"password": {p.password},
	}, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /login without a CSRF token returned %d, want 403", resp.StatusCode)
	}
	if p.isLoggedIn() {
		t.Error("a CSRF-less login must not establish a session")
	}
}

func TestE2ELoginWithWrongCSRFIsRejected(t *testing.T) {
	p := newPanel(t)
	token := p.csrfToken()

	resp := p.postFormWithToken("login", url.Values{
		"username": {p.username},
		"password": {p.password},
	}, token[:len(token)-2]+"ff")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /login with a tampered CSRF token returned %d, want 403", resp.StatusCode)
	}
}

// TestE2ELoginSuccessSetsHardenedCookie 覆盖会话 cookie 的属性。
func TestE2ELoginSuccessSetsHardenedCookie(t *testing.T) {
	p := newPanel(t)
	p.login()

	c := p.sessionCookie()
	if c == nil {
		t.Fatal("a successful login must set a session cookie")
	}
	if !c.HttpOnly {
		t.Error("the session cookie must be HttpOnly so XSS cannot read it")
	}
	if c.Value == "" {
		t.Error("the session cookie is empty")
	}
	if strings.Contains(c.Value, p.password) {
		t.Error("the session cookie leaks the password")
	}
	if !p.isLoggedIn() {
		t.Error("the session does not actually authenticate follow-up requests")
	}
}

func TestE2ELoginRejectsBadCredentials(t *testing.T) {
	p := newPanel(t)

	cases := map[string]struct{ user, pass string }{
		"wrong password": {p.username, "definitely-not-the-password"},
		"wrong username": {"root", p.password},
		"empty password": {p.username, ""},
		"empty username": {"", p.password},
		"both empty":     {"", ""},
		"sql-ish":        {"admin' OR '1'='1", "x"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p.loginAs(tc.user, tc.pass, false)
			if p.isLoggedIn() {
				t.Fatal("a rejected login must not establish a session")
			}
		})
	}
}

// TestE2ELoginLockout 覆盖爆破防护：连续失败到阈值后，
// 即便随后提交正确口令也会被拒。
func TestE2ELoginLockout(t *testing.T) {
	p := newPanel(t)
	limit := p.server.loginLimiter.MaxFailures

	for i := 0; i < limit; i++ {
		p.loginAs(p.username, "wrong-password", false)
	}

	msg := p.loginAs(p.username, p.password, false)
	if !strings.Contains(strings.ToLower(msg.Msg), "lock") && !strings.Contains(msg.Msg, "锁") {
		t.Errorf("lockout message = %q, want it to mention the lock", msg.Msg)
	}
	if p.isLoggedIn() {
		t.Fatal("a locked-out IP must not be able to log in with the correct password")
	}
}

// TestE2EForgedXFFDoesNotBypassLockout 是 H-1 最有价值的一条端到端断言。
//
// 默认没有可信代理，所以攻击者往 X-Forwarded-For 里塞随机 IP 也换不到新的
// 限流分桶——每次尝试仍然记在同一个 TCP 对端地址上。
func TestE2EForgedXFFDoesNotBypassLockout(t *testing.T) {
	p := newPanel(t)
	limit := p.server.loginLimiter.MaxFailures

	forged := []string{
		"1.2.3.4", "5.6.7.8", "9.10.11.12", "13.14.15.16",
		"17.18.19.20", "21.22.23.24", "25.26.27.28",
	}
	for i := 0; i < limit; i++ {
		resp := p.postForm("login", url.Values{
			"username": {p.username},
			"password": {"wrong-password"},
		}, [2]string{"X-Forwarded-For", forged[i%len(forged)]})
		if msg := p.decode(resp); msg.Success {
			t.Fatalf("attempt %d unexpectedly succeeded", i)
		}
	}

	// 换一个从未用过的伪造 IP，外加 X-Real-IP —— 都不该重置计数。
	resp := p.postForm("login", url.Values{
		"username": {p.username},
		"password": {p.password},
	},
		[2]string{"X-Forwarded-For", "203.0.113.99"},
		[2]string{"X-Real-IP", "203.0.113.98"},
	)
	if msg := p.decode(resp); msg.Success {
		t.Fatal("a forged X-Forwarded-For reset the login rate limiter")
	}
	if p.isLoggedIn() {
		t.Fatal("the forged header let the client through the lockout")
	}
}

// TestE2ESuccessfulLoginResetsTheCounter 确认限流不会把正常用户越锁越死。
func TestE2ESuccessfulLoginResetsTheCounter(t *testing.T) {
	p := newPanel(t)
	limit := p.server.loginLimiter.MaxFailures

	for i := 0; i < limit-1; i++ {
		p.loginAs(p.username, "wrong-password", false)
	}
	p.login()

	// 计数已清零：又能重新用掉一整轮失败额度。
	for i := 0; i < limit-1; i++ {
		p.loginAs(p.username, "wrong-password", false)
	}
	p.login()
}

// TestE2EConcurrentLogins 在 -race 下跑，用来暴露 session / 限流 / i18n 的竞态。
func TestE2EConcurrentLogins(t *testing.T) {
	p := newPanel(t)

	var wg sync.WaitGroup
	wg.Add(8)
	for i := 0; i < 8; i++ {
		go func() {
			defer wg.Done()
			// 每个 goroutine 用独立的 client，模拟不同浏览器。
			client := p.newClient()
			token, cookies := client.primeCSRF()
			resp := client.postForm("login", url.Values{
				"username": {p.username},
				"password": {p.password},
			}, token, cookies)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("concurrent login returned %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
}

// TestE2ELogoutClearsTheSession 覆盖 L-1：登出下发的删除 cookie 的 Path
// 必须与登录时一致，否则浏览器里的 session cookie 原封不动。
func TestE2ELogoutClearsTheSession(t *testing.T) {
	for _, basePath := range []string{"", "/panel/"} {
		name := "root"
		var opts []panelOption
		if basePath != "" {
			name = "custom base path"
			opts = append(opts, withBasePath(basePath))
		}
		t.Run(name, func(t *testing.T) {
			p := newPanel(t, opts...)
			p.login()
			if !p.isLoggedIn() {
				t.Fatal("login did not take effect")
			}

			loginCookiePath := p.sessionCookie().Path

			resp := p.get("logout")
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusTemporaryRedirect {
				t.Errorf("GET /logout returned %d, want a redirect to the login page", resp.StatusCode)
			}

			var cleared *http.Cookie
			for _, c := range resp.Cookies() {
				if c.Name == "session" {
					cleared = c
				}
			}
			if cleared == nil {
				t.Fatal("logout must send a Set-Cookie that deletes the session")
			}
			if cleared.MaxAge >= 0 {
				t.Errorf("the deletion cookie has MaxAge=%d, want a negative value", cleared.MaxAge)
			}
			if cleared.Path != loginCookiePath {
				t.Errorf("logout clears Path=%q but login set Path=%q; the browser would keep the real cookie",
					cleared.Path, loginCookiePath)
			}
			if p.jarHasSession() {
				t.Error("the browser still holds a session cookie after logout")
			}
			if p.isLoggedIn() {
				t.Error("the session still authenticates after logout")
			}
		})
	}
}

// TestE2EProtectedRoutesRequireLogin 未登录时的两种表现：
// AJAX 请求拿到 JSON 失败提示，浏览器导航被重定向回登录页。
func TestE2EProtectedRoutesRequireLogin(t *testing.T) {
	p := newPanel(t)

	t.Run("ajax", func(t *testing.T) {
		msg := p.decode(p.postForm("xui/inbound/list", nil))
		if msg.Success {
			t.Fatal("an unauthenticated client listed inbounds")
		}
	})

	t.Run("browser navigation", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, p.url("xui/inbounds"), nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		resp := p.do(req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTemporaryRedirect {
			t.Fatalf("GET /xui/inbounds while logged out returned %d, want a redirect", resp.StatusCode)
		}
		if got := resp.Header.Get("Location"); got != p.basePath {
			t.Errorf("Location = %q, want the login page at %q", got, p.basePath)
		}
	})
}

// TestE2EAuthenticatedWritesStillNeedCSRF 登录之后 CSRF 依然生效。
func TestE2EAuthenticatedWritesStillNeedCSRF(t *testing.T) {
	p := newPanel(t)
	p.login()

	resp := p.postFormWithToken("xui/inbound/add", url.Values{
		"port":     {"20000"},
		"protocol": {"vmess"},
	}, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an authenticated POST without a CSRF token returned %d, want 403", resp.StatusCode)
	}
	if p.countInbounds() != 0 {
		t.Error("the CSRF-less request still created an inbound")
	}
}

func TestE2ECSRFTokenIsBoundToTheSession(t *testing.T) {
	p := newPanel(t)
	p.login()

	// 另一个"浏览器"的 token 不能拿来用。
	other := p.newClient()
	otherToken, _ := other.primeCSRF()

	resp := p.postFormWithToken("xui/inbound/list", nil, otherToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a token minted for another session returned %d, want 403", resp.StatusCode)
	}
}
