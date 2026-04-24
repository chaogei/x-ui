package service

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"x-ui/logger"
	"x-ui/web/session"
)

// 审计事件名（面板所有对安全/合规敏感的操作用同一套枚举常量，
// 便于 journald / 日志采集端用 grep "AUDIT.*event=..." 做精确过滤）。
const (
	EventLoginSuccess   = "login_success"
	EventLoginFail      = "login_fail"
	EventLoginLocked    = "login_locked"
	EventLogout         = "logout"
	EventUserUpdate     = "user_update"
	EventInboundAdd     = "inbound_add"
	EventInboundUpdate  = "inbound_update"
	EventInboundDelete  = "inbound_delete"
	EventSettingUpdate  = "setting_update"
	EventPanelRestart   = "panel_restart"
)

// Audit 写入一条结构化审计日志。
//
// 输出一行以 "AUDIT " 作为识别前缀的 JSON（写入同一 logger backend），
// 不依赖独立文件或 DB，降低部署复杂度；若未来需要独立存储只需替换本函数即可。
//
// result 取值建议：ok / fail / locked；extra 承载事件私有上下文（如 inbound_id、reason）。
func Audit(c *gin.Context, event, result string, extra map[string]interface{}) {
	entry := map[string]interface{}{
		"ts":     time.Now().UTC().Format(time.RFC3339),
		"event":  event,
		"result": result,
		"ip":     clientIP(c),
	}
	if c != nil {
		if user := session.GetLoginUser(c); user != nil {
			entry["user"] = user.Username
			entry["uid"] = user.Id
		}
	}
	for k, v := range extra {
		entry[k] = v
	}
	b, err := json.Marshal(entry)
	if err != nil {
		logger.Warningf("audit marshal failed: %v", err)
		return
	}
	logger.Infof("AUDIT %s", b)
}

// clientIP 从 gin.Context 提取客户端 IP。与 controller.getRemoteIp 等价，
// 此处避免反向依赖 controller 包，故本地实现一份。
func clientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v := c.GetHeader("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}
