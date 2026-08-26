package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"x-ui/core/singbox/spec"
	"x-ui/database/model"
)

// 服务端分享链接生成。
//
// 为什么要在后端重做一份：订阅接口（GET sub/<token>）没有浏览器参与，
// 不可能复用前端 core.js 的 genLink。把这段逻辑放到 Go 侧还有两个附带好处——
// 它可以被单元测试覆盖，而且面板与订阅两条路径从此共用同一个实现，
// 不会出现"网页上复制的链接能连、订阅里的连不上"这种最难排查的分歧。

// ShareTarget 描述一次分享链接生成所需的全部上下文。
type ShareTarget struct {
	Inbound *model.Inbound
	// Client 为 nil 时凭证取自入站 settings 里的第一个用户，
	// 这正是"还没建客户端的老入站"的情形。
	Client *model.Client
	// Address 是客户端要连的服务器地址。TLS 场景下若配了 server_name，
	// 生成时会优先用 server_name（证书就是按它签的）。
	Address string
	Remark  string
}

// shareSettings 是分享链接需要的入站 settings 字段子集。
// 只声明用得到的字段：其余内容对链接没有影响，多解析一份只会增加出错面。
type shareSettings struct {
	Method            string           `json:"method"`
	Password          string           `json:"password"`
	UpMbps            int              `json:"up_mbps"`
	DownMbps          int              `json:"down_mbps"`
	CongestionControl string           `json:"congestion_control"`
	TLS               *shareTLS        `json:"tls"`
	Transport         *shareTransport  `json:"transport"`
	Users             []map[string]any `json:"users"`
}

type shareTLS struct {
	Enabled    bool          `json:"enabled"`
	ServerName string        `json:"server_name"`
	ALPN       []string      `json:"alpn"`
	Reality    *shareReality `json:"reality"`
}

type shareReality struct {
	Enabled   bool     `json:"enabled"`
	PublicKey string   `json:"public_key"`
	ShortID   []string `json:"short_id"`
}

type shareTransport struct {
	Type        string   `json:"type"`
	Path        string   `json:"path"`
	Host        []string `json:"host"`
	ServiceName string   `json:"service_name"`
}

func parseShareSettings(raw string) (*shareSettings, error) {
	s := &shareSettings{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return s, nil
	}
	if err := json.Unmarshal([]byte(trimmed), s); err != nil {
		return nil, fmt.Errorf("inbound settings are not usable for a share link: %w", err)
	}
	return s, nil
}

func (s *shareSettings) tlsEnabled() bool { return s.TLS != nil && s.TLS.Enabled }

func (s *shareSettings) serverName() string {
	if s.TLS == nil {
		return ""
	}
	return s.TLS.ServerName
}

func (s *shareSettings) realityOn() bool {
	return s.tlsEnabled() && s.TLS.Reality != nil && s.TLS.Reality.Enabled
}

func (s *shareSettings) transportType() string {
	if s.Transport == nil {
		return ""
	}
	return s.Transport.Type
}

func (s *shareSettings) firstHost() string {
	if s.Transport == nil || len(s.Transport.Host) == 0 {
		return ""
	}
	return s.Transport.Host[0]
}

// credential 按 UserSchema 定位凭证：优先取客户端，其次回落到 settings。
//
// 回落这条路径不是可有可无的兼容代码——面板升级前建的入站，凭证只存在于
// settings.users[0] 里，clients 表是空的。
func (t ShareTarget) credential(s *shareSettings, field string) string {
	if t.Client != nil {
		switch field {
		case "uuid":
			return t.Client.UUID
		case "password":
			return t.Client.Password
		case "username":
			return t.Client.Username
		}
		return ""
	}
	sp, ok := spec.Get(string(t.Inbound.Protocol))
	if !ok {
		return ""
	}
	if sp.Users.Container == "" {
		if field == "password" {
			return s.Password
		}
		return ""
	}
	if len(s.Users) == 0 {
		return ""
	}
	if v, ok := s.Users[0][field].(string); ok {
		return v
	}
	return ""
}

