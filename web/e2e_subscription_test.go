package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"x-ui/database/model"
)

// 订阅接口是面板上唯一一个不需要登录的内容接口，所以它的端到端行为
// （鉴权、404 收敛、格式切换、缓存头）值得走真实 HTTP 栈验证一遍。

// addClient 通过面板 API 建一个客户端，返回服务端下发的记录（含 sub_token）。
func (p *panel) addClient(inboundID int, form url.Values) *model.Client {
	p.t.Helper()

	form.Set("inboundId", strconv.Itoa(inboundID))
	msg := p.decode(p.postForm("xui/client/add", form))
	if !msg.Success {
		p.t.Fatalf("add client: %s", msg.Msg)
	}
	client := &model.Client{}
	if err := json.Unmarshal(msg.Obj, client); err != nil {
		p.t.Fatalf("decode created client: %v", err)
	}
	if client.SubToken == "" {
		p.t.Fatal("the created client has no subscription token")
	}
	return client
}

// getAnonymous 发一个不带 cookie 也不带 CSRF 头的裸 GET —— 订阅客户端就是这样请求的。
func (p *panel) getAnonymous(path string) *http.Response {
	p.t.Helper()

	req, err := http.NewRequest(http.MethodGet, p.url(path), nil)
	if err != nil {
		p.t.Fatalf("build request: %v", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		p.t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// seedSubscription 建一条 vmess 入站并挂一个客户端，返回它。
func seedSubscription(t *testing.T, p *panel, port int) *model.Client {
	t.Helper()

	if msg := p.decode(p.postForm("xui/inbound/add", inboundForm(port, "vmess", vmessSettings))); !msg.Success {
		t.Fatalf("add inbound: %s", msg.Msg)
	}
	var inbounds []*model.Inbound
	msg := p.decode(p.postForm("xui/inbound/list", nil))
	if err := json.Unmarshal(msg.Obj, &inbounds); err != nil {
		t.Fatalf("decode inbound list: %v", err)
	}
	if len(inbounds) == 0 {
		t.Fatal("the inbound list is empty right after a successful add")
	}
	return p.addClient(inbounds[len(inbounds)-1].Id, url.Values{
		"email":  {"sub-" + strconv.Itoa(port) + "@example.com"},
		"enable": {"true"},
		"uuid":   {"11111111-1111-1111-1111-111111111111"},
	})
}

func TestE2ESubscriptionServesTokenHolders(t *testing.T) {
	p := newPanel(t)
	p.login()
	client := seedSubscription(t, p, 21001)

	resp := p.getAnonymous("sub/" + client.SubToken)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET sub/<token> = %d, want 200 (body %q)", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain for the default format", ct)
	}
	// 订阅体里是明文凭证，任何中间缓存都不该留副本。
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if info := resp.Header.Get("Subscription-Userinfo"); !strings.Contains(info, "total=") {
		t.Errorf("Subscription-Userinfo = %q, want the quota fields", info)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		t.Fatalf("subscription body is not base64: %v (%s)", err, body)
	}
	if !strings.HasPrefix(string(decoded), "vmess://") {
		t.Errorf("subscription body = %q, want a vmess link", decoded)
	}
	// 链接里的地址应当来自请求 Host，而不是硬编码的 localhost 字面量。
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(strings.TrimSpace(string(decoded)), "vmess://"))
	if err != nil {
		t.Fatalf("vmess payload is not base64: %v", err)
	}
	var node map[string]any
	if err := json.Unmarshal(payload, &node); err != nil {
		t.Fatalf("vmess payload is not JSON: %v (%s)", err, payload)
	}
	if node["port"] != "21001" {
		t.Errorf("vmess port = %v, want the inbound port", node["port"])
	}
	if node["id"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("vmess uuid = %v, want the client's uuid", node["id"])
	}
}

func TestE2ESubscriptionSwitchesFormat(t *testing.T) {
	p := newPanel(t)
	p.login()
	client := seedSubscription(t, p, 21002)

	cases := []struct {
		format      string
		contentType string
		contains    string
	}{
		{"clash", "yaml", "proxy-groups"},
		{"sing-box", "json", `"outbounds"`},
		{"singbox", "json", `"outbounds"`},
		// 未知格式回落到默认的 v2rayN，而不是报错。
		{"nonsense", "text/plain", ""},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			resp := p.getAnonymous("sub/" + client.SubToken + "?format=" + tc.format)
			body := readBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, tc.contentType) {
				t.Errorf("Content-Type = %q, want it to mention %q", ct, tc.contentType)
			}
			if tc.contains != "" && !strings.Contains(string(body), tc.contains) {
				t.Errorf("body does not contain %q:\n%s", tc.contains, body)
			}
		})
	}
}

