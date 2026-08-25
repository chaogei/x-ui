package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newContext 构造一个带指定 RemoteAddr 与请求头的 gin.Context，
// 并按 trustProxies 配置 gin 引擎的受信代理策略。
func newContext(t *testing.T, remoteAddr string, headers map[string]string, trustProxies []string) *gin.Context {
	t.Helper()

	engine := gin.New()
	if len(trustProxies) == 0 {
		engine.ForwardedByClientIP = false
		if err := engine.SetTrustedProxies(nil); err != nil {
			t.Fatal(err)
		}
	} else {
		engine.ForwardedByClientIP = true
		if err := engine.SetTrustedProxies(trustProxies); err != nil {
			t.Fatal(err)
		}
	}

	var captured *gin.Context
	engine.GET("/", func(c *gin.Context) { captured = c })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	engine.ServeHTTP(httptest.NewRecorder(), req)
	if captured == nil {
		t.Fatal("handler never ran")
	}
	return captured
}

// TestClientIPIgnoresForwardedHeadersByDefault 是 H-1 的核心回归：
// 默认不信任任何代理时，伪造的 XFF / X-Real-IP 绝不能改变判定结果。
func TestClientIPIgnoresForwardedHeadersByDefault(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"no headers", nil},
		{"forged XFF", map[string]string{"X-Forwarded-For": "1.2.3.4"}},
		{"forged XFF chain", map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8"}},
		{"forged X-Real-IP", map[string]string{"X-Real-IP": "9.9.9.9"}},
		{"both forged", map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "9.9.9.9"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newContext(t, "198.51.100.20:44321", tc.headers, nil)
			if got := ClientIP(c); got != "198.51.100.20" {
				t.Errorf("ClientIP = %q, want the TCP peer 198.51.100.20 (headers must be ignored)", got)
			}
		})
	}
}

// TestClientIPHonoursForwardedHeadersForTrustedProxies 验证运维显式配置后 XFF 生效。
func TestClientIPHonoursForwardedHeadersForTrustedProxies(t *testing.T) {
	c := newContext(t,
		"10.1.2.3:5555",
		map[string]string{"X-Forwarded-For": "203.0.113.9"},
		[]string{"10.0.0.0/8"},
	)
	if got := ClientIP(c); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want 203.0.113.9 from the trusted proxy's XFF", got)
	}
}

// TestClientIPRejectsXFFFromUntrustedPeer 验证白名单外的对端即使发 XFF 也不被采信。
func TestClientIPRejectsXFFFromUntrustedPeer(t *testing.T) {
	c := newContext(t,
		"203.0.113.200:5555",
		map[string]string{"X-Forwarded-For": "1.1.1.1"},
		[]string{"10.0.0.0/8"},
	)
	if got := ClientIP(c); got != "203.0.113.200" {
		t.Errorf("ClientIP = %q, want the peer 203.0.113.200; it is not in the trusted CIDR", got)
	}
}

func TestClientIPHandlesNilContext(t *testing.T) {
	if got := ClientIP(nil); got != "" {
		t.Errorf("ClientIP(nil) = %q, want empty", got)
	}
}

func TestRemoteAddrIPFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-a-host-port"
	if got := remoteAddrIP(req); got != "not-a-host-port" {
		t.Errorf("remoteAddrIP = %q, want the raw value when it has no port", got)
	}
	if got := remoteAddrIP(nil); got != "" {
		t.Errorf("remoteAddrIP(nil) = %q, want empty", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	cases := map[string][]string{
		"":                         {},
		"   ":                      {},
		"10.0.0.0/8":               {"10.0.0.0/8"},
		"10.0.0.0/8,192.168.0.0/16": {"10.0.0.0/8", "192.168.0.0/16"},
		" 10.0.0.0/8 , 172.16.0.0/12 ": {"10.0.0.0/8", "172.16.0.0/12"},
		"10.0.0.0/8\n192.168.0.0/16":   {"10.0.0.0/8", "192.168.0.0/16"},
		",,10.0.0.0/8,,":               {"10.0.0.0/8"},
	}
	for input, want := range cases {
		got := ParseTrustedProxies(input)
		if len(got) != len(want) {
			t.Errorf("ParseTrustedProxies(%q) = %v, want %v", input, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ParseTrustedProxies(%q)[%d] = %q, want %q", input, i, got[i], want[i])
			}
		}
	}
}
