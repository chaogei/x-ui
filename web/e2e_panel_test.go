package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"x-ui/core/singbox/spec"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/web/service"
)

// inboundForm 造一条合法的入站表单。
func inboundForm(port int, protocol, settings string) url.Values {
	return url.Values{
		"port":     {strconv.Itoa(port)},
		"protocol": {protocol},
		"remark":   {"e2e"},
		"settings": {settings},
		"sniffing": {`{"sniff":true}`},
		"total":    {"0"},
	}
}

const vmessSettings = `{"users":[{"name":"e2e","uuid":"11111111-1111-1111-1111-111111111111"}]}`

// TestE2EProtocolSpecAPI 覆盖协议元数据接口——前端整个新建入站对话框都依赖它。
func TestE2EProtocolSpecAPI(t *testing.T) {
	p := newPanel(t)

	t.Run("requires login", func(t *testing.T) {
		resp := p.get("xui/api/protocols", [2]string{"X-Requested-With", "XMLHttpRequest"})
		msg := p.decode(resp)
		if msg.Success {
			t.Fatal("protocol metadata is served to unauthenticated clients")
		}
	})

	p.login()

	resp := p.get("xui/api/protocols", [2]string{"X-Requested-With", "XMLHttpRequest"})
	msg := p.decode(resp)
	if !msg.Success {
		t.Fatalf("GET /xui/api/protocols failed: %s", msg.Msg)
	}

	var got []spec.Spec
	if err := json.Unmarshal(msg.Obj, &got); err != nil {
		t.Fatalf("decode protocol list: %v", err)
	}
	want := spec.All()
	if len(got) != len(want) {
		t.Fatalf("the API returned %d protocols, want %d", len(got), len(want))
	}
	if len(got) != 14 {
		t.Errorf("the panel advertises %d protocols, want the documented 14", len(got))
	}
	// 顺序也是契约：前端下拉框直接按返回顺序渲染。
	for i := range want {
		if got[i].Key != want[i].Key {
			t.Errorf("protocol %d = %q, want %q", i, got[i].Key, want[i].Key)
		}
	}
}

// TestE2ERealityKeyPairAPI 覆盖"生成密钥"按钮背后的接口。
func TestE2ERealityKeyPairAPI(t *testing.T) {
	p := newPanel(t)
	p.login()

	msg := p.decode(p.postForm("xui/api/reality/keypair", nil))
	if !msg.Success {
		t.Fatalf("generate reality keypair failed: %s", msg.Msg)
	}

	var pair service.RealityKeyPair
	if err := json.Unmarshal(msg.Obj, &pair); err != nil {
		t.Fatalf("decode key pair: %v", err)
	}
	if pair.PrivateKey == "" || pair.PublicKey == "" {
		t.Fatalf("key pair = %+v, want both halves populated", pair)
	}
	derived, err := service.DeriveRealityPublicKey(pair.PrivateKey)
	if err != nil {
		t.Fatalf("derive public key: %v", err)
	}
	if derived != pair.PublicKey {
		t.Errorf("the API handed out a mismatched pair: public %q, derived %q", pair.PublicKey, derived)
	}
}

