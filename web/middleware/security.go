package middleware

import (
	"github.com/gin-gonic/gin"
)

// contentSecurityPolicy 是与本面板实际资源加载方式匹配的 CSP。
//
// 说明：
//   - 'unsafe-eval' 是 Vue 2 完整版编译 in-DOM 模板所必需的；
//   - 'unsafe-inline' 是因为页面里存在大量内联 <script>/<style>（antd + Vue 用法），
//     一次性去掉需要重写全部模板，超出本次修复范围；
//   - 关键收益在 default-src 'self'（禁止加载外部脚本）与 frame-ancestors 'none'
//     （防点击劫持），这两项不依赖内联脚本的整改。
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

// SecurityHeaders 给所有响应加上基础安全响应头。
//
// isHTTPS 为 true 时额外下发 HSTS —— 在纯 HTTP 部署下发 HSTS 会把用户浏览器
// 永久锁定到 https，导致面板彻底无法访问，因此必须由调用方按实际监听方式决定。
func SecurityHeaders(isHTTPS bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		if isHTTPS {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
