package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// csrfEngine 搭一个最小的、与生产同构的中间件栈：session → CSRF → handler。
func csrfEngine() *gin.Engine {
	engine := gin.New()
	store := cookie.NewStore([]byte("test-session-secret-not-a-real-one"))
	engine.Use(sessions.Sessions("x-ui-test", store))
	engine.Use(CSRF())

	handler := func(c *gin.Context) {
		c.String(http.StatusOK, SessionCSRFToken(c))
	}
	engine.GET("/", handler)
	engine.HEAD("/", handler)
	engine.OPTIONS("/", handler)
	engine.POST("/login", handler)
	engine.POST("/xui/inbound/add", handler)
	engine.PUT("/xui/inbound/update", handler)
	engine.PATCH("/xui/inbound/patch", handler)
	engine.DELETE("/xui/inbound/del", handler)
	return engine
}

// primeCSRF 走一次 GET 拿到 token 与 session cookie，模拟浏览器加载页面。
func primeCSRF(t *testing.T, engine *gin.Engine) (token string, cookies []*http.Cookie) {
	t.Helper()

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("priming GET returned %d, want 200", rec.Code)
	}
	token = rec.Body.String()
	if token == "" {
		t.Fatal("the middleware must mint a CSRF token on a safe request")
	}
	return token, rec.Result().Cookies()
}

func withCookies(req *http.Request, cookies []*http.Cookie) *http.Request {
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return req
}

func TestCSRFAllowsSafeMethods(t *testing.T) {
	engine := csrfEngine()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, httptest.NewRequest(method, "/", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("%s returned %d, want 200", method, rec.Code)
			}
		})
	}
}

// TestCSRFRejectsUnsafeMethodsWithoutToken 是这个中间件存在的理由：
// 攻击者站点上的 <form method=post> 会带上受害者的 session cookie，
// 但拿不到 token，因此必须被拦下。
func TestCSRFRejectsUnsafeMethodsWithoutToken(t *testing.T) {
	engine := csrfEngine()
	_, cookies := primeCSRF(t, engine)

	unsafe := map[string]string{
		http.MethodPost:   "/xui/inbound/add",
		http.MethodPut:    "/xui/inbound/update",
		http.MethodPatch:  "/xui/inbound/patch",
		http.MethodDelete: "/xui/inbound/del",
	}
	for method, path := range unsafe {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, withCookies(httptest.NewRequest(method, path, nil), cookies))
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s returned %d, want 403", method, path, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "csrf") {
				t.Errorf("body = %q, want it to name the CSRF failure", rec.Body.String())
			}
		})
	}
}

func TestCSRFAcceptsMatchingToken(t *testing.T) {
	engine := csrfEngine()
	token, cookies := primeCSRF(t, engine)

	req := withCookies(httptest.NewRequest(http.MethodPost, "/xui/inbound/add", nil), cookies)
	req.Header.Set(HeaderCSRFToken, token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST with a valid token returned %d, want 200", rec.Code)
	}
}

func TestCSRFRejectsWrongToken(t *testing.T) {
	engine := csrfEngine()
	token, cookies := primeCSRF(t, engine)

	cases := map[string]string{
		"empty":     "",
		"garbage":   "not-the-token",
		"truncated": token[:len(token)-1],
		"extended":  token + "0",
		"uppercase": strings.ToUpper(token),
	}
	for name, supplied := range cases {
		t.Run(name, func(t *testing.T) {
			req := withCookies(httptest.NewRequest(http.MethodPost, "/xui/inbound/add", nil), cookies)
			req.Header.Set(HeaderCSRFToken, supplied)
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("POST with the %s token returned %d, want 403", name, rec.Code)
			}
		})
	}
}

// TestCSRFProtectsLogin 覆盖 CSRF-login：攻击者把受害者登进自己的账号，
// 之后受害者的所有操作都发生在攻击者可见的账号里。
func TestCSRFProtectsLogin(t *testing.T) {
	engine := csrfEngine()
	_, cookies := primeCSRF(t, engine)

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodPost, "/login", nil), cookies))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /login without a token returned %d, want 403", rec.Code)
	}
}

// TestCSRFTokenOfAnotherSessionIsRejected 覆盖 double-submit 的关键性质：
// token 必须与 session 绑定，光知道某个合法 token 是不够的。
func TestCSRFTokenOfAnotherSessionIsRejected(t *testing.T) {
	engine := csrfEngine()
	attackerToken, _ := primeCSRF(t, engine)
	victimToken, victimCookies := primeCSRF(t, engine)

	if attackerToken == victimToken {
		t.Fatal("two independent sessions were issued the same CSRF token")
	}

	req := withCookies(httptest.NewRequest(http.MethodPost, "/xui/inbound/add", nil), victimCookies)
	req.Header.Set(HeaderCSRFToken, attackerToken)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("a token from a different session returned %d, want 403", rec.Code)
	}
}

func TestCSRFTokenIsStableWithinASession(t *testing.T) {
	engine := csrfEngine()
	token, cookies := primeCSRF(t, engine)

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodGet, "/", nil), cookies))
		if got := rec.Body.String(); got != token {
			t.Fatalf("request %d saw token %q, want the stable %q", i, got, token)
		}
	}
}

func TestSessionCSRFTokenWithoutMiddleware(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := SessionCSRFToken(c); got != "" {
		t.Errorf("SessionCSRFToken = %q, want empty when the middleware never ran", got)
	}

	c.Set(SessionKeyCSRFToken, 42)
	if got := SessionCSRFToken(c); got != "" {
		t.Errorf("SessionCSRFToken = %q, want empty for a non-string value", got)
	}
}

func TestCSRFTokenIsHighEntropy(t *testing.T) {
	engine := csrfEngine()
	seen := make(map[string]bool, 32)
	for i := 0; i < 32; i++ {
		token, _ := primeCSRF(t, engine)
		if len(token) != 64 {
			t.Fatalf("token %q is %d chars, want 64 hex chars (32 bytes)", token, len(token))
		}
		if seen[token] {
			t.Fatalf("token %q was issued twice", token)
		}
		seen[token] = true
	}
}

func TestCSRFConcurrentRequests(t *testing.T) {
	engine := csrfEngine()
	token, cookies := primeCSRF(t, engine)

	var wg sync.WaitGroup
	wg.Add(16)
	for i := 0; i < 16; i++ {
		go func() {
			defer wg.Done()
			req := withCookies(httptest.NewRequest(http.MethodPost, "/xui/inbound/add", nil), cookies)
			req.Header.Set(HeaderCSRFToken, token)
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("concurrent POST returned %d, want 200", rec.Code)
			}
		}()
	}
	wg.Wait()
}