// TestE2EInboundCRUD 是入站管理的主干流程。
func TestE2EInboundCRUD(t *testing.T) {
	p := newPanel(t)
	p.login()

	// 空面板。
	if inbounds := p.listInbounds(); len(inbounds) != 0 {
		t.Fatalf("a fresh panel lists %d inbounds, want none", len(inbounds))
	}

	// 新增。
	if msg := p.decode(p.postForm("xui/inbound/add", inboundForm(20001, "vmess", vmessSettings))); !msg.Success {
		t.Fatalf("add inbound failed: %s", msg.Msg)
	}
	inbounds := p.listInbounds()
	if len(inbounds) != 1 {
		t.Fatalf("after add the panel lists %d inbounds, want 1", len(inbounds))
	}
	created := inbounds[0]
	if created.Port != 20001 || created.Protocol != model.VMess {
		t.Errorf("created inbound = port %d protocol %q, want 20001/vmess", created.Port, created.Protocol)
	}
	if !created.Enable {
		t.Error("a newly created inbound must be enabled")
	}
	if created.Tag == "" {
		t.Error("the inbound has no tag; sing-box needs one to address it")
	}

	// 修改。
	update := inboundForm(20002, "vmess", vmessSettings)
	update.Set("remark", "renamed")
	if msg := p.decode(p.postForm("xui/inbound/update/"+strconv.Itoa(created.Id), update)); !msg.Success {
		t.Fatalf("update inbound failed: %s", msg.Msg)
	}
	updated := p.listInbounds()[0]
	if updated.Port != 20002 {
		t.Errorf("port = %d after update, want 20002", updated.Port)
	}
	if updated.Remark != "renamed" {
		t.Errorf("remark = %q after update, want %q", updated.Remark, "renamed")
	}

	// 删除。
	if msg := p.decode(p.postForm("xui/inbound/del/"+strconv.Itoa(created.Id), nil)); !msg.Success {
		t.Fatalf("delete inbound failed: %s", msg.Msg)
	}
	if inbounds := p.listInbounds(); len(inbounds) != 0 {
		t.Fatalf("after delete the panel lists %d inbounds, want none", len(inbounds))
	}
}

// TestE2EInboundRejectsReservedKeys 是 M-4 的端到端断言。
//
// settings 里的 listen_port 会在生成的 sing-box 配置里覆盖真正的监听端口：
// 面板显示 20003，内核实际听在 1，且没有任何提示。
func TestE2EInboundRejectsReservedKeys(t *testing.T) {
	p := newPanel(t)
	p.login()

	for _, key := range []string{"listen_port", "listen", "tag", "type"} {
		t.Run(key, func(t *testing.T) {
			settings := `{"users":[],"` + key + `":"hijacked"}`
			msg := p.decode(p.postForm("xui/inbound/add", inboundForm(20003, "vmess", settings)))
			if msg.Success {
				t.Fatalf("settings carrying the reserved key %q were accepted", key)
			}
			if !strings.Contains(msg.Msg, key) {
				t.Errorf("error message %q should name the offending key %q", msg.Msg, key)
			}
			if p.countInbounds() != 0 {
				t.Fatal("the rejected inbound was still persisted")
			}
		})
	}
}

// TestE2EInboundRejectsInvalidPayloads 逐项覆盖字段级校验，
// 并确认失败的写入不会在库里留下半条记录。
func TestE2EInboundRejectsInvalidPayloads(t *testing.T) {
	p := newPanel(t)
	p.login()

	cases := map[string]url.Values{
		"unknown protocol":    inboundForm(20010, "carrier-pigeon", `{}`),
		"port zero":           inboundForm(0, "vmess", vmessSettings),
		"port out of range":   inboundForm(70000, "vmess", vmessSettings),
		"settings not object": inboundForm(20011, "vmess", `[1,2,3]`),
		"settings malformed":  inboundForm(20012, "vmess", `{"users":[`),
		"sniffing not object": func() url.Values {
			v := inboundForm(20013, "vmess", vmessSettings)
			v.Set("sniffing", `"yes please"`)
			return v
		}(),
	}
	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			msg := p.decode(p.postForm("xui/inbound/add", form))
			if msg.Success {
				t.Fatalf("an invalid inbound was accepted: %s", msg.Msg)
			}
			if p.countInbounds() != 0 {
				t.Fatal("the invalid inbound was persisted anyway")
			}
		})
	}
}

