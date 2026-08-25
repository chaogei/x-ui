package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"x-ui/core/singbox/spec"
	"x-ui/database/model"
)

// reservedInboundKeys 是由 x-ui 强类型字段独占的 sing-box inbound 键。
//
// InboundConfig.MarshalJSON 会把 Settings / Sniffing 的内容与这些顶层字段
// 平铺拼接；若用户提交的 settings 里也带了同名键，生成的 JSON 会出现重复键，
// sing-box 解析时以后者为准，等于用户可以静默改写监听端口/监听地址/tag。
var reservedInboundKeys = []string{"type", "tag", "listen", "listen_port"}

// FieldError 是可直接回传给前端的字段级校验错误。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// ValidateInbound 在写库之前完整校验一条入站记录。
//
// 这样非法配置会在 API 层带着字段名被拒绝，而不是等到 cron 触发
// Process.Start 时让整份 sing-box 配置一起启动失败——后者会连带
// 打挂其他所有正常入站，且用户在界面上看不到原因。
func ValidateInbound(inbound *model.Inbound) error {
	if inbound == nil {
		return &FieldError{Field: "inbound", Reason: "empty payload"}
	}
	if _, ok := spec.Get(string(inbound.Protocol)); !ok {
		return &FieldError{Field: "protocol", Reason: fmt.Sprintf("unsupported protocol %q", inbound.Protocol)}
	}
	if inbound.Port <= 0 || inbound.Port > 65535 {
		return &FieldError{Field: "port", Reason: fmt.Sprintf("port %d out of range 1-65535", inbound.Port)}
	}
	if err := validateSettingsJSON("settings", inbound.Settings); err != nil {
		return err
	}
	if err := validateSettingsJSON("sniffing", inbound.Sniffing); err != nil {
		return err
	}
	return validateRealitySettings(inbound.Settings)
}

// realitySettings 只解析校验 Reality 所需的最小字段子集。
type realitySettings struct {
	TLS struct {
		Reality struct {
			Enabled    bool   `json:"enabled"`
			PrivateKey string `json:"private_key"`
			PublicKey  string `json:"public_key"`
		} `json:"reality"`
	} `json:"tls"`
}

// validateRealitySettings 在启用 Reality 时强制要求成对且一致的密钥。
func validateRealitySettings(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.Contains(trimmed, "reality") {
		return nil
	}
	var s realitySettings
	if err := json.Unmarshal([]byte(trimmed), &s); err != nil {
		// JSON 结构本身已在 validateSettingsJSON 校验过；这里形状不匹配就跳过。
		return nil
	}
	if !s.TLS.Reality.Enabled {
		return nil
	}
	return ValidateRealityKeys(s.TLS.Reality.PrivateKey, s.TLS.Reality.PublicKey)
}

// validateSettingsJSON 校验一段协议私有 JSON：必须是对象，且不得包含保留键。
// 空串视为合法（表示该协议无附加字段）。
func validateSettingsJSON(field, raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return nil
	}

	var probe interface{}
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return &FieldError{Field: field, Reason: "invalid JSON: " + err.Error()}
	}
	obj, ok := probe.(map[string]interface{})
	if !ok {
		return &FieldError{Field: field, Reason: "must be a JSON object"}
	}

	found := make([]string, 0, len(reservedInboundKeys))
	for _, key := range reservedInboundKeys {
		if _, exists := obj[key]; exists {
			found = append(found, key)
		}
	}
	if len(found) > 0 {
		sort.Strings(found)
		return &FieldError{
			Field:  field,
			Reason: "must not contain reserved key(s): " + strings.Join(found, ", "),
		}
	}
	return nil
}
