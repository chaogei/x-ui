package middleware

import (
	"github.com/gin-gonic/gin"
)

// contentSecurityPolicy 是与本面板实际资源加载方式匹配的 CSP。
//
// 说明：
//   - 没有 'unsafe-eval'：面板早已不是 Vue 2 的在线模板编译器，Vue 3 的模板在
//     构建期就编译成了渲染函数，运行时不碰 eval/Function。这条由
//     TestE2EFrontendBootsWithoutUnsafeEval 把着——它让 jsdom 封掉这两样再跑
//     一遍真实产物；
//   - script-src 的 'unsafe-inline' 还留着，是为了 app.html 里那一句
//     `window.__XUI__ = ...`。去掉它要给这个 script 发 nonce，并让 CSP 头随
//     每次响应变化；
//   - style-src 的 'unsafe-inline' 是 antd 的运行时样式注入所需，改不掉；
//   - 关键收益在 default-src 'self'（禁止加载外部脚本）与 frame-ancestors 'none'
//     （防点击劫持），这两项不依赖内联脚本的整改。
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
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