// TestE2EInboundRejectsMismatchedRealityKeys 覆盖 Reality 分享链接的服务端护栏。
func TestE2EInboundRejectsMismatchedRealityKeys(t *testing.T) {
	p := newPanel(t)
	p.login()

	a, err := service.GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	b, err := service.GenerateRealityKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	realitySettings := func(priv, pub string) string {
		return `{"users":[{"uuid":"11111111-1111-1111-1111-111111111111"}],` +
			`"tls":{"enabled":true,"reality":{"enabled":true,"private_key":"` + priv + `","public_key":"` + pub + `"}}}`
	}

	t.Run("mismatched", func(t *testing.T) {
		msg := p.decode(p.postForm("xui/inbound/add", inboundForm(20020, "vless", realitySettings(a.PrivateKey, b.PublicKey))))
		if msg.Success {
			t.Fatal("a mismatched Reality key pair was accepted; the share link would never connect")
		}
		if p.countInbounds() != 0 {
			t.Fatal("the broken inbound was persisted")
		}
	})

	t.Run("missing public key", func(t *testing.T) {
		msg := p.decode(p.postForm("xui/inbound/add", inboundForm(20021, "vless", realitySettings(a.PrivateKey, ""))))
		if msg.Success {
			t.Fatal("Reality without a public_key was accepted; the share link's pbk would be empty")
		}
	})

	t.Run("matching", func(t *testing.T) {
		msg := p.decode(p.postForm("xui/inbound/add", inboundForm(20022, "vless", realitySettings(a.PrivateKey, a.PublicKey))))
		if !msg.Success {
			t.Fatalf("a correct Reality pair was rejected: %s", msg.Msg)
		}
	})
}

// TestE2EInboundPortConflict 同一端口同一传输层不允许两条入站。
func TestE2EInboundPortConflict(t *testing.T) {
	p := newPanel(t)
	p.login()

	if msg := p.decode(p.postForm("xui/inbound/add", inboundForm(20030, "vmess", vmessSettings))); !msg.Success {
		t.Fatalf("add first inbound: %s", msg.Msg)
	}
	msg := p.decode(p.postForm("xui/inbound/add", inboundForm(20030, "trojan", `{"users":[{"password":"secret"}]}`)))
	if msg.Success {
		t.Fatal("two TCP inbounds were allowed to share port 20030")
	}
	if p.countInbounds() != 1 {
		t.Errorf("inbound count = %d, want the single original", p.countInbounds())
	}

	// UDP 协议与 TCP 协议可以共用同一端口，这是合法配置，不能误杀。
	if msg := p.decode(p.postForm("xui/inbound/add", inboundForm(20030, "hysteria2",
		`{"up_mbps":100,"down_mbps":100,"users":[{"password":"secret"}]}`))); !msg.Success {
		t.Errorf("a UDP inbound on the same port as a TCP one was rejected: %s", msg.Msg)
	}
}

// TestE2ESettingsUpdate 覆盖设置页的读写与校验。
func TestE2ESettingsUpdate(t *testing.T) {
	p := newPanel(t)
	p.login()

	msg := p.decode(p.postForm("xui/setting/all", nil))
	if !msg.Success {
		t.Fatalf("read settings failed: %s", msg.Msg)
	}
	var current map[string]interface{}
	if err := json.Unmarshal(msg.Obj, &current); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	for _, key := range []string{"webPort", "webBasePath", "timeLocation", "webTrustedProxies"} {
		if _, ok := current[key]; !ok {
			t.Errorf("the settings payload is missing %q", key)
		}
	}
	// 设置接口绝不能回传 session 密钥。
	if _, leaked := current["secret"]; leaked {
		t.Error("the settings API leaks the session secret")
	}

	form := settingsForm(current)
	form.Set("timeLocation", "UTC")
	if msg := p.decode(p.postForm("xui/setting/update", form)); !msg.Success {
		t.Fatalf("update settings failed: %s", msg.Msg)
	}
	if got := readSetting(t, "timeLocation"); got != "UTC" {
		t.Errorf("timeLocation = %q after update, want UTC", got)
	}

	t.Run("rejects invalid values", func(t *testing.T) {
		invalid := map[string]string{
			"timeLocation":      "Mars/Olympus_Mons",
			"webPort":           "70000",
			"webListen":         "not-an-ip",
			"webTrustedProxies": "10.0.0.0/8, not-a-cidr",
		}
		for key, value := range invalid {
			t.Run(key, func(t *testing.T) {
				bad := settingsForm(current)
				bad.Set("timeLocation", "UTC")
				bad.Set(key, value)
				if msg := p.decode(p.postForm("xui/setting/update", bad)); msg.Success {
					t.Fatalf("%s=%q was accepted", key, value)
				}
				if got := readSetting(t, "timeLocation"); got != "UTC" {
					t.Errorf("a rejected update still changed timeLocation to %q", got)
				}
			})
		}
	})
}

