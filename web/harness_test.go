// Package web 的端到端测试脚手架。
//
// 这些用例把真正的 gin 引擎（同一套中间件栈、同一套内嵌模板）架在
// httptest 服务器上，对着临时目录里的 SQLite 库跑完整的 HTTP 流程。
//
// 两条硬约束：
//   - 数据库永远落在 t.TempDir()，绝不触碰 /etc/x-ui；
//   - 不需要 sing-box 二进制。cron 建了但不 Start，定时任务不会真的去拉内核。
package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/op/go-logging"
	"github.com/robfig/cron/v3"

	"x-ui/database"
	"x-ui/database/model"
	"x-ui/logger"
	"x-ui/testutil"
	"x-ui/web/global"
	"x-ui/web/middleware"
	"x-ui/web/service"
)

// TestMain 把测试期的日志压到 ERROR，并把审计流引到黑洞。
// 端到端用例会产生大量正常的登录/入站事件，全打在 stderr 上会把真正的
// 失败信息淹掉；需要断言审计内容的用例自己重新接管 SetAuditOutput。
func TestMain(m *testing.M) {
	logger.InitLogger(logging.ERROR)
	service.SetAuditOutput(io.Discard)
	os.Exit(m.Run())
}

// panel 是一个跑在 httptest 上的完整面板实例。
type panel struct {
	t *testing.T

	server   *Server
	http     *httptest.Server
	client   *http.Client
	basePath string

	// lastSessionCookie 是服务端最近一次下发的 session cookie 原件。
	// 并发用例会从多个 goroutine 发请求，故加锁。
	cookieMu          sync.Mutex
	lastSessionCookie *http.Cookie

	// 首启公告里的凭证——测试能登录的唯一途径，和运维看 journalctl 一样。
	username string
	password string
}

