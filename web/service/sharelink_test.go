package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"x-ui/database/model"
)

// 这些用例全部是纯函数测试：分享链接生成不碰数据库，也不碰网络。

func inboundFor(protocol model.Protocol, port int, settings string) *model.Inbound {
	return &model.Inbound{
		Id:       1,
		Enable:   true,
		Port:     port,
		Protocol: protocol,
		Settings: settings,
		Remark:   "node",
		Tag:      "inbound-" + string(protocol),
	}
}

// parseLink 拆开一条 scheme://userinfo@host?query#fragment 形式的分享链接。
func parseLink(t *testing.T, link string) *url.URL {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("share link is not a URL: %v (%s)", err, link)
	}
	return u
}

func TestVMessLinkCarriesTheV2rayNPayload(t *testing.T) {
	target := ShareTarget{
		Inbound: inboundFor(model.VMess, 443,
			`{"users":[{"name":"alice","uuid":"11111111-1111-1111-1111-111111111111"}],
			  "tls":{"enabled":true,"server_name":"cdn.example.com"},
			  "transport":{"type":"ws","path":"/ray","host":["cdn.example.com"]}}`),
		Client:  &model.Client{Email: "alice", UUID: "22222222-2222-2222-2222-222222222222"},
		Address: "203.0.113.7",
		Remark:  "tokyo",
	}

	link, err := BuildShareLink(target)
	if err != nil {
		t.Fatalf("build vmess link: %v", err)
	}
	payload, ok := strings.CutPrefix(link, "vmess://")
	if !ok {
		t.Fatalf("link does not use the vmess scheme: %s", link)
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("vmess payload is not base64: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("vmess payload is not JSON: %v (%s)", err, raw)
	}

	want := map[string]any{
		"v":    "2",
		"ps":   "tokyo",
		"add":  "cdn.example.com", // TLS 场景下用 server_name，不是入口 IP
		"port": "443",
		"id":   "22222222-2222-2222-2222-222222222222", // 客户端凭证优先于 settings
		"net":  "ws",
		"path": "/ray",
		"host": "cdn.example.com",
		"tls":  "tls",
		"sni":  "cdn.example.com",
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("vmess payload %q = %v, want %v", key, got[key], expected)
		}
	}
}

func TestVLESSRealityLinkCarriesThePublicKey(t *testing.T) {
	target := ShareTarget{
		Inbound: inboundFor(model.VLESS, 8443,
			`{"users":[{"name":"a","uuid":"u","flow":"xtls-rprx-vision"}],
			  "tls":{"enabled":true,"server_name":"www.microsoft.com",
			         "reality":{"enabled":true,"private_key":"SERVER-ONLY","public_key":"PBK-VALUE","short_id":["ab","cd"]}}}`),
		Address: "203.0.113.7",
		Remark:  "reality",
	}

	link, err := BuildShareLink(target)
	if err != nil {
		t.Fatalf("build vless link: %v", err)
	}
	if strings.Contains(link, "SERVER-ONLY") {
		t.Fatal("the share link leaks the Reality private key to subscribers")
	}
	q := parseLink(t, link).Query()
	if q.Get("security") != "reality" {
		t.Errorf("security = %q, want reality", q.Get("security"))
	}
	// pbk 缺失时客户端一律握手失败，这是 Reality 链接最容易出的问题。
	if q.Get("pbk") != "PBK-VALUE" {
		t.Errorf("pbk = %q, want the Reality public key", q.Get("pbk"))
	}
	if q.Get("sid") != "ab" {
		t.Errorf("sid = %q, want the first short id", q.Get("sid"))
	}
	if q.Get("sni") != "www.microsoft.com" {
		t.Errorf("sni = %q, want the Reality server name", q.Get("sni"))
	}
	if q.Get("flow") != "xtls-rprx-vision" {
		t.Errorf("flow = %q, want the value carried on the user entry", q.Get("flow"))
	}
}

