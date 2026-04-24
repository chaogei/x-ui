// Package singbox 提供基于 sing-box 内核的 core.Core 实现。
//
// 设计原则：
//  1. 配置采用自定义 Go struct 镜像 sing-box JSON schema，
//     运行时仍通过 exec 调用外部 bin/sing-box 二进制启动。
//  2. 协议相关字段（VMess/VLESS/Hysteria2/TUIC/Reality 等）
//     由前端以完整 JSON 片段上传，在 Settings 字段中以 RawMessage 存储，
//     服务端仅做结构聚合与 Tag 管理，不反序列化协议细节。
//  3. 流量统计借道 sing-box 的 experimental.v2ray_api，
//     与 V2Ray/Xray 的 StatsService protobuf 完全兼容。
package singbox

import (
	"bytes"
	"encoding/json"
	"x-ui/core"
	"x-ui/util/json_util"
)

// Config 是 sing-box 的顶层配置结构。
//
// 字段命名与 sing-box 官方 JSON 完全一致，未覆盖的冷门字段通过
// RawMessage 透传，允许用户在面板"自定义配置模板"中任意扩展。
type Config struct {
	Log          json_util.RawMessage `json:"log,omitempty"`
	DNS          json_util.RawMessage `json:"dns,omitempty"`
	NTP          json_util.RawMessage `json:"ntp,omitempty"`
	Certificate  json_util.RawMessage `json:"certificate,omitempty"`
	Endpoints    []InboundConfig      `json:"endpoints,omitempty"` // WireGuard 等在 1.11+ 迁移至 endpoints
	Inbounds     []InboundConfig      `json:"inbounds"`
	Outbounds    json_util.RawMessage `json:"outbounds,omitempty"`
	Route        json_util.RawMessage `json:"route,omitempty"`
	Experimental json_util.RawMessage `json:"experimental,omitempty"`
}

// 断言 *Config 实现 core.Config。
var _ core.Config = (*Config)(nil)

// MarshalJSON 将配置序列化为 sing-box 可直接加载的 JSON。
func (c *Config) MarshalJSON() ([]byte, error) {
	type alias Config
	return json.Marshal((*alias)(c))
}

// Equals 比较两份配置在序列化层面的等价性，
// 用于避免不必要的 sing-box 重启。
func (c *Config) Equals(other core.Config) bool {
	o, ok := other.(*Config)
	if !ok || o == nil {
		return false
	}
	if len(c.Inbounds) != len(o.Inbounds) {
		return false
	}
	for i := range c.Inbounds {
		if !c.Inbounds[i].Equals(&o.Inbounds[i]) {
			return false
		}
	}
	if len(c.Endpoints) != len(o.Endpoints) {
		return false
	}
	for i := range c.Endpoints {
		if !c.Endpoints[i].Equals(&o.Endpoints[i]) {
			return false
		}
	}
	return bytes.Equal(c.Log, o.Log) &&
		bytes.Equal(c.DNS, o.DNS) &&
		bytes.Equal(c.NTP, o.NTP) &&
		bytes.Equal(c.Certificate, o.Certificate) &&
		bytes.Equal(c.Outbounds, o.Outbounds) &&
		bytes.Equal(c.Route, o.Route) &&
		bytes.Equal(c.Experimental, o.Experimental)
}
