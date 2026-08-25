package service

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ClientIP 是面板内提取「请求真实来源 IP」的唯一入口。
//
// 安全模型：
//   - gin 引擎通过 SetTrustedProxies 决定是否采信 X-Forwarded-For / X-Real-IP。
//     默认配置为 nil（不信任任何代理），此时 c.ClientIP() 等价于 TCP 对端地址，
//     攻击者伪造 XFF 无法改变登录限流的分桶，也无法污染审计日志。
//   - 运维在「面板设置 → 受信代理」填入前置代理网段后，gin 才会沿 XFF 回溯。
//
// 登录限流器与审计日志共用本函数，保证两者对"同一个客户端"的判定完全一致。
func ClientIP(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	return remoteAddrIP(c.Request)
}

// remoteAddrIP 从 http.Request.RemoteAddr 中剥出 IP 部分。
func remoteAddrIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
