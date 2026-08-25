package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func securityEngine(isHTTPS bool) *gin.Engine {
	engine := gin.New()
	engine.Use(SecurityHeaders(isHTTPS))
	engine.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return engine
}

func serve(engine *gin.Engine) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

func TestSecurityHeadersAlwaysPresent(t *testing.T) {
	rec := serve(securityEngine(false))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy must be set")
	}
}

// TestContentSecurityPolicyBlocksTheImportantThings 只断言真正影响安全的指令。
// script-src 里的 'unsafe-inline' 是 Vue2 模板的现实约束，不在断言范围内。
func TestContentSecurityPolicyBlocksTheImportantThings(t *testing.T) {
	csp := serve(securityEngine(false)).Header().Get("Content-Security-Policy")

	required := []string{
		"default-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"connect-src 'self'",
	}
	for _, directive := range required {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP %q is missing %q", csp, directive)
		}
	}
	// 面板不该从第三方 CDN 拉脚本；'*' 会让整条策略形同虚设。
	if strings.Contains(csp, "script-src *") || strings.Contains(csp, "default-src *") {
		t.Errorf("CSP %q contains a wildcard source", csp)
	}
}

// TestHSTSOnlyOverHTTPS 是一条防止把用户锁死在门外的护栏：
// 纯 HTTP 部署下发 HSTS，浏览器会永久拒绝访问这个面板。
func TestHSTSOnlyOverHTTPS(t *testing.T) {
	if got := serve(securityEngine(false)).Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS = %q over plain HTTP; that would lock operators out of their panel", got)
	}

	got := serve(securityEngine(true)).Header().Get("Strict-Transport-Security")
	if !strings.Contains(got, "max-age=") {
		t.Errorf("HSTS = %q over HTTPS, want a max-age directive", got)
	}
	if strings.Contains(got, "max-age=0") {
		t.Errorf("HSTS = %q disables itself", got)
	}
}

func TestSecurityHeadersDoNotBreakTheResponse(t *testing.T) {
	rec := serve(securityEngine(true))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want the handler output", rec.Body.String())
	}
}