// 任何失败都必须是同一个 404 —— 否则 token 就成了可枚举的存在性预言机。
func TestE2ESubscriptionReturns404ForEveryFailure(t *testing.T) {
	p := newPanel(t)
	p.login()
	client := seedSubscription(t, p, 21003)

	bodies := map[string]string{}
	for name, token := range map[string]string{
		"unknown token":   "definitely-not-a-real-token",
		"truncated token": client.SubToken[:len(client.SubToken)-1],
	} {
		resp := p.getAnonymous("sub/" + token)
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, resp.StatusCode)
		}
		bodies[name] = string(body)
	}
	if bodies["unknown token"] != bodies["truncated token"] {
		t.Errorf("the 404 bodies differ between failure modes: %q vs %q",
			bodies["unknown token"], bodies["truncated token"])
	}

	// 停用客户端后，同一个 token 也必须变成 404。
	clients := p.listClients(client.InboundId)
	if len(clients) != 1 {
		t.Fatalf("listed %d clients, want 1", len(clients))
	}
	msg := p.decode(p.postForm("xui/client/update/"+strconv.Itoa(client.Id), url.Values{
		"email":  {client.Email},
		"enable": {"false"},
		"uuid":   {client.UUID},
	}))
	if !msg.Success {
		t.Fatalf("disable client: %s", msg.Msg)
	}
	resp := p.getAnonymous("sub/" + client.SubToken)
	if body := readBody(t, resp); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a disabled client still serves its subscription: %d %q", resp.StatusCode, body)
	}
}

func TestE2ESubscriptionTokenRotation(t *testing.T) {
	p := newPanel(t)
	p.login()
	client := seedSubscription(t, p, 21004)

	msg := p.decode(p.postForm("xui/client/rotateToken/"+strconv.Itoa(client.Id), nil))
	if !msg.Success {
		t.Fatalf("rotate token: %s", msg.Msg)
	}
	var rotated struct {
		SubToken string `json:"subToken"`
	}
	if err := json.Unmarshal(msg.Obj, &rotated); err != nil {
		t.Fatalf("decode rotated token: %v", err)
	}
	if rotated.SubToken == "" || rotated.SubToken == client.SubToken {
		t.Fatalf("rotated token = %q, want a fresh value", rotated.SubToken)
	}

	if resp := p.getAnonymous("sub/" + client.SubToken); resp.StatusCode != http.StatusNotFound {
		readBody(t, resp)
		t.Errorf("the old subscription link still works after rotation: %d", resp.StatusCode)
	}
	resp := p.getAnonymous("sub/" + rotated.SubToken)
	readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the rotated subscription link returns %d, want 200", resp.StatusCode)
	}
}

// 反向代理后面板看到的 Host 常常是内网名字，直接写进订阅会让客户端
// 连到一个解析不出来的地址，所以 subAddress 必须能覆盖它。
func TestE2ESubscriptionHonoursTheConfiguredAddress(t *testing.T) {
	p := newPanel(t)
	p.login()
	client := seedSubscription(t, p, 21005)
	writeSetting(t, "subAddress", "vpn.example.org")

	resp := p.getAnonymous("sub/" + client.SubToken + "?format=sing-box")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "vpn.example.org") {
		t.Errorf("the subscription does not use the configured address:\n%s", body)
	}
	if strings.Contains(string(body), "127.0.0.1") {
		t.Errorf("the subscription leaks the panel's own listen address:\n%s", body)
	}
}

// 订阅接口不能被当成"绕过登录的入站导出"：它只暴露 token 对应的那一个客户端。
func TestE2ESubscriptionOnlyExposesItsOwnClient(t *testing.T) {
	p := newPanel(t)
	p.login()
	first := seedSubscription(t, p, 21006)
	second := seedSubscription(t, p, 21007)

	resp := p.getAnonymous("sub/" + first.SubToken + "?format=sing-box")
	body := string(readBody(t, resp))
	if strings.Contains(body, strconv.Itoa(21007)) {
		t.Errorf("the subscription exposes another client's inbound:\n%s", body)
	}
	if second.SubToken != "" && strings.Contains(body, second.SubToken) {
		t.Error("the subscription body leaks another client's token")
	}
}

// listClients 走 API 列出某条入站下的客户端。
func (p *panel) listClients(inboundID int) []*model.Client {
	p.t.Helper()

	msg := p.decode(p.postForm("xui/client/list/"+strconv.Itoa(inboundID), nil))
	if !msg.Success {
		p.t.Fatalf("list clients: %s", msg.Msg)
	}
	var clients []*model.Client
	if err := json.Unmarshal(msg.Obj, &clients); err != nil {
		p.t.Fatalf("decode client list: %v", err)
	}
	return clients
}
