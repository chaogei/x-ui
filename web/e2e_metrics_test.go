package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// /metrics 是面板上唯一一个"内容有侦察价值但又需要被机器抓取"的端点，
// 所以它的鉴权行为值得单独走一遍真实 HTTP 栈。

// scrape 发一个不带 cookie 的 GET，可选 Authorization 头。
func (p *panel) scrape(path, bearer string) *http.Response {
	p.t.Helper()

	req, err := http.NewRequest(http.MethodGet, p.url(path), nil)
	if err != nil {
		p.t.Fatalf("build request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		p.t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func TestE2EMetricsRefusesAnonymousScrapes(t *testing.T) {
	p := newPanel(t)

	// 没配 token 时端点退回会话鉴权，匿名抓取必须被拒。
	resp := p.scrape("/metrics", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous scrape = %d, want 401 (body %q)", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "xui_up") {
		t.Fatal("the metrics body was served to an unauthenticated client")
	}
	if auth := resp.Header.Get("WWW-Authenticate"); !strings.Contains(auth, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, want it to advertise bearer auth", auth)
	}
}

func TestE2EMetricsAcceptsTheConfiguredToken(t *testing.T) {
	p := newPanel(t)
	writeSetting(t, "metricsToken", "s3cret-scrape-token")

	t.Run("wrong token", func(t *testing.T) {
		resp := p.scrape("/metrics", "not-the-token")
		readBody(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("right token", func(t *testing.T) {
		resp := p.scrape("/metrics", "s3cret-scrape-token")
		body := string(readBody(t, resp))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("Content-Type = %q, want the text exposition format", ct)
		}
		for _, want := range []string{
			"xui_up 1",
			"xui_core_running",
			"xui_core_restarts_total",
			"xui_login_fail_total",
			"xui_http_requests_total",
			"xui_http_request_duration_seconds",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("the exposition is missing %q", want)
			}
		}
	})

	// token 只从 Authorization 头读。查询串会被写进访问日志与反向代理日志，
	// 那等于把一个长期凭证撒得到处都是。
	t.Run("token in the query string is not accepted", func(t *testing.T) {
		resp := p.scrape("/metrics?token=s3cret-scrape-token", "")
		readBody(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})
}

// 没配 token 时，已登录的管理员在浏览器里点开 /metrics 应该能看到内容。
func TestE2EMetricsAllowsALoggedInAdmin(t *testing.T) {
	p := newPanel(t)
	p.login()

	resp := p.get("/metrics")
	body := string(readBody(t, resp))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a logged-in admin", resp.StatusCode)
	}
	if !strings.Contains(body, "xui_up 1") {
		t.Errorf("the exposition does not contain xui_up:\n%s", body)
	}
}

func TestE2EMetricsReportsInboundTraffic(t *testing.T) {
	p := newPanel(t)
	p.login()
	if msg := p.decode(p.postForm("xui/inbound/add", inboundForm(22001, "vmess", vmessSettings))); !msg.Success {
		t.Fatalf("add inbound: %s", msg.Msg)
	}

	body := string(readBody(t, p.get("/metrics")))
	if !strings.Contains(body, "xui_inbound_up_bytes") || !strings.Contains(body, "xui_inbound_down_bytes") {
		t.Fatalf("the exposition has no inbound traffic series:\n%s", body)
	}
	// 标签用 tag / id / protocol，全是有限集合，不会随请求内容膨胀。
	if !strings.Contains(body, `protocol="vmess"`) {
		t.Errorf("the inbound series is not labelled with its protocol:\n%s", body)
	}
}

// 路由模板必须做标签，否则每个客户端 id 都会长出一条独立时间序列。
func TestE2EMetricsKeepsPathCardinalityLow(t *testing.T) {
	p := newPanel(t)
	p.login()

	// 打几个带不同 id 的 404/失败请求。
	for _, id := range []string{"1", "2", "3"} {
		p.decode(p.postForm("xui/client/del/"+id, nil))
	}

	body := string(readBody(t, p.get("/metrics")))
	if !strings.Contains(body, `path="/xui/client/del/:id"`) {
		t.Errorf("requests are not aggregated under the route template:\n%s", body)
	}
	for _, id := range []string{"/xui/client/del/1", "/xui/client/del/2"} {
		if strings.Contains(body, `path="`+id+`"`) {
			t.Errorf("the raw path %q became a label; cardinality grows with every id", id)
		}
	}
}

// 未匹配任何路由的请求全部归到 "other"：攻击者能构造无限多不存在的路径。
func TestE2EMetricsFoldsUnknownPaths(t *testing.T) {
	p := newPanel(t)
	p.login()

	for _, path := range []string{"/nope-1", "/nope-2", "/nope-3"} {
		readBody(t, p.get(path))
	}

	body := string(readBody(t, p.get("/metrics")))
	if !strings.Contains(body, `path="other"`) {
		t.Errorf("unmatched requests are not folded into a single series:\n%s", body)
	}
	if strings.Contains(body, `path="/nope-1"`) {
		t.Error("a scanner can create one time series per bogus URL")
	}
}

func TestE2EMetricsCountsLoginFailures(t *testing.T) {
	p := newPanel(t)
	writeSetting(t, "metricsToken", "scrape-me")

	p.loginAs(p.username, "wrong-password", false)

	body := string(readBody(t, p.scrape("/metrics", "scrape-me")))
	if !strings.Contains(body, `xui_login_fail_total{reason="bad_credentials"}`) {
		t.Errorf("failed logins are not counted:\n%s", body)
	}
}

// /healthz 必须继续匿名可用：探针里没有任何可供侦察的内容，
// 而 k8s / 负载均衡器不会带 cookie 也不该拿到抓取 token。
func TestE2EMetricsDoesNotBreakHealthz(t *testing.T) {
	p := newPanel(t)
	writeSetting(t, "metricsToken", "scrape-me")

	for _, path := range []string{"/healthz", "/readyz"} {
		resp := p.scrape(path, "")
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (body %q)", path, resp.StatusCode, body)
		}
	}
}

// 自定义 basePath 下 /metrics 也得挂上，否则反向代理只转发子路径时抓不到。
func TestE2EMetricsUnderABasePath(t *testing.T) {
	p := newPanel(t, withBasePath("/panel/"))
	writeSetting(t, "metricsToken", "scrape-me")

	for _, path := range []string{"/metrics", "/panel/metrics"} {
		resp := p.scrape(path, "scrape-me")
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (body %q)", path, resp.StatusCode, body)
		}
	}
}

// 抓取端点不能变成一条绕过登录的配置读取通道。
func TestE2EMetricsExposesNoSecrets(t *testing.T) {
	p := newPanel(t)
	p.login()
	if msg := p.decode(p.postForm("xui/inbound/add", inboundForm(22002, "vmess", vmessSettings))); !msg.Success {
		t.Fatalf("add inbound: %s", msg.Msg)
	}
	client := p.addClient(lastInboundID(t, p), url.Values{
		"email":  {"metrics@example.com"},
		"enable": {"true"},
		"uuid":   {"11111111-1111-1111-1111-111111111111"},
	})

	body := string(readBody(t, p.get("/metrics")))
	for name, secret := range map[string]string{
		"subscription token": client.SubToken,
		"client uuid":        client.UUID,
		"client email":       client.Email,
	} {
		if strings.Contains(body, secret) {
			t.Errorf("the exposition leaks the %s", name)
		}
	}
}
