package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"x-ui/core/singbox/spec"
)

// Client 是挂在某条入站下的一个终端用户（多用户能力的持久化模型）。
//
// 设计取舍：
//
//   - 凭证用三个定型列（UUID / Password / Username）而不是一整块 JSON。
//     spec.UserSchema 已经把 14 个协议的凭证形态收敛成
//     identifier + credentials 两类字段名，而这些字段名的全集恰好就是
//     uuid / password / username 三个。定型列让"按 email 查、按配额筛"
//     这类查询保持可索引，也让迁移时能一眼看出存了什么。
//
//   - Extra 承载协议私有的非凭证字段（VLESS 的 flow、VMess 的 alterId 等），
//     以 JSON 对象透传进生成的用户条目，避免每加一个协议字段就动一次表结构。
//
//   - Email 全局唯一而不是"每条入站内唯一"。sing-box 的 v2ray_api 用户流量
//     计数器名是 user>>><name>>>>traffic>>><dir>，跨入站共用一个命名空间；
//     若两条入站各有一个 alice，两人的流量会被加到同一个计数器上，配额也就
//     跟着串了。
type Client struct {
	Id        int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	InboundId int    `json:"inboundId" form:"inboundId" gorm:"index"`
	Email     string `json:"email" form:"email" gorm:"uniqueIndex"`
	Enable    bool   `json:"enable" form:"enable"`

	Up    int64 `json:"up" form:"up"`
	Down  int64 `json:"down" form:"down"`
	Total int64 `json:"total" form:"total"` // 配额字节数，0 表示不限

	ExpiryTime int64 `json:"expiryTime" form:"expiryTime"` // 毫秒时间戳，0 表示永不过期

	UUID     string `json:"uuid" form:"uuid"`
	Password string `json:"password" form:"password"`
	Username string `json:"username" form:"username"`

	// Extra 是协议私有附加字段的 JSON 对象，可为空。
	Extra string `json:"extra" form:"extra"`

	// SubToken 是订阅 URL 里的密钥，CSPRNG 生成且全局唯一。
	// 它就是订阅接口的全部凭证，因此绝不能出现在日志或审计明文里。
	SubToken string `json:"subToken" gorm:"uniqueIndex"`

	// LastSeen 是最近一次观测到该用户产生流量的毫秒时间戳，0 表示从未。
	LastSeen int64 `json:"lastSeen"`
}

// credentialFieldNames 是 UserSchema 可能用到的凭证字段名全集。
// registry_test 会校验注册表没有引入这三个之外的字段名。
var credentialFieldNames = []string{"uuid", "password", "username"}

// credential 按字段名读取客户端的对应凭证列。
func (c *Client) credential(field string) string {
	switch field {
	case "uuid":
		return c.UUID
	case "password":
		return c.Password
	case "username":
		return c.Username
	}
	return ""
}

// IsActive 判断该客户端此刻是否应当出现在 sing-box 配置里。
//
// now 为毫秒时间戳。三条独立的失效原因（手动禁用、配额耗尽、到期）
// 任意一条成立就不下发——过期用户留在配置里等于配额形同虚设。
func (c *Client) IsActive(now int64) bool {
	if !c.Enable {
		return false
	}
	if c.Total > 0 && c.Up+c.Down >= c.Total {
		return false
	}
	if c.ExpiryTime > 0 && c.ExpiryTime <= now {
		return false
	}
	return true
}

// ErrProtocolHasNoUsers 表示该协议没有用户维度（direct / wireguard）。
var ErrProtocolHasNoUsers = fmt.Errorf("protocol has no user dimension")

// ValidateClientForProtocol 校验一个客户端能否挂到给定协议下。
//
// 校验点：
//  1. 协议必须有用户维度，否则 direct / wireguard 会被塞进无意义的用户条目；
//  2. UserSchema 声明的 identifier 与 credentials 必须都有值，
//     缺一个 sing-box 就会拒绝整份配置，连带打挂其它入站；
//  3. Extra 必须是 JSON 对象，且不得覆盖凭证字段或 name。
func ValidateClientForProtocol(protocol Protocol, c *Client) error {
	s, ok := spec.Get(string(protocol))
	if !ok {
		return fmt.Errorf("unsupported protocol %q", protocol)
	}
	if !s.HasUsers() {
		return fmt.Errorf("%w: %s", ErrProtocolHasNoUsers, protocol)
	}
	if c == nil {
		return fmt.Errorf("empty client")
	}
	if strings.TrimSpace(c.Email) == "" {
		return fmt.Errorf("email is required and is used as the sing-box user name")
	}

	required := append([]string{s.Users.Identifier}, s.Users.Credentials...)
	for _, field := range required {
		if c.credential(field) == "" {
			return fmt.Errorf("protocol %s requires the client field %q", protocol, field)
		}
	}
	return validateClientExtra(c.Extra, required)
}