// userExtra 读取用户条目上的协议私有字段（例如 VLESS 的 flow）。
func (t ShareTarget) userExtra(s *shareSettings, field string) string {
	if t.Client != nil {
		if strings.TrimSpace(t.Client.Extra) == "" {
			return ""
		}
		var extra map[string]any
		if err := json.Unmarshal([]byte(t.Client.Extra), &extra); err != nil {
			return ""
		}
		if v, ok := extra[field].(string); ok {
			return v
		}
		return ""
	}
	if len(s.Users) == 0 {
		return ""
	}
	if v, ok := s.Users[0][field].(string); ok {
		return v
	}
	return ""
}

// dialAddress 返回链接里该写的主机名。
// 启用 TLS 且配了 server_name 时用后者：证书按它签发，直连 IP 会握手失败。
func (t ShareTarget) dialAddress(s *shareSettings) string {
	if s.tlsEnabled() && s.serverName() != "" {
		return s.serverName()
	}
	return t.Address
}

// IsShareable 报告该协议是否有标准分享 URL。
func IsShareable(protocol model.Protocol) bool {
	sp, ok := spec.Get(string(protocol))
	return ok && sp.Shareable
}

// ErrNotShareable 表示该协议没有可生成的分享 URL。
var ErrNotShareable = fmt.Errorf("protocol has no standard share URL")

// BuildShareLink 生成 v2rayN 风格的单条分享 URL。
func BuildShareLink(t ShareTarget) (string, error) {
	if t.Inbound == nil {
		return "", fmt.Errorf("no inbound")
	}
	if !IsShareable(t.Inbound.Protocol) {
		return "", fmt.Errorf("%w: %s", ErrNotShareable, t.Inbound.Protocol)
	}
	s, err := parseShareSettings(t.Inbound.Settings)
	if err != nil {
		return "", err
	}

	switch t.Inbound.Protocol {
	case model.VMess:
		return t.vmessLink(s)
	case model.VLESS:
		return t.vlessLink(s), nil
	case model.Trojan:
		return t.trojanLink(s), nil
	case model.Shadowsocks:
		return t.shadowsocksLink(s), nil
	case model.Hysteria2:
		return t.hysteria2Link(s), nil
	case model.TUIC:
		return t.tuicLink(s), nil
	case model.Socks:
		return t.proxyLink(s, "socks"), nil
	case model.HTTP:
		scheme := "http"
		if s.tlsEnabled() {
			scheme = "https"
		}
		return t.proxyLink(s, scheme), nil
	}
	return "", fmt.Errorf("%w: %s", ErrNotShareable, t.Inbound.Protocol)
}

