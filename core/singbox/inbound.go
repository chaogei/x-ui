package singbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"x-ui/util/json_util"
)

// InboundConfig 表示 sing-box 中的一个 inbound/endpoint 条目。
//
// sing-box 采用 type + flat fields 的多态结构（VMess/VLESS/Hysteria2/TUIC/...
// 各自有独立字段集），与 Xray 的 protocol+settings 嵌套结构完全不同。
// 为兼顾类型安全与灵活性：
//   - Type、Tag、Listen、ListenPort 这些通用字段强类型
//   - Settings 为完整的协议私有字段 JSON（由前端按 sing-box schema 提交）
//   - Sniff 为 sniff 相关的顶层字段集
//   - 序列化时将 Settings/Sniff 的 key/value 与顶层字段平铺合并输出
//
// 约定：前端提交的 Settings / Sniff 不得包含 type/tag/listen/listen_port 等
// 与强类型字段重名的键，后端不再做去重（相比之前的 map 合并版本，改为
// byte-level 拼接以消除热路径上的 Unmarshal+Marshal 开销）。
type InboundConfig struct {
	Type       string               `json:"type"`
	Tag        string               `json:"tag,omitempty"`
	Listen     string               `json:"listen,omitempty"`
	ListenPort int                  `json:"listen_port,omitempty"`
	Settings   json_util.RawMessage `json:"-"` // 协议私有字段（如 users, tls, masquerade 等）
	Sniff      json_util.RawMessage `json:"-"` // sing-box 的 sniff 相关字段也整体放入 Settings
}

// Equals 基于字段值做语义比对。
func (c *InboundConfig) Equals(other *InboundConfig) bool {
	if c.Type != other.Type ||
		c.Tag != other.Tag ||
		c.Listen != other.Listen ||
		c.ListenPort != other.ListenPort {
		return false
	}
	return bytes.Equal(c.Settings, other.Settings) && bytes.Equal(c.Sniff, other.Sniff)
}

// MarshalJSON 将顶层字段和 Settings/Sniff 中的协议私有字段平铺合并。
//
// 实现采用 byte-level 拼接，避免对 Settings / Sniff 做一次 Unmarshal+Marshal，
// 这条路径在 sing-box 频繁 restart / Equals 比较时是热点。
func (c *InboundConfig) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(64 + len(c.Settings) + len(c.Sniff))
	buf.WriteByte('{')

	written := false
	writeField := func(key string, value interface{}) error {
		if written {
			buf.WriteByte(',')
		}
		written = true
		buf.WriteByte('"')
		buf.WriteString(key)
		buf.WriteString(`":`)
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		buf.Write(raw)
		return nil
	}

	if err := writeField("type", c.Type); err != nil {
		return nil, err
	}
	if c.Tag != "" {
		if err := writeField("tag", c.Tag); err != nil {
			return nil, err
		}
	}
	if c.Listen != "" {
		if err := writeField("listen", c.Listen); err != nil {
			return nil, err
		}
	}
	if c.ListenPort != 0 {
		if err := writeField("listen_port", c.ListenPort); err != nil {
			return nil, err
		}
	}

	if err := appendInnerObject(&buf, c.Settings, &written); err != nil {
		return nil, fmt.Errorf("inbound settings invalid: %w", err)
	}
	if err := appendInnerObject(&buf, c.Sniff, &written); err != nil {
		return nil, fmt.Errorf("inbound sniff invalid: %w", err)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// appendInnerObject 将 raw（预期为 JSON 对象，如 `{"users":[...],"tls":{...}}`）的
// 内容剥离外层 `{}` 后附加到 buf，实现与顶层字段平铺合并的效果。
//
// 空、`null`、空对象 `{}` 均跳过；非对象类型返回错误以暴露上游 schema 误用。
func appendInnerObject(buf *bytes.Buffer, raw json_util.RawMessage, written *bool) error {
	if len(raw) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return fmt.Errorf("expect JSON object, got %q", truncate(trimmed, 32))
	}
	inner := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
	if len(inner) == 0 {
		return nil
	}
	if *written {
		buf.WriteByte(',')
	}
	*written = true
	buf.Write(inner)
	return nil
}

// truncate 返回字节串的前 n 个字节用于错误信息，避免把大块 JSON 写进日志。
func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// UnmarshalJSON 对偶实现：允许从模板 JSON 中反序列化回来。
//
// 策略：先用临时结构捕获强类型字段，再把剩余所有 key 打包到 Settings。
func (c *InboundConfig) UnmarshalJSON(data []byte) error {
	var base struct {
		Type       string `json:"type"`
		Tag        string `json:"tag,omitempty"`
		Listen     string `json:"listen,omitempty"`
		ListenPort int    `json:"listen_port,omitempty"`
	}
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	c.Type = base.Type
	c.Tag = base.Tag
	c.Listen = base.Listen
	c.ListenPort = base.ListenPort

	var full map[string]json.RawMessage
	if err := json.Unmarshal(data, &full); err != nil {
		return err
	}
	for _, key := range []string{"type", "tag", "listen", "listen_port"} {
		delete(full, key)
	}
	if len(full) == 0 {
		c.Settings = nil
		return nil
	}
	raw, err := json.Marshal(full)
	if err != nil {
		return err
	}
	c.Settings = raw
	return nil
}