// TestE2EPasswordChangeClearsTheWarning 覆盖改密流程与首启告警的熄灭。
func TestE2EPasswordChangeClearsTheWarning(t *testing.T) {
	p := newPanel(t)
	p.login()

	users := &service.UserService{}
	if !users.UsingInitialCredentials() {
		t.Fatal("the initial-credentials warning should be on before the password is changed")
	}

	const newPassword = "a-much-better-password-42"
	msg := p.decode(p.postForm("xui/setting/updateUser", url.Values{
		"oldUsername": {p.username},
		"oldPassword": {p.password},
		"newUsername": {"operator"},
		"newPassword": {newPassword},
	}))
	if !msg.Success {
		t.Fatalf("change password failed: %s", msg.Msg)
	}
	if users.UsingInitialCredentials() {
		t.Error("the initial-credentials warning is still on after the operator set their own password")
	}

	// 旧凭证失效，新凭证可用。
	fresh := newPanelClient(t, p)
	fresh.loginAs(p.username, p.password, false)
	fresh2 := newPanelClient(t, p)
	fresh2.loginAs("operator", newPassword, true)
}

// TestE2EWrongOldPasswordIsRejected 防止拿到会话就能改密。
func TestE2EWrongOldPasswordIsRejected(t *testing.T) {
	p := newPanel(t)
	p.login()

	msg := p.decode(p.postForm("xui/setting/updateUser", url.Values{
		"oldUsername": {p.username},
		"oldPassword": {"not-the-old-password"},
		"newUsername": {"operator"},
		"newPassword": {"whatever-42"},
	}))
	if msg.Success {
		t.Fatal("the password was changed without knowing the old one")
	}

	// 原凭证依然有效。
	fresh := newPanelClient(t, p)
	fresh.loginAs(p.username, p.password, true)
}

// TestE2EInboundsPageShowsTheCredentialWarning 覆盖那条被写死为 v-if="false"
// 的安全告警：它现在由真实标记驱动，改密之后自动消失。
const warnInitialCredentialsEN = "You are still using the randomly generated first-boot password"

func TestE2EInboundsPageShowsTheCredentialWarning(t *testing.T) {
	p := newPanel(t)
	p.login()

	english := [2]string{"Accept-Language", "en-US"}

	before := string(readBody(t, p.get("xui/inbounds", english)))
	if !strings.Contains(before, warnInitialCredentialsEN) {
		t.Fatalf("the inbounds page does not warn about the generated password:\n%s",
			excerpt(before, "a-alert"))
	}

	if msg := p.decode(p.postForm("xui/setting/updateUser", url.Values{
		"oldUsername": {p.username},
		"oldPassword": {p.password},
		"newUsername": {p.username},
		"newPassword": {"a-much-better-password-42"},
	})); !msg.Success {
		t.Fatalf("change password failed: %s", msg.Msg)
	}

	after := string(readBody(t, p.get("xui/inbounds", english)))
	if strings.Contains(after, warnInitialCredentialsEN) {
		t.Error("the warning is still shown after the operator set their own password")
	}
}

// TestE2EHealthEndpoints 覆盖运维探针。
func TestE2EHealthEndpoints(t *testing.T) {
	p := newPanel(t)

	for _, path := range []string{"/healthz", "/readyz", "/api/v1/health", "/api/v1/ready"} {
		t.Run(path, func(t *testing.T) {
			resp := p.get(path)
			body := readBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s returned %d, want 200 without authentication", path, resp.StatusCode)
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("probe response is not JSON: %v (%s)", err, body)
			}
			if payload["status"] != "ok" {
				t.Errorf("status = %v, want ok", payload["status"])
			}
			// 探针面向未认证客户端，绝不能吐出部署细节。
			for _, secret := range []string{"secret", "password", "db_path"} {
				if _, leaked := payload[secret]; leaked {
					t.Errorf("the probe response leaks %q", secret)
				}
			}
			if strings.Contains(string(body), p.password) {
				t.Error("the probe response leaks the admin password")
			}
		})
	}
}

