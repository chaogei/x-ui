package service

import (
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/gin-gonic/gin"

	"x-ui/web/session"
)

// 审计事件名（面板所有对安全/合规敏感的操作用同一套枚举常量，
// 便于 journald / 日志采集端用 grep "AUDIT " 做精确过滤）。
const (
	EventLoginSuccess        = "login_success"
	EventLoginFail           = "login_fail"
	EventLoginLocked         = "login_locked"
	EventLogout              = "logout"
	EventUserUpdate          = "user_update"
	EventInboundAdd          = "inbound_add"
	EventInboundUpdate       = "inbound_update"
	EventInboundDelete       = "inbound_delete"
	EventInboundResetTraffic = "inbound_reset_traffic"
	EventSettingUpdate       = "setting_update"
	EventPanelRestart        = "panel_restart"

	EventClientAdd          = "client_add"
	EventClientUpdate       = "client_update"
	EventClientDelete       = "client_delete"
	EventClientResetTraffic = "client_reset_traffic"
	// EventClientRotateToken 只记录 client_id：新旧订阅 token 都是凭证，
	// 写进审计等于把它们复制到另一个更容易被读取的地方。
	EventClientRotateToken = "client_rotate_token"

	EventTwoFactorEnroll  = "twofactor_enroll"
	EventTwoFactorEnable  = "twofactor_enable"
	EventTwoFactorDisable = "twofactor_disable"
	EventTwoFactorFail    = "twofactor_fail"
	EventRecoveryCodeUsed = "recovery_code_used"

	EventSubscriptionFetch = "subscription_fetch"
)

// auditPrefix 让审计行在混合日志流里仍能被 grep 精确定位。
const auditPrefix = "AUDIT "

// prefixWriter 在每条 slog JSON 记录前加上固定前缀。
// slog.JSONHandler 保证一次 Write 恰好对应一条完整记录（含结尾换行）。
type prefixWriter struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := io.WriteString(p.w, p.prefix); err != nil {
		return 0, err
	}
	return p.w.Write(b)
}

var (
	auditMu     sync.RWMutex
	auditSink   = &prefixWriter{w: os.Stderr, prefix: auditPrefix}
	auditLogger = slog.New(slog.NewJSONHandler(auditSink, &slog.HandlerOptions{Level: slog.LevelInfo}))
)

// SetAuditOutput 替换审计日志的输出目标并返回旧值，供测试断言事件内容。
func SetAuditOutput(w io.Writer) io.Writer {
	auditMu.Lock()
	defer auditMu.Unlock()
	old := auditSink.w
	auditSink.mu.Lock()
	auditSink.w = w
	auditSink.mu.Unlock()
	return old
}

// Audit 写入一条结构化审计日志。
//
// 使用 stdlib log/slog 的 JSON handler：字段是真正的结构化键值对，
// 不会因为用户提交的字符串里带引号/换行而破坏日志行（不可伪造成额外事件）。
//
// result 取值建议：ok / fail / locked；extra 承载事件私有上下文（如 inbound_id、reason）。
func Audit(c *gin.Context, event, result string, extra map[string]interface{}) {
	attrs := []any{
		slog.String("event", event),
		slog.String("result", result),
		slog.String("ip", ClientIP(c)),
	}
	if c != nil {
		if user := session.GetLoginUser(c); user != nil {
			attrs = append(attrs, slog.String("user", user.Username), slog.Int("uid", user.Id))
		}
	}
	for k, v := range extra {
		attrs = append(attrs, slog.Any(k, v))
	}

	auditMu.RLock()
	l := auditLogger
	auditMu.RUnlock()
	l.Info("audit", attrs...)
}