// vmessLink 生成 vmess:// 链接。
// 载荷是 v2rayN 约定的 JSON，字段名与顺序都是事实标准，不能自行简化。
func (t ShareTarget) vmessLink(s *shareSettings) (string, error) {
	network := s.transportType()
	if network == "" {
		network = "tcp"
	}
	path := ""
	if s.Transport != nil {
		path = s.Transport.Path
	}
	tls := ""
	if s.tlsEnabled() {
		tls = "tls"
	}
	payload := map[string]any{
		"v":    "2",
		"ps":   t.Remark,
		"add":  t.dialAddress(s),
		"port": strconv.Itoa(t.Inbound.Port),
		"id":   t.credential(s, "uuid"),
		"aid":  "0",
		"net":  network,
		"type": "none",
		"host": s.firstHost(),
		"path": path,
		"tls":  tls,
		"sni":  s.serverName(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(encoded), nil
}

func (t ShareTarget) vlessLink(s *shareSettings) string {
	params := url.Values{}
	network := s.transportType()
	if network == "" {
		network = "tcp"
	}
	params.Set("type", network)
	switch {
	case s.realityOn():
		params.Set("security", "reality")
		// pbk 是 Reality 握手的必要参数；缺了它客户端一律连不上，
		// 所以 ValidateInbound 在保存阶段就强制要求 public_key 存在。
		params.Set("pbk", s.TLS.Reality.PublicKey)
		if len(s.TLS.Reality.ShortID) > 0 {
			params.Set("sid", s.TLS.Reality.ShortID[0])
		}
		if s.serverName() != "" {
			params.Set("sni", s.serverName())
		}
	case s.tlsEnabled():
		params.Set("security", "tls")
		if s.serverName() != "" {
			params.Set("sni", s.serverName())
		}
		if len(s.TLS.ALPN) > 0 {
			params.Set("alpn", strings.Join(s.TLS.ALPN, ","))
		}
	default:
		params.Set("security", "none")
	}
	if flow := t.userExtra(s, "flow"); flow != "" {
		params.Set("flow", flow)
	}
	if s.Transport != nil {
		if s.Transport.Path != "" {
			params.Set("path", s.Transport.Path)
		}
		if len(s.Transport.Host) > 0 {
			params.Set("host", strings.Join(s.Transport.Host, ","))
		}
		if s.Transport.ServiceName != "" {
			params.Set("serviceName", s.Transport.ServiceName)
		}
	}
	return fmt.Sprintf("vless://%s@%s?%s#%s",
		t.credential(s, "uuid"),
		net.JoinHostPort(t.dialAddress(s), strconv.Itoa(t.Inbound.Port)),
		params.Encode(),
		url.PathEscape(t.Remark))
}

func (t ShareTarget) trojanLink(s *shareSettings) string {
	params := url.Values{}
	if s.serverName() != "" {
		params.Set("sni", s.serverName())
	}
	if typ := s.transportType(); typ != "" {
		params.Set("type", typ)
	}
	return "trojan://" + escapeUserinfo(t.credential(s, "password")) + "@" +
		net.JoinHostPort(t.dialAddress(s), strconv.Itoa(t.Inbound.Port)) +
		querySuffix(params) + "#" + url.PathEscape(t.Remark)
}

// shadowsocksLink 生成 SIP002 形式的 ss:// 链接。
// userinfo 用 base64url 且不带 padding，这是 SIP002 的硬性要求。
func (t ShareTarget) shadowsocksLink(s *shareSettings) string {
	userInfo := base64.RawURLEncoding.EncodeToString([]byte(s.Method + ":" + t.credential(s, "password")))
	return "ss://" + userInfo + "@" +
		net.JoinHostPort(t.Address, strconv.Itoa(t.Inbound.Port)) +
		"#" + url.PathEscape(t.Remark)
}

func (t ShareTarget) hysteria2Link(s *shareSettings) string {
	params := url.Values{}
	if s.serverName() != "" {
		params.Set("sni", s.serverName())
	}
	if s.UpMbps > 0 {
		params.Set("up", strconv.Itoa(s.UpMbps))
	}
	if s.DownMbps > 0 {
		params.Set("down", strconv.Itoa(s.DownMbps))
	}
	return "hysteria2://" + escapeUserinfo(t.credential(s, "password")) + "@" +
		net.JoinHostPort(t.dialAddress(s), strconv.Itoa(t.Inbound.Port)) +
		querySuffix(params) + "#" + url.PathEscape(t.Remark)
}

func (t ShareTarget) tuicLink(s *shareSettings) string {
	params := url.Values{}
	if s.CongestionControl != "" {
		params.Set("congestion_control", s.CongestionControl)
	}
	if s.serverName() != "" {
		params.Set("sni", s.serverName())
	}
	params.Set("alpn", "h3")
	userInfo := escapeUserinfo(t.credential(s, "uuid")) + ":" + escapeUserinfo(t.credential(s, "password"))
	return "tuic://" + userInfo + "@" +
		net.JoinHostPort(t.dialAddress(s), strconv.Itoa(t.Inbound.Port)) +
		querySuffix(params) + "#" + url.PathEscape(t.Remark)
}

// proxyLink 生成 socks:// 与 http(s):// 这类带 userinfo 的代理 URL。
func (t ShareTarget) proxyLink(s *shareSettings, scheme string) string {
	auth := ""
	if username := t.credential(s, "username"); username != "" {
		auth = escapeUserinfo(username)
		if password := t.credential(s, "password"); password != "" {
			auth += ":" + escapeUserinfo(password)
		}
		auth += "@"
	}
	return scheme + "://" + auth +
		net.JoinHostPort(t.dialAddress(s), strconv.Itoa(t.Inbound.Port)) +
		"#" + url.PathEscape(t.Remark)
}

// escapeUserinfo 按 RFC 3986 的 userinfo 规则转义凭证。
//
// 不能用 url.QueryEscape：它是查询串的规则，会把空格写成 '+'，
// 而 userinfo 里的 '+' 是字面量，客户端会连着加号一起当成密码。
func escapeUserinfo(raw string) string {
	if raw == "" {
		return ""
	}
	// url.User 内部就走 userinfo 的转义表，比自己维护一份安全字符集可靠。
	return url.User(raw).String()
}

func querySuffix(params url.Values) string {
	if len(params) == 0 {
		return ""
	}
	return "?" + params.Encode()
}

// BuildClashProxy 生成 Clash 配置里的一个 proxies 条目。
func BuildClashProxy(t ShareTarget) (map[string]any, error) {
	if t.Inbound == nil {
		return nil, fmt.Errorf("no inbound")
	}
	if !IsShareable(t.Inbound.Protocol) {
		return nil, fmt.Errorf("%w: %s", ErrNotShareable, t.Inbound.Protocol)
	}
	s, err := parseShareSettings(t.Inbound.Settings)
	if err != nil {
		return nil, err
	}

	proxy := map[string]any{
		"name":   t.Remark,
		"server": t.dialAddress(s),
		"port":   t.Inbound.Port,
	}
	switch t.Inbound.Protocol {
	case model.VMess:
		proxy["type"] = "vmess"
		proxy["uuid"] = t.credential(s, "uuid")
		proxy["alterId"] = 0
		proxy["cipher"] = "auto"
		proxy["tls"] = s.tlsEnabled()
		if s.serverName() != "" {
			proxy["servername"] = s.serverName()
		}
		applyClashTransport(proxy, s)
	case model.VLESS:
		proxy["type"] = "vless"
		proxy["uuid"] = t.credential(s, "uuid")
		proxy["tls"] = s.tlsEnabled()
		if s.serverName() != "" {
			proxy["servername"] = s.serverName()
		}
		if flow := t.userExtra(s, "flow"); flow != "" {
			proxy["flow"] = flow
		}
		if s.realityOn() {
			opts := map[string]any{"public-key": s.TLS.Reality.PublicKey}
			if len(s.TLS.Reality.ShortID) > 0 {
				opts["short-id"] = s.TLS.Reality.ShortID[0]
			}
			proxy["reality-opts"] = opts
			proxy["client-fingerprint"] = "chrome"
		}
		applyClashTransport(proxy, s)
	case model.Trojan:
		proxy["type"] = "trojan"
		proxy["password"] = t.credential(s, "password")
		if s.serverName() != "" {
			proxy["sni"] = s.serverName()
		}
	case model.Shadowsocks:
		proxy["type"] = "ss"
		proxy["server"] = t.Address
		proxy["cipher"] = s.Method
		proxy["password"] = t.credential(s, "password")
	case model.Hysteria2:
		proxy["type"] = "hysteria2"
		proxy["password"] = t.credential(s, "password")
		if s.serverName() != "" {
			proxy["sni"] = s.serverName()
		}
		if s.UpMbps > 0 {
			proxy["up"] = s.UpMbps
		}
		if s.DownMbps > 0 {
			proxy["down"] = s.DownMbps
		}
	case model.TUIC:
		proxy["type"] = "tuic"
		proxy["uuid"] = t.credential(s, "uuid")
		proxy["password"] = t.credential(s, "password")
		proxy["alpn"] = []string{"h3"}
		if s.CongestionControl != "" {
			proxy["congestion-controller"] = s.CongestionControl
		}
		if s.serverName() != "" {
			proxy["sni"] = s.serverName()
		}
	case model.Socks:
		proxy["type"] = "socks5"
		applyClashUserPass(proxy, t, s)
	case model.HTTP:
		proxy["type"] = "http"
		proxy["tls"] = s.tlsEnabled()
		applyClashUserPass(proxy, t, s)
	}
	return proxy, nil
}

func applyClashUserPass(proxy map[string]any, t ShareTarget, s *shareSettings) {
	if username := t.credential(s, "username"); username != "" {
		proxy["username"] = username
	}
	if password := t.credential(s, "password"); password != "" {
		proxy["password"] = password
	}
}

func applyClashTransport(proxy map[string]any, s *shareSettings) {
	if s.Transport == nil || s.Transport.Type == "" {
		proxy["network"] = "tcp"
		return
	}
	switch s.Transport.Type {
	case "ws":
		proxy["network"] = "ws"
		opts := map[string]any{"path": s.Transport.Path}
		if host := s.firstHost(); host != "" {
			opts["headers"] = map[string]any{"Host": host}
		}
		proxy["ws-opts"] = opts
	case "grpc":
		proxy["network"] = "grpc"
		proxy["grpc-opts"] = map[string]any{"grpc-service-name": s.Transport.ServiceName}
	case "http":
		proxy["network"] = "http"
	default:
		proxy["network"] = s.Transport.Type
	}
}

// BuildSingBoxOutbound 生成 sing-box 客户端配置里的一个 outbound 条目。
func BuildSingBoxOutbound(t ShareTarget) (map[string]any, error) {
	if t.Inbound == nil {
		return nil, fmt.Errorf("no inbound")
	}
	if !IsShareable(t.Inbound.Protocol) {
		return nil, fmt.Errorf("%w: %s", ErrNotShareable, t.Inbound.Protocol)
	}
	s, err := parseShareSettings(t.Inbound.Settings)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"type":        string(t.Inbound.Protocol),
		"tag":         t.Remark,
		"server":      t.dialAddress(s),
		"server_port": t.Inbound.Port,
	}
	switch t.Inbound.Protocol {
	case model.VMess:
		out["uuid"] = t.credential(s, "uuid")
		out["security"] = "auto"
	case model.VLESS:
		out["uuid"] = t.credential(s, "uuid")
		if flow := t.userExtra(s, "flow"); flow != "" {
			out["flow"] = flow
		}
	case model.Trojan, model.Hysteria2:
		out["password"] = t.credential(s, "password")
	case model.Shadowsocks:
		out["server"] = t.Address
		out["method"] = s.Method
		out["password"] = t.credential(s, "password")
	case model.TUIC:
		out["uuid"] = t.credential(s, "uuid")
		out["password"] = t.credential(s, "password")
		if s.CongestionControl != "" {
			out["congestion_control"] = s.CongestionControl
		}
	case model.Socks:
		out["version"] = "5"
		applySingBoxUserPass(out, t, s)
	case model.HTTP:
		applySingBoxUserPass(out, t, s)
	}

	if s.Inbound2OutboundTLS() != nil {
		out["tls"] = s.Inbound2OutboundTLS()
	}
	if transport := s.outboundTransport(); transport != nil {
		out["transport"] = transport
	}
	if t.Inbound.Protocol == model.Hysteria2 {
		if s.UpMbps > 0 {
			out["up_mbps"] = s.UpMbps
		}
		if s.DownMbps > 0 {
			out["down_mbps"] = s.DownMbps
		}
	}
	return out, nil
}

func applySingBoxUserPass(out map[string]any, t ShareTarget, s *shareSettings) {
	if username := t.credential(s, "username"); username != "" {
		out["username"] = username
	}
	if password := t.credential(s, "password"); password != "" {
		out["password"] = password
	}
}

// Inbound2OutboundTLS 把入站的 TLS 块翻译成客户端出站需要的形态。
//
// 关键差异：服务端持有 private_key，客户端持有 public_key。
// 直接把入站 tls 原样拷过去会把服务端私钥发给所有订阅用户。
func (s *shareSettings) Inbound2OutboundTLS() map[string]any {
	if !s.tlsEnabled() {
		return nil
	}
	tls := map[string]any{"enabled": true}
	if s.serverName() != "" {
		tls["server_name"] = s.serverName()
	}
	if len(s.TLS.ALPN) > 0 {
		tls["alpn"] = s.TLS.ALPN
	}
	if s.realityOn() {
		reality := map[string]any{
			"enabled":    true,
			"public_key": s.TLS.Reality.PublicKey,
		}
		if len(s.TLS.Reality.ShortID) > 0 {
			reality["short_id"] = s.TLS.Reality.ShortID[0]
		}
		tls["reality"] = reality
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
	}
	return tls
}

func (s *shareSettings) outboundTransport() map[string]any {
	if s.Transport == nil || s.Transport.Type == "" {
		return nil
	}
	transport := map[string]any{"type": s.Transport.Type}
	switch s.Transport.Type {
	case "ws", "httpupgrade":
		transport["path"] = s.Transport.Path
		if host := s.firstHost(); host != "" {
			transport["headers"] = map[string]any{"Host": host}
		}
	case "grpc":
		transport["service_name"] = s.Transport.ServiceName
	case "http":
		if s.Transport.Path != "" {
			transport["path"] = s.Transport.Path
		}
		if len(s.Transport.Host) > 0 {
			transport["host"] = s.Transport.Host
		}
	}
	return transport
}
