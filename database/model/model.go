package model

import (
	"x-ui/core/singbox"
	"x-ui/util/json_util"
)

// Protocol 枚举 sing-box 支持并由 x-ui 面板直接管理的协议类型。
//
// 取值与 sing-box 配置中 inbound/endpoint 的 "type" 字段保持一致，
// 便于直接序列化为 sing-box JSON。
type Protocol string

const (
	VMess       Protocol = "vmess"
	VLESS       Protocol = "vless"
	Trojan      Protocol = "trojan"
	Shadowsocks Protocol = "shadowsocks"

	Hysteria2 Protocol = "hysteria2"
	TUIC      Protocol = "tuic"
	AnyTLS    Protocol = "anytls"
	ShadowTLS Protocol = "shadowtls"
	Naive     Protocol = "naive"
	WireGuard Protocol = "wireguard"

	Socks Protocol = "socks"
	HTTP  Protocol = "http"
	Mixed Protocol = "mixed"

	Direct Protocol = "direct"
)

// IsEndpoint 判断协议应挂在 sing-box 的 endpoints 列表而非 inbounds。
// sing-box 1.11+ 将 WireGuard 等隧道协议从 inbound 迁移至 endpoint。
func (p Protocol) IsEndpoint() bool {
	return p == WireGuard
}

type User struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// Inbound 是 x-ui 面板中一个入站记录的持久化模型。
//
// 为兼容所有协议采用最小通用字段 + 协议私有 JSON 片段的方式：
//   - Listen/Port/Protocol/Tag 是与 sing-box schema 固定对应的通用字段
//   - Settings 存放与 sing-box type 对应的协议私有字段 JSON，
//     由前端严格按照 sing-box 文档构造
//   - Sniffing 仅在支持 sniff 的 TCP 协议下有意义，为 sing-box 顶层字段集
//     （sniff/sniff_override_destination/sniff_timeout/domain_strategy）
//
// Port 字段不再单独 unique；由应用层按协议 network 类型（TCP/UDP）分组校验，
// 允许 TCP + UDP 协议复用同一端口（sing-box 的合法用法）。
type Inbound struct {
	Id         int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	UserId     int    `json:"-"`
	Up         int64  `json:"up" form:"up"`
	Down       int64  `json:"down" form:"down"`
	Total      int64  `json:"total" form:"total"`
	Remark     string `json:"remark" form:"remark"`
	Enable     bool   `json:"enable" form:"enable"`
	ExpiryTime int64  `json:"expiryTime" form:"expiryTime"`

	Listen   string   `json:"listen" form:"listen"`
	Port     int      `json:"port" form:"port"`
	Protocol Protocol `json:"protocol" form:"protocol"`
	Settings string   `json:"settings" form:"settings"`
	Tag      string   `json:"tag" form:"tag" gorm:"unique"`
	Sniffing string   `json:"sniffing" form:"sniffing"`
}

// Network 返回协议对应的传输层网络类型。
// 返回值："tcp" / "udp" / "both"
//
// 用于端口冲突校验：同一端口下 TCP 和 UDP 协议可以并存，
// 同网络类型的协议（两个 TCP 或两个 UDP）则不行。
func (p Protocol) Network() string {
	switch p {
	case Hysteria2, TUIC, WireGuard:
		return "udp"
	case Mixed, Socks:
		return "both"
	default:
		return "tcp"
	}
}

// ConflictsWith 判断新协议与同端口已有协议是否发生网络层冲突。
func (p Protocol) ConflictsWith(other Protocol) bool {
	a, b := p.Network(), other.Network()
	if a == "both" || b == "both" {
		return true
	}
	return a == b
}

// BuildSingBoxInbound 将持久化的入站记录转换为 sing-box 的 inbound/endpoint 配置。
//
// 调用方负责区分 Protocol.IsEndpoint() 决定拼接到 Config.Endpoints 还是 Config.Inbounds。
func (i *Inbound) BuildSingBoxInbound() *singbox.InboundConfig {
	return &singbox.InboundConfig{
		Type:       string(i.Protocol),
		Tag:        i.Tag,
		Listen:     i.Listen,
		ListenPort: i.Port,
		Settings:   json_util.RawMessage(i.Settings),
		Sniff:      json_util.RawMessage(i.Sniffing),
	}
}

type Setting struct {
	Id    int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Key   string `json:"key" form:"key"`
	Value string `json:"value" form:"value"`
}
