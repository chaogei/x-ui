package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"x-ui/database/model"
)

// 订阅接口的服务端实现。
//
// 鉴权模型：URL 里的 sub_token 就是全部凭证，没有 session、没有 CSRF。
// 因此这里的每一个"找不到 / 不可用"分支都必须收敛成同一个 404：
// 区分"token 不存在"与"token 存在但用户被停用"会把 token 变成一个
// 可枚举的存在性预言机。

// SubFormat 是订阅输出格式。
type SubFormat string

const (
	// SubFormatV2rayN 是默认格式：整段 base64 的换行分隔链接列表。
	// 绝大多数客户端（v2rayN / v2rayNG / Shadowrocket…）都认这个。
	SubFormatV2rayN SubFormat = "v2rayn"
	// SubFormatClash 输出 Clash 的 proxies / proxy-groups YAML。
	SubFormatClash SubFormat = "clash"
	// SubFormatSingBox 输出 sing-box 客户端配置的 outbounds JSON。
	SubFormatSingBox SubFormat = "sing-box"
)

// ErrSubscriptionNotFound 是订阅接口唯一对外可见的失败原因。
var ErrSubscriptionNotFound = errors.New("subscription not found")

// ParseSubFormat 解析 ?format= 参数，未知/为空一律回落到默认格式。
//
// 不对未知格式报错是有意的：订阅链接常年躺在客户端里，
// 因为一个拼错的参数就让所有节点消失，比给出默认格式糟得多。
func ParseSubFormat(raw string) SubFormat {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "clash":
		return SubFormatClash
	case "sing-box", "singbox":
		return SubFormatSingBox
	default:
		return SubFormatV2rayN
	}
}

// Subscription 是一次订阅请求的渲染结果。
type Subscription struct {
	Body        string
	ContentType string
	// Filename 用于 Content-Disposition，方便客户端保存时有个可读的名字。
	Filename string
	// UserInfo 是 Subscription-Userinfo 头的内容（配额与到期），
	// 客户端据此显示剩余流量。空串表示不下发该头。
	UserInfo string
}

// SubscriptionService 按 token 渲染订阅内容。
type SubscriptionService struct {
	clientService  ClientService
	inboundService InboundService
}

// Render 按 token 查出客户端并渲染订阅。
//
// address 是客户端要连的服务器地址，由调用方从请求 Host 或面板设置推导。
func (s *SubscriptionService) Render(token string, format SubFormat, address string) (*Subscription, error) {
	client, err := s.clientService.GetClientBySubToken(token)
	if errors.Is(err, ErrClientNotFound) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	// 停用、超配额、已过期的用户拿到的也是 404：它的节点此刻不在内核配置里，
	// 返回一份连不上的链接只会让人以为是服务端坏了。
	if !client.IsActive(time.Now().UnixMilli()) {
		return nil, ErrSubscriptionNotFound
	}

	inbound, err := s.inboundService.GetInbound(client.InboundId)
	if err != nil {
		return nil, ErrSubscriptionNotFound
	}
	if !inbound.Enable {
		return nil, ErrSubscriptionNotFound
	}
	// 没有标准分享 URL 的协议（anytls / naive / wireguard …）不进订阅：
	// 硬造一个客户端读不懂的 scheme 只会让整份订阅解析失败。
	if !IsShareable(inbound.Protocol) {
		return nil, ErrSubscriptionNotFound
	}

	target := ShareTarget{
		Inbound: inbound,
		Client:  client,
		Address: address,
		Remark:  subscriptionRemark(inbound, client),
	}

	sub := &Subscription{UserInfo: subscriptionUserInfo(client)}
	switch format {
	case SubFormatClash:
		body, err := renderClash([]ShareTarget{target})
		if err != nil {
			return nil, err
		}
		sub.Body = body
		sub.ContentType = "text/yaml; charset=utf-8"
		sub.Filename = "clash.yaml"
	case SubFormatSingBox:
		body, err := renderSingBox([]ShareTarget{target})
		if err != nil {
			return nil, err
		}
		sub.Body = body
		sub.ContentType = "application/json; charset=utf-8"
		sub.Filename = "sing-box.json"
	default:
		body, err := renderV2rayN([]ShareTarget{target})
		if err != nil {
			return nil, err
		}
		sub.Body = body
		sub.ContentType = "text/plain; charset=utf-8"
		sub.Filename = "subscription.txt"
	}
	return sub, nil
}

// subscriptionRemark 是客户端节点列表里显示的名字。
// 用入站备注而不是 email：订阅内容会出现在用户的截图与剪贴板里。
func subscriptionRemark(inbound *model.Inbound, client *model.Client) string {
	if inbound.Remark != "" {
		return inbound.Remark
	}
	if client.Email != "" {
		return client.Email
	}
	return inbound.Tag
}

// subscriptionUserInfo 生成 Subscription-Userinfo 头。
//
// 格式是机场订阅的事实标准：upload=/download=/total=/expire=（秒级时间戳）。
// total=0 表示不限量，客户端会隐藏进度条。
func subscriptionUserInfo(c *model.Client) string {
	expire := int64(0)
	if c.ExpiryTime > 0 {
		expire = c.ExpiryTime / 1000
	}
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
		c.Up, c.Down, c.Total, expire)
}

// renderV2rayN 把链接列表整体 base64 —— 这正是客户端期待的形态。
func renderV2rayN(targets []ShareTarget) (string, error) {
	links := make([]string, 0, len(targets))
	for _, t := range targets {
		link, err := BuildShareLink(t)
		if errors.Is(err, ErrNotShareable) {
			continue
		}
		if err != nil {
			return "", err
		}
		links = append(links, link)
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n"))), nil
}

// renderClash 输出一份最小但可直接使用的 Clash 配置。
//
// 只给 proxies 而不给 proxy-groups 的话，多数客户端会把节点导入成
// 一个无法选中的列表，所以这里补上一个 select 组。
func renderClash(targets []ShareTarget) (string, error) {
	proxies := make([]map[string]any, 0, len(targets))
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		proxy, err := BuildClashProxy(t)
		if errors.Is(err, ErrNotShareable) {
			continue
		}
		if err != nil {
			return "", err
		}
		proxies = append(proxies, proxy)
		names = append(names, t.Remark)
	}

	doc := map[string]any{
		"proxies": proxies,
		"proxy-groups": []map[string]any{
			{"name": "PROXY", "type": "select", "proxies": names},
		},
		"rules": []string{"MATCH,PROXY"},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// renderSingBox 输出 sing-box 客户端配置的 outbounds 片段。
func renderSingBox(targets []ShareTarget) (string, error) {
	outbounds := make([]map[string]any, 0, len(targets)+2)
	tags := make([]string, 0, len(targets))
	for _, t := range targets {
		outbound, err := BuildSingBoxOutbound(t)
		if errors.Is(err, ErrNotShareable) {
			continue
		}
		if err != nil {
			return "", err
		}
		outbounds = append(outbounds, outbound)
		tags = append(tags, t.Remark)
	}
	outbounds = append(outbounds,
		map[string]any{"type": "selector", "tag": "proxy", "outbounds": tags},
		map[string]any{"type": "direct", "tag": "direct"},
	)

	doc := map[string]any{"outbounds": outbounds}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