// 老入站的凭证只存在于 settings.users[0] 里，clients 表是空的。
// 这条回落路径断了的话，升级面板会让所有既有节点的分享链接变成空凭证。
func TestShareLinkFallsBackToTheInboundSettings(t *testing.T) {
	cases := []struct {
		name     string
		protocol model.Protocol
		settings string
		want     string
	}{
		{
			name:     "vmess uuid",
			protocol: model.VMess,
			settings: `{"users":[{"name":"legacy","uuid":"LEGACY-UUID"}]}`,
			want:     "LEGACY-UUID",
		},
		{
			name:     "trojan password",
			protocol: model.Trojan,
			settings: `{"users":[{"name":"legacy","password":"LEGACY-PASS"}]}`,
			want:     "LEGACY-PASS",
		},
		{
			// shadowsocks 的凭证挂在 settings 顶层，没有 users 数组。
			name:     "shadowsocks top-level password",
			protocol: model.Shadowsocks,
			settings: `{"method":"2022-blake3-aes-128-gcm","password":"LEGACY-SS"}`,
			want:     "LEGACY-SS",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			link, err := BuildShareLink(ShareTarget{
				Inbound: inboundFor(tc.protocol, 1080, tc.settings),
				Address: "example.com",
				Remark:  "legacy",
			})
			if err != nil {
				t.Fatalf("build link: %v", err)
			}
			// vmess 与 shadowsocks 都把凭证藏在 base64 里，
			// 所以这里比对解码后的内容而不是链接原文。
			switch tc.protocol {
			case model.Shadowsocks:
				userInfo, _, _ := strings.Cut(strings.TrimPrefix(link, "ss://"), "@")
				decoded, err := base64.RawURLEncoding.DecodeString(userInfo)
				if err != nil {
					t.Fatalf("ss userinfo is not SIP002 base64url: %v", err)
				}
				if !strings.Contains(string(decoded), tc.want) {
					t.Errorf("ss userinfo = %q, want it to carry %q", decoded, tc.want)
				}
				return
			case model.VMess:
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
				if err != nil {
					t.Fatalf("vmess payload is not base64: %v", err)
				}
				if !strings.Contains(string(decoded), tc.want) {
					t.Errorf("vmess payload = %q, want it to carry %q", decoded, tc.want)
				}
				return
			}
			if !strings.Contains(link, tc.want) {
				t.Errorf("link %q does not carry the legacy credential %q", link, tc.want)
			}
		})
	}
}