// writeSetting 直接写 settings 表。
//
// 走数据库而不是给 SettingService 加 test-only 方法：生产代码不该为了测试
// 多出可写配置的导出接口。
func writeSetting(t *testing.T, key, value string) {
	t.Helper()

	db := database.GetDB()
	existing := &model.Setting{}
	err := db.Model(model.Setting{}).Where("key = ?", key).First(existing).Error
	if database.IsNotFound(err) {
		if err := db.Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
			t.Fatalf("create setting %s: %v", key, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("read setting %s: %v", key, err)
	}
	existing.Value = value
	if err := db.Save(existing).Error; err != nil {
		t.Fatalf("save setting %s: %v", key, err)
	}
}

// newBareDB 只初始化数据库，不搭 HTTP 层。
// 给那些要自己调 Server.Start() / Stop() 的生命周期用例使用。
func newBareDB(t *testing.T) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	t.Setenv("XUI_DB_PATH", dbPath)

	old := database.SetCredentialsOutput(io.Discard)
	t.Cleanup(func() { database.SetCredentialsOutput(old) })

	if err := database.InitDB(dbPath); err != nil {
		t.Fatalf("init database: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })
}

type panelOption func(*panelConfig)

type panelConfig struct {
	basePath       string
	trustedProxies string
}

// withBasePath 让面板挂在子路径下，用来验证 cookie Path 与探针路由。
func withBasePath(p string) panelOption {
	return func(c *panelConfig) { c.basePath = p }
}

// withTrustedProxies 打开 XFF 采信，用于对照"默认不信任"的行为差异。
func withTrustedProxies(cidrs string) panelOption {
	return func(c *panelConfig) { c.trustedProxies = cidrs }
}

func newPanel(t *testing.T, opts ...panelOption) *panel {
	t.Helper()

	cfg := &panelConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	t.Setenv("XUI_DB_PATH", dbPath)

	banner := &bytes.Buffer{}
	oldWriter := database.SetCredentialsOutput(banner)
	t.Cleanup(func() { database.SetCredentialsOutput(oldWriter) })

	if err := database.InitDB(dbPath); err != nil {
		t.Fatalf("init panel database: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	username, password := testutil.ParseInitialCredentials(t, banner)

	if cfg.basePath != "" {
		writeSetting(t, "webBasePath", cfg.basePath)
	}
	if cfg.trustedProxies != "" {
		writeSetting(t, "webTrustedProxies", cfg.trustedProxies)
	}

	server := NewServer()
	global.SetWebServer(server)
	// cron 只建不启动：这里只走 initRouter 而不走 startTask，所以上面什么
	// 任务都没有；建出来是因为若干代码路径会取用它，启动它则毫无必要。
	server.cron = cron.New(cron.WithSeconds())
	t.Cleanup(func() { server.cron.Stop() })

	engine, err := server.initRouter()
	if err != nil {
		t.Fatalf("init router: %v", err)
	}

	ts := httptest.NewServer(engine)
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	basePath, err := (&service.SettingService{}).GetBasePath()
	if err != nil {
		t.Fatalf("read base path: %v", err)
	}

	return &panel{
		t:        t,
		server:   server,
		http:     ts,
		basePath: basePath,
		username: username,
		password: password,
		client: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// 断言重定向本身，所以不自动跟随。
				return http.ErrUseLastResponse
			},
		},
	}
}

// url 把面板内路径（相对 basePath）拼成绝对地址。"/healthz" 这类根路径原样使用。
func (p *panel) url(path string) string {
	if strings.HasPrefix(path, "/") {
		return p.http.URL + path
	}
	return p.http.URL + p.basePath + path
}

var csrfMeta = regexp.MustCompile(`<meta name="csrf-token" content="([^"]*)">`)

// csrfToken 走一次页面渲染拿 token 与 session cookie，正如浏览器所为。
//
// 已登录时 GET / 会 307 到 xui/，此时改从面板首页取 token —— 两个页面共用
// 同一个 head 模板，meta 标签的来源完全一致。
func (p *panel) csrfToken() string {
	p.t.Helper()

	resp := p.get("")
	body := readBody(p.t, resp)
	if resp.StatusCode == http.StatusTemporaryRedirect {
		resp = p.get("xui/")
		body = readBody(p.t, resp)
	}
	m := csrfMeta.FindSubmatch(body)
	if m == nil {
		p.t.Fatalf("page carries no csrf-token meta tag; the frontend cannot POST anything")
	}
	return string(m[1])
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return body
}

func (p *panel) do(req *http.Request) *http.Response {
	p.t.Helper()
	resp, err := p.client.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	// cookiejar 只保留 name/value，HttpOnly / Path / MaxAge 这些属性会被丢掉，
	// 而它们正是要断言的对象，所以在这里直接留存原始的 Set-Cookie。
	p.cookieMu.Lock()
	for _, c := range resp.Cookies() {
		if c.Name == "session" {
			p.lastSessionCookie = c
		}
	}
	p.cookieMu.Unlock()
	return resp
}

func (p *panel) get(path string, headers ...[2]string) *http.Response {
	p.t.Helper()
	req, err := http.NewRequest(http.MethodGet, p.url(path), nil)
	if err != nil {
		p.t.Fatalf("build request: %v", err)
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	return p.do(req)
}

// postForm 发一次带 CSRF token 的表单 POST，等价于前端 axios 的行为。
func (p *panel) postForm(path string, form url.Values, headers ...[2]string) *http.Response {
	p.t.Helper()
	return p.postFormWithToken(path, form, p.csrfToken(), headers...)
}

func (p *panel) postFormWithToken(path string, form url.Values, token string, headers ...[2]string) *http.Response {
	p.t.Helper()

	req, err := http.NewRequest(http.MethodPost, p.url(path), strings.NewReader(form.Encode()))
	if err != nil {
		p.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if token != "" {
		req.Header.Set(middleware.HeaderCSRFToken, token)
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	return p.do(req)
}

// apiMsg 是面板统一的 {success,msg,obj} 响应体。
type apiMsg struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (p *panel) decode(resp *http.Response) apiMsg {
	p.t.Helper()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.t.Fatalf("read response: %v", err)
	}
	var msg apiMsg
	if err := json.Unmarshal(body, &msg); err != nil {
		p.t.Fatalf("response is not the panel JSON envelope: %v\nbody: %s", err, body)
	}
	return msg
}

// login 用首启公告里的凭证登录，并断言成功。
func (p *panel) login() {
	p.t.Helper()
	p.loginAs(p.username, p.password, true)
}

// loginAs 尝试登录并断言结果，返回响应体。
func (p *panel) loginAs(username, password string, wantSuccess bool) apiMsg {
	p.t.Helper()

	msg := p.decode(p.postForm("login", url.Values{
		"username": {username},
		"password": {password},
	}))
	if msg.Success != wantSuccess {
		p.t.Fatalf("login as %q: success = %v (%s), want %v", username, msg.Success, msg.Msg, wantSuccess)
	}
	return msg
}

// sessionCookie 返回服务端最近一次下发的 session cookie（含全部属性）。
func (p *panel) sessionCookie() *http.Cookie {
	p.t.Helper()
	p.cookieMu.Lock()
	defer p.cookieMu.Unlock()
	return p.lastSessionCookie
}

// jarHasSession 报告 cookie jar 里是否还留着非空的 session cookie。
func (p *panel) jarHasSession() bool {
	p.t.Helper()

	u, err := url.Parse(p.url(""))
	if err != nil {
		p.t.Fatalf("parse panel url: %v", err)
	}
	for _, c := range p.client.Jar.Cookies(u) {
		if c.Name == "session" && c.Value != "" {
			return true
		}
	}
	return false
}

// isLoggedIn 用一个需要登录的只读接口探测会话是否有效。
func (p *panel) isLoggedIn() bool {
	p.t.Helper()
	return p.decode(p.postForm("xui/inbound/list", nil)).Success
}

// countInbounds 直接查库，绕过 API，用来断言"请求被拒时确实没落库"。
func (p *panel) countInbounds() int64 {
	p.t.Helper()

	var count int64
	if err := database.GetDB().Model(&model.Inbound{}).Count(&count).Error; err != nil {
		p.t.Fatalf("count inbounds: %v", err)
	}
	return count
}

// browser 是一个独立 cookie jar 的客户端，用于并发/跨会话场景。
type browser struct {
	t      *testing.T
	panel  *panel
	client *http.Client
}

func (p *panel) newClient() *browser {
	p.t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		p.t.Fatalf("create cookie jar: %v", err)
	}
	return &browser{
		t:     p.t,
		panel: p,
		client: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// primeCSRF 拉一次登录页，返回 token 与该会话的 cookie。
func (b *browser) primeCSRF() (string, []*http.Cookie) {
	req, err := http.NewRequest(http.MethodGet, b.panel.url(""), nil)
	if err != nil {
		b.t.Errorf("build request: %v", err)
		return "", nil
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Errorf("GET login page: %v", err)
		return "", nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Errorf("read login page: %v", err)
		return "", nil
	}
	m := csrfMeta.FindSubmatch(body)
	if m == nil {
		b.t.Error("login page carries no csrf-token meta tag")
		return "", nil
	}
	return string(m[1]), resp.Cookies()
}

func (b *browser) postForm(path string, form url.Values, token string, cookies []*http.Cookie) *http.Response {
	req, err := http.NewRequest(http.MethodPost, b.panel.url(path), strings.NewReader(form.Encode()))
	if err != nil {
		b.t.Errorf("build request: %v", err)
		return &http.Response{StatusCode: 0, Body: io.NopCloser(strings.NewReader(""))}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set(middleware.HeaderCSRFToken, token)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Errorf("POST %s: %v", path, err)
		return &http.Response{StatusCode: 0, Body: io.NopCloser(strings.NewReader(""))}
	}
	return resp
}