// validateClientExtra 校验 Extra 的形状并挡住会覆盖凭证的键。
func validateClientExtra(raw string, reserved []string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return fmt.Errorf("extra must be a JSON object: %w", err)
	}
	blocked := append([]string{"name"}, reserved...)
	var clashes []string
	for _, key := range blocked {
		if _, exists := probe[key]; exists {
			clashes = append(clashes, key)
		}
	}
	if len(clashes) > 0 {
		sort.Strings(clashes)
		return fmt.Errorf("extra must not override %s", strings.Join(clashes, ", "))
	}
	return nil
}

// userEntry 把一个客户端渲染成 sing-box 用户条目。
//
// name 固定取 Email：sing-box 的按用户流量计数器就是以它命名的
// （user>>><name>>>>traffic>>><dir>），也正是配额的记账键。
func userEntry(s spec.Spec, c *Client) (map[string]json.RawMessage, error) {
	entry := map[string]json.RawMessage{}
	if extra := strings.TrimSpace(c.Extra); extra != "" && extra != "null" {
		if err := json.Unmarshal([]byte(extra), &entry); err != nil {
			return nil, fmt.Errorf("client %q has invalid extra JSON: %w", c.Email, err)
		}
	}
	name, err := json.Marshal(c.Email)
	if err != nil {
		return nil, err
	}
	entry["name"] = name

	for _, field := range append([]string{s.Users.Identifier}, s.Users.Credentials...) {
		value, err := json.Marshal(c.credential(field))
		if err != nil {
			return nil, err
		}
		entry[field] = value
	}
	return entry, nil
}

// ApplyClients 把一组客户端展开进入站的协议私有 settings。
//
// 返回值是新的 settings JSON 字符串；原始 settings 中除用户字段之外的内容
// （tls / transport / masquerade …）原样保留。
//
// 语义按 spec.UserSchema 分三种：
//
//	Container 非空  → settings.<Container> 被整体替换为客户端数组
//	Container 为空  → 凭证写在 settings 顶层（目前只有 shadowsocks），
//	                  因而只能承载一个客户端；多于一个时报错而不是静默丢弃
//	无用户维度      → 原样返回（direct / wireguard）
//
// clients 为空时同样原样返回：这是历史数据的迁移路径——那些入站的凭证只存在
// 于 settings 里，clients 表是空的，必须继续按原样下发。
func ApplyClients(protocol Protocol, settings string, clients []*Client) (string, error) {
	s, ok := spec.Get(string(protocol))
	if !ok {
		return "", fmt.Errorf("unsupported protocol %q", protocol)
	}
	if len(clients) == 0 || !s.HasUsers() {
		return settings, nil
	}

	base := map[string]json.RawMessage{}
	if trimmed := strings.TrimSpace(settings); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal([]byte(trimmed), &base); err != nil {
			return "", fmt.Errorf("inbound settings are not a JSON object: %w", err)
		}
	}

	if s.Users.Container == "" {
		// shadowsocks：单份凭证挂在顶层，没有 name 字段可放，
		// 因此这种协议拿不到按用户的流量计数（见 ClientService 的注释）。
		if len(clients) > 1 {
			return "", fmt.Errorf("protocol %s stores a single credential at the settings top level and cannot host %d clients",
				protocol, len(clients))
		}
		c := clients[0]
		for _, field := range append([]string{s.Users.Identifier}, s.Users.Credentials...) {
			value, err := json.Marshal(c.credential(field))
			if err != nil {
				return "", err
			}
			base[field] = value
		}
	} else {
		entries := make([]map[string]json.RawMessage, 0, len(clients))
		for _, c := range clients {
			entry, err := userEntry(s, c)
			if err != nil {
				return "", err
			}
			entries = append(entries, entry)
		}
		encoded, err := json.Marshal(entries)
		if err != nil {
			return "", err
		}
		base[s.Users.Container] = encoded
	}

	out, err := json.Marshal(base)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// StatsUserNames 返回应当出现在 sing-box
// experimental.v2ray_api.stats.users 里的用户名。
//
// 只有 Container 非空的协议才会给用户条目写 name，也只有那些用户才会被
// sing-box 计数；shadowsocks 这类顶层凭证协议不在此列，其流量只能落到
// 入站维度上。
func StatsUserNames(protocol Protocol, clients []*Client) []string {
	s, ok := spec.Get(string(protocol))
	if !ok || !s.HasUsers() || s.Users.Container == "" {
		return nil
	}
	names := make([]string, 0, len(clients))
	for _, c := range clients {
		if c.Email != "" {
			names = append(names, c.Email)
		}
	}
	return names
}