func TestShadowsocksLinkIsSIP002(t *testing.T) {
	link, err := BuildShareLink(ShareTarget{
		Inbound: inboundFor(model.Shadowsocks, 8388, `{"method":"2022-blake3-aes-128-gcm"}`),
		Client:  &model.Client{Email: "alice", Password: "s3cret"},
		Address: "198.51.100.9",
		Remark:  "ss node",
	})
	if err != nil {
		t.Fatalf("build ss link: %v", err)
	}
	userInfo, rest, ok := strings.Cut(strings.TrimPrefix(link, "ss://"), "@")
	if !ok {
		t.Fatalf("ss link has no userinfo: %s", link)
	}
	// SIP002 要求 base64url 且不带 padding；带 '=' 的链接部分客户端会拒收。
	if strings.ContainsAny(userInfo, "=+/") {
		t.Errorf("ss userinfo %q is not unpadded base64url", userInfo)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(userInfo)
	if err != nil {
		t.Fatalf("decode ss userinfo: %v", err)
	}
	if string(decoded) != "2022-blake3-aes-128-gcm:s3cret" {
		t.Errorf("ss userinfo = %q, want method:password", decoded)
	}
	if !strings.HasPrefix(rest, "198.51.100.9:8388") {
		t.Errorf("ss host = %q, want the inbound address and port", rest)
	}
}

func TestTUICLinkCarriesBothCredentials(t *testing.T) {
	link, err := BuildShareLink(ShareTarget{
		Inbound: inboundFor(model.TUIC, 2053,
			`{"congestion_control":"bbr","tls":{"enabled":true,"server_name":"tuic.example.com"}}`),
		Client:  &model.Client{Email: "alice", UUID: "TUIC-UUID", Password: "TUIC-PASS"},
		Address: "203.0.113.7",
		Remark:  "tuic",
	})
	if err != nil {
		t.Fatalf("build tuic link: %v", err)
	}
	u := parseLink(t, link)
	if u.User == nil || u.User.Username() != "TUIC-UUID" {
		t.Errorf("tuic userinfo = %v, want the uuid", u.User)
	}
	password, _ := u.User.Password()
	if password != "TUIC-PASS" {
		t.Errorf("tuic password = %q, want the client password", password)
	}
	if got := u.Query().Get("congestion_control"); got != "bbr" {
		t.Errorf("congestion_control = %q, want bbr", got)
	}
}

func TestProxyLinksCarryUserinfo(t *testing.T) {
	for _, tc := range []struct {
		protocol model.Protocol
		settings string
		scheme   string
	}{
		{model.Socks, `{}`, "socks"},
		{model.HTTP, `{}`, "http"},
		{model.HTTP, `{"tls":{"enabled":true}}`, "https"},
	} {
		t.Run(string(tc.protocol)+"/"+tc.scheme, func(t *testing.T) {
			link, err := BuildShareLink(ShareTarget{
				Inbound: inboundFor(tc.protocol, 1080, tc.settings),
				Client:  &model.Client{Email: "alice", Username: "u ser", Password: "p@ss"},
				Address: "198.51.100.9",
				Remark:  "proxy",
			})
			if err != nil {
				t.Fatalf("build link: %v", err)
			}
			u := parseLink(t, link)
			if u.Scheme != tc.scheme {
				t.Errorf("scheme = %q, want %q", u.Scheme, tc.scheme)
			}
			if u.User.Username() != "u ser" {
				t.Errorf("username = %q, want it percent-decoded back to %q", u.User.Username(), "u ser")
			}
			if password, _ := u.User.Password(); password != "p@ss" {
				t.Errorf("password = %q, want %q", password, "p@ss")
			}
		})
	}
}

// Shareable=false 的协议不能硬造一个 scheme：客户端解析失败会把整份订阅丢掉。
func TestUnshareableProtocolsAreRefused(t *testing.T) {
	for _, protocol := range []model.Protocol{model.AnyTLS, model.Naive, model.WireGuard, model.Direct, model.Mixed, model.ShadowTLS} {
		t.Run(string(protocol), func(t *testing.T) {
			if IsShareable(protocol) {
				t.Fatalf("%s is marked shareable; this test tracks the unshareable set", protocol)
			}
			if _, err := BuildShareLink(ShareTarget{Inbound: inboundFor(protocol, 1080, `{}`)}); !errors.Is(err, ErrNotShareable) {
				t.Errorf("BuildShareLink error = %v, want ErrNotShareable", err)
			}
			if _, err := BuildClashProxy(ShareTarget{Inbound: inboundFor(protocol, 1080, `{}`)}); !errors.Is(err, ErrNotShareable) {
				t.Errorf("BuildClashProxy error = %v, want ErrNotShareable", err)
			}
			if _, err := BuildSingBoxOutbound(ShareTarget{Inbound: inboundFor(protocol, 1080, `{}`)}); !errors.Is(err, ErrNotShareable) {
				t.Errorf("BuildSingBoxOutbound error = %v, want ErrNotShareable", err)
			}
		})
	}
}

func TestShareLinkRejectsMalformedSettings(t *testing.T) {
	if _, err := BuildShareLink(ShareTarget{Inbound: inboundFor(model.VMess, 443, `not json`)}); err == nil {
		t.Fatal("malformed settings produced a share link instead of an error")
	}
}

func TestClashProxyMapsRealityAndTransport(t *testing.T) {
	proxy, err := BuildClashProxy(ShareTarget{
		Inbound: inboundFor(model.VLESS, 8443,
			`{"tls":{"enabled":true,"server_name":"www.microsoft.com",
			         "reality":{"enabled":true,"private_key":"SERVER-ONLY","public_key":"PBK","short_id":["ab"]}},
			  "transport":{"type":"grpc","service_name":"grpcsvc"}}`),
		Client:  &model.Client{Email: "alice", UUID: "U", Extra: `{"flow":"xtls-rprx-vision"}`},
		Address: "203.0.113.7",
		Remark:  "reality",
	})
	if err != nil {
		t.Fatalf("build clash proxy: %v", err)
	}
	if proxy["type"] != "vless" || proxy["uuid"] != "U" {
		t.Errorf("proxy = %v, want a vless entry with the client uuid", proxy)
	}
	if proxy["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow = %v, want the value read out of the client extra JSON", proxy["flow"])
	}
	opts, ok := proxy["reality-opts"].(map[string]any)
	if !ok {
		t.Fatalf("reality-opts = %v, want a map", proxy["reality-opts"])
	}
	if opts["public-key"] != "PBK" || opts["short-id"] != "ab" {
		t.Errorf("reality-opts = %v, want the public key and short id", opts)
	}
	if proxy["network"] != "grpc" {
		t.Errorf("network = %v, want grpc", proxy["network"])
	}
	grpcOpts, ok := proxy["grpc-opts"].(map[string]any)
	if !ok || grpcOpts["grpc-service-name"] != "grpcsvc" {
		t.Errorf("grpc-opts = %v, want the service name", proxy["grpc-opts"])
	}
}

