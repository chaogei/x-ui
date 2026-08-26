package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"x-ui/database"
	"x-ui/database/model"
	"x-ui/testutil"
)

// seedClient 建一条入站加一个客户端，返回该客户端（含服务端生成的 sub_token）。
func seedClient(t *testing.T, protocol model.Protocol, port int, settings string, client *model.Client) *model.Client {
	t.Helper()

	inbound := seedInbound(t, protocol, port, settings)
	client.InboundId = inbound.Id
	client.Enable = true
	if err := (&ClientService{}).AddClient(client); err != nil {
		t.Fatalf("seed client: %v", err)
	}
	return client
}

func TestParseSubFormat(t *testing.T) {
	cases := map[string]SubFormat{
		"":         SubFormatV2rayN,
		"v2rayn":   SubFormatV2rayN,
		"clash":    SubFormatClash,
		"CLASH":    SubFormatClash,
		" clash ":  SubFormatClash,
		"sing-box": SubFormatSingBox,
		"singbox":  SubFormatSingBox,
		// 拼错的参数回落到默认格式而不是报错：订阅链接一旦发出去就很难改，
		// 因为一个手滑的 query 就让客户端里所有节点消失是更坏的结果。
		"yaml": SubFormatV2rayN,
	}
	for raw, want := range cases {
		if got := ParseSubFormat(raw); got != want {
			t.Errorf("ParseSubFormat(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSubscriptionRendersV2rayNByDefault(t *testing.T) {
	testutil.InitDB(t)
	client := seedClient(t, model.VMess, 30101, `{"users":[]}`, &model.Client{
		Email: "alice@example.com",
		UUID:  "11111111-1111-1111-1111-111111111111",
		Total: 1024,
	})
	// AddClient 有意忽略调用方提交的用量，所以流量只能从统计侧写进去。
	setClientTraffic(t, client.Id, 100, 200)

	sub, err := (&SubscriptionService{}).Render(client.SubToken, SubFormatV2rayN, "example.com")
	if err != nil {
		t.Fatalf("render subscription: %v", err)
	}
	if !strings.HasPrefix(sub.ContentType, "text/plain") {
		t.Errorf("content type = %q, want text/plain", sub.ContentType)
	}

	// 整份订阅体是 base64 的换行分隔链接列表，这是 v2rayN 系客户端的约定。
	decoded, err := base64.StdEncoding.DecodeString(sub.Body)
	if err != nil {
		t.Fatalf("subscription body is not base64: %v (%s)", err, sub.Body)
	}
	if !strings.HasPrefix(string(decoded), "vmess://") {
		t.Errorf("subscription body = %q, want a vmess link", decoded)
	}

	// 客户端靠这个头显示剩余流量；缺了它进度条恒为 0。
	if sub.UserInfo != "upload=100; download=200; total=1024; expire=0" {
		t.Errorf("Subscription-Userinfo = %q", sub.UserInfo)
	}
}

func TestSubscriptionUserinfoReportsExpiryInSeconds(t *testing.T) {
	testutil.InitDB(t)
	expiry := time.Now().Add(48 * time.Hour).UnixMilli()
	client := seedClient(t, model.VMess, 30102, `{"users":[]}`, &model.Client{
		Email:      "bob@example.com",
		UUID:       "22222222-2222-2222-2222-222222222222",
		ExpiryTime: expiry,
	})

	sub, err := (&SubscriptionService{}).Render(client.SubToken, SubFormatV2rayN, "example.com")
	if err != nil {
		t.Fatalf("render subscription: %v", err)
	}
	// Subscription-Userinfo 的 expire 是秒级时间戳，传毫秒会让客户端显示
	// 一个几万年后的到期日。
	if !strings.Contains(sub.UserInfo, "expire="+strconv.FormatInt(expiry/1000, 10)) {
		t.Errorf("Subscription-Userinfo = %q, want expire in seconds", sub.UserInfo)
	}
}

func TestSubscriptionRendersClash(t *testing.T) {
	testutil.InitDB(t)
	client := seedClient(t, model.VLESS, 30103,
		`{"users":[],"tls":{"enabled":true,"server_name":"www.microsoft.com",
		  "reality":{"enabled":true,"private_key":"SERVER-ONLY","public_key":"PBK","short_id":["ab"]}}}`,
		&model.Client{Email: "carol@example.com", UUID: "33333333-3333-3333-3333-333333333333"})

	sub, err := (&SubscriptionService{}).Render(client.SubToken, SubFormatClash, "example.com")
	if err != nil {
		t.Fatalf("render clash subscription: %v", err)
	}
	if !strings.Contains(sub.ContentType, "yaml") {
		t.Errorf("content type = %q, want a YAML type", sub.ContentType)
	}
	if strings.Contains(sub.Body, "SERVER-ONLY") {
		t.Fatal("the Clash subscription leaks the Reality private key")
	}

	var doc struct {
		Proxies []struct {
			Name        string `yaml:"name"`
			Type        string `yaml:"type"`
			UUID        string `yaml:"uuid"`
			RealityOpts struct {
				PublicKey string `yaml:"public-key"`
			} `yaml:"reality-opts"`
		} `yaml:"proxies"`
		ProxyGroups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal([]byte(sub.Body), &doc); err != nil {
		t.Fatalf("subscription body is not valid YAML: %v\n%s", err, sub.Body)
	}
	if len(doc.Proxies) != 1 {
		t.Fatalf("got %d proxies, want 1", len(doc.Proxies))
	}
	if doc.Proxies[0].Type != "vless" || doc.Proxies[0].UUID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("proxy = %+v, want the client's vless credentials", doc.Proxies[0])
	}
	if doc.Proxies[0].RealityOpts.PublicKey != "PBK" {
		t.Errorf("reality public-key = %q, want PBK", doc.Proxies[0].RealityOpts.PublicKey)
	}
	// 没有 proxy-group 的话，多数客户端会把节点导入成一个选不中的列表。
	if len(doc.ProxyGroups) != 1 || len(doc.ProxyGroups[0].Proxies) != 1 {
		t.Errorf("proxy-groups = %+v, want one group listing the node", doc.ProxyGroups)
	}
}

func TestSubscriptionRendersSingBox(t *testing.T) {
	testutil.InitDB(t)
	client := seedClient(t, model.Trojan, 30104,
		`{"users":[],"tls":{"enabled":true,"server_name":"trojan.example.com"}}`,
		&model.Client{Email: "dave@example.com", Password: "trojan-pass"})

	sub, err := (&SubscriptionService{}).Render(client.SubToken, SubFormatSingBox, "example.com")
	if err != nil {
		t.Fatalf("render sing-box subscription: %v", err)
	}
	if !strings.Contains(sub.ContentType, "json") {
		t.Errorf("content type = %q, want a JSON type", sub.ContentType)
	}

	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(sub.Body), &doc); err != nil {
		t.Fatalf("subscription body is not valid JSON: %v\n%s", err, sub.Body)
	}
	// 一个节点 + selector + direct。
	if len(doc.Outbounds) != 3 {
		t.Fatalf("got %d outbounds, want the node plus selector and direct", len(doc.Outbounds))
	}
	if doc.Outbounds[0]["type"] != "trojan" || doc.Outbounds[0]["password"] != "trojan-pass" {
		t.Errorf("outbound = %v, want the client's trojan password", doc.Outbounds[0])
	}
	if doc.Outbounds[1]["type"] != "selector" {
		t.Errorf("outbound[1] = %v, want a selector", doc.Outbounds[1])
	}
}

// 订阅接口的唯一凭证是 URL 里的 token，因此每一种失败都必须收敛成同一个
// "not found"：区分"token 不存在"与"token 存在但用户停用"会把接口变成
// 一个可枚举的存在性预言机。
func TestSubscriptionHidesEveryFailureBehindNotFound(t *testing.T) {
	testutil.InitDB(t)
	s := &SubscriptionService{}

	disabled := seedClient(t, model.VMess, 30105, `{"users":[]}`, &model.Client{
		Email: "disabled@example.com", UUID: "44444444-4444-4444-4444-444444444444",
	})
	setClientEnabled(t, disabled.Id, false)

	expired := seedClient(t, model.VMess, 30106, `{"users":[]}`, &model.Client{
		Email:      "expired@example.com",
		UUID:       "55555555-5555-5555-5555-555555555555",
		ExpiryTime: time.Now().Add(-time.Hour).UnixMilli(),
	})

	overQuota := seedClient(t, model.VMess, 30107, `{"users":[]}`, &model.Client{
		Email: "overquota@example.com",
		UUID:  "66666666-6666-6666-6666-666666666666",
		Total: 1000,
	})
	setClientTraffic(t, overQuota.Id, 600, 500)

	unshareable := seedClient(t, model.AnyTLS, 30108, `{"users":[]}`, &model.Client{
		Email: "anytls@example.com", Password: "p",
	})

	closedInbound := seedClient(t, model.VMess, 30109, `{"users":[]}`, &model.Client{
		Email: "closed@example.com", UUID: "77777777-7777-7777-7777-777777777777",
	})
	setInboundEnabled(t, closedInbound.InboundId, false)

	cases := []struct {
		name  string
		token string
	}{
		{"unknown token", "there-is-no-such-token"},
		{"empty token", ""},
		{"disabled client", disabled.SubToken},
		{"expired client", expired.SubToken},
		{"client over quota", overQuota.SubToken},
		{"unshareable protocol", unshareable.SubToken},
		{"disabled inbound", closedInbound.SubToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Render(tc.token, SubFormatV2rayN, "example.com"); !errors.Is(err, ErrSubscriptionNotFound) {
				t.Errorf("error = %v, want ErrSubscriptionNotFound", err)
			}
		})
	}
}

// 轮换 token 必须让旧链接立刻失效，否则"重置订阅"这个动作毫无意义。
func TestRotatingTheTokenKillsTheOldSubscription(t *testing.T) {
	testutil.InitDB(t)
	client := seedClient(t, model.VMess, 30110, `{"users":[]}`, &model.Client{
		Email: "eve@example.com", UUID: "88888888-8888-8888-8888-888888888888",
	})
	s := &SubscriptionService{}

	if _, err := s.Render(client.SubToken, SubFormatV2rayN, "example.com"); err != nil {
		t.Fatalf("render before rotation: %v", err)
	}
	fresh, err := (&ClientService{}).RotateSubToken(client.Id)
	if err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	if _, err := s.Render(client.SubToken, SubFormatV2rayN, "example.com"); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Errorf("the old token still resolves after rotation (error = %v)", err)
	}
	if _, err := s.Render(fresh, SubFormatV2rayN, "example.com"); err != nil {
		t.Errorf("the rotated token does not resolve: %v", err)
	}
}

func setClientEnabled(t *testing.T, id int, enable bool) {
	t.Helper()
	if err := database.GetDB().Model(model.Client{}).Where("id = ?", id).
		Update("enable", enable).Error; err != nil {
		t.Fatalf("update client enable: %v", err)
	}
}

func setClientTraffic(t *testing.T, id int, up, down int64) {
	t.Helper()
	if err := database.GetDB().Model(model.Client{}).Where("id = ?", id).
		Updates(map[string]interface{}{"up": up, "down": down}).Error; err != nil {
		t.Fatalf("update client traffic: %v", err)
	}
}

func setInboundEnabled(t *testing.T, id int, enable bool) {
	t.Helper()
	if err := database.GetDB().Model(model.Inbound{}).Where("id = ?", id).
		Update("enable", enable).Error; err != nil {
		t.Fatalf("update inbound enable: %v", err)
	}
}