// TestE2EReadyzReportsCoreState ?core=1 才把内核状态纳入就绪判定。
// 测试环境里没有 sing-box 二进制，所以这条必然是 503——这正是要断言的。
func TestE2EReadyzReportsCoreState(t *testing.T) {
	p := newPanel(t)

	resp := p.get("/readyz?core=1")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz?core=1 returned %d with no kernel running, want 503", resp.StatusCode)
	}
	if !strings.Contains(string(body), "stopped") {
		t.Errorf("body = %s, want it to report the stopped core", body)
	}

	// 不带 core=1 时面板依然就绪：内核没起来时运维还得靠面板去改配置。
	if resp := p.get("/readyz"); resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Errorf("GET /readyz returned %d; the panel is usable even without a running kernel", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

// TestE2EHealthEndpointsUnderCustomBasePath 自定义 basePath 时探针不能 404。
func TestE2EHealthEndpointsUnderCustomBasePath(t *testing.T) {
	p := newPanel(t, withBasePath("/panel/"))

	for _, path := range []string{"/healthz", "/panel/healthz", "/readyz", "/panel/readyz"} {
		resp := p.get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s returned %d, want 200", path, resp.StatusCode)
		}
	}
}

// TestE2ESecurityHeaders 覆盖响应头加固。
func TestE2ESecurityHeaders(t *testing.T) {
	p := newPanel(t)

	resp := p.get("")
	resp.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for header, value := range want {
		if got := resp.Header.Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q, want frame-ancestors 'none'", csp)
	}
	// 面板跑在 HTTP 上，下发 HSTS 会把运维永久挡在门外。
	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS = %q over plain HTTP", got)
	}
}

// listInbounds 通过 API 读取入站列表。
func (p *panel) listInbounds() []*model.Inbound {
	p.t.Helper()

	msg := p.decode(p.postForm("xui/inbound/list", nil))
	if !msg.Success {
		p.t.Fatalf("list inbounds failed: %s", msg.Msg)
	}
	var inbounds []*model.Inbound
	if err := json.Unmarshal(msg.Obj, &inbounds); err != nil {
		p.t.Fatalf("decode inbound list: %v", err)
	}
	return inbounds
}

// newPanelClient 返回一个共享同一个面板但 cookie 独立的 panel 视图，
// 用来验证"换个浏览器重新登录"的效果。
func newPanelClient(t *testing.T, base *panel) *panel {
	t.Helper()

	clone := *base
	clone.t = t
	b := base.newClient()
	clone.client = b.client
	clone.lastSessionCookie = nil
	return &clone
}

// settingsForm 把 /setting/all 的响应转回表单，模拟前端"读出来改一个字段再提交"。
func settingsForm(current map[string]interface{}) url.Values {
	form := url.Values{}
	for key, value := range current {
		switch v := value.(type) {
		case string:
			form.Set(key, v)
		case bool:
			form.Set(key, strconv.FormatBool(v))
		case float64:
			form.Set(key, strconv.FormatInt(int64(v), 10))
		}
	}
	return form
}

func readSetting(t *testing.T, key string) string {
	t.Helper()

	setting := &model.Setting{}
	if err := database.GetDB().Model(model.Setting{}).Where("key = ?", key).First(setting).Error; err != nil {
		t.Fatalf("read setting %s: %v", key, err)
	}
	return setting.Value
}

// excerpt 截取 needle 附近的片段，让失败信息可读。
func excerpt(haystack, needle string) string {
	idx := strings.Index(haystack, needle)
	if idx < 0 {
		return "(not found)"
	}
	start := idx - 120
	if start < 0 {
		start = 0
	}
	end := idx + 120
	if end > len(haystack) {
		end = len(haystack)
	}
	return haystack[start:end]
}