func TestClashWebSocketProxyCarriesTheHostHeader(t *testing.T) {
	proxy, err := BuildClashProxy(ShareTarget{
		Inbound: inboundFor(model.VMess, 443,
			`{"transport":{"type":"ws","path":"/ray","host":["cdn.example.com"]}}`),
		Client:  &model.Client{Email: "alice", UUID: "U"},
		Address: "203.0.113.7",
		Remark:  "ws",
	})
	if err != nil {
		t.Fatalf("build clash proxy: %v", err)
	}
	opts, ok := proxy["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("ws-opts = %v, want a map", proxy["ws-opts"])
	}
	if opts["path"] != "/ray" {
		t.Errorf("ws path = %v, want /ray", opts["path"])
	}
	headers, ok := opts["headers"].(map[string]any)
	if !ok || headers["Host"] != "cdn.example.com" {
		t.Errorf("ws headers = %v, want the Host header", opts["headers"])
	}
}

// 出站配置里出现 private_key 就是把服务端私钥发给了每一个订阅用户。
func TestSingBoxOutboundNeverLeaksTheServerPrivateKey(t *testing.T) {
	out, err := BuildSingBoxOutbound(ShareTarget{
		Inbound: inboundFor(model.VLESS, 8443,
			`{"tls":{"enabled":true,"server_name":"www.microsoft.com","alpn":["h2"],
			         "reality":{"enabled":true,"private_key":"SERVER-ONLY","public_key":"PBK","short_id":["ab"]}}}`),
		Client:  &model.Client{Email: "alice", UUID: "U"},
		Address: "203.0.113.7",
		Remark:  "reality",
	})
	if err != nil {
		t.Fatalf("build outbound: %v", err)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal outbound: %v", err)
	}
	if strings.Contains(string(encoded), "SERVER-ONLY") || strings.Contains(string(encoded), "private_key") {
		t.Fatalf("outbound leaks the server private key: %s", encoded)
	}

	tls, ok := out["tls"].(map[string]any)
	if !ok {
		t.Fatalf("tls = %v, want a map", out["tls"])
	}
	reality, ok := tls["reality"].(map[string]any)
	if !ok || reality["public_key"] != "PBK" {
		t.Errorf("reality = %v, want the client-side public key", tls["reality"])
	}
	if _, ok := tls["utls"]; !ok {
		t.Error("a Reality outbound without utls will be fingerprinted immediately")
	}
}

func TestSingBoxOutboundCoversEveryShareableProtocol(t *testing.T) {
	cases := []struct {
		protocol model.Protocol
		settings string
		client   *model.Client
		wantKeys []string
	}{
		{model.VMess, `{}`, &model.Client{Email: "a", UUID: "U"}, []string{"uuid", "security"}},
		{model.VLESS, `{}`, &model.Client{Email: "a", UUID: "U"}, []string{"uuid"}},
		{model.Trojan, `{}`, &model.Client{Email: "a", Password: "P"}, []string{"password"}},
		{model.Shadowsocks, `{"method":"aes-128-gcm"}`, &model.Client{Email: "a", Password: "P"}, []string{"method", "password"}},
		{model.Hysteria2, `{"up_mbps":100,"down_mbps":200}`, &model.Client{Email: "a", Password: "P"}, []string{"password", "up_mbps", "down_mbps"}},
		{model.TUIC, `{}`, &model.Client{Email: "a", UUID: "U", Password: "P"}, []string{"uuid", "password"}},
		{model.Socks, `{}`, &model.Client{Email: "a", Username: "u", Password: "P"}, []string{"version", "username", "password"}},
		{model.HTTP, `{}`, &model.Client{Email: "a", Username: "u", Password: "P"}, []string{"username", "password"}},
	}

	for _, tc := range cases {
		t.Run(string(tc.protocol), func(t *testing.T) {
			out, err := BuildSingBoxOutbound(ShareTarget{
				Inbound: inboundFor(tc.protocol, 443, tc.settings),
				Client:  tc.client,
				Address: "203.0.113.7",
				Remark:  "node",
			})
			if err != nil {
				t.Fatalf("build outbound: %v", err)
			}
			if out["type"] != string(tc.protocol) {
				t.Errorf("type = %v, want %s", out["type"], tc.protocol)
			}
			for _, key := range tc.wantKeys {
				if _, ok := out[key]; !ok {
					t.Errorf("outbound is missing %q: %v", key, out)
				}
			}
		})
	}
}
