package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// CSRF 中间件 —— Double Submit with Session 方案：
//
//  1. 所有请求进入时，若 session 无 csrf_token，生成 32 字节随机 hex 并持久化到 session
//  2. 模板通过 SessionCSRFToken(c) 获取 token，渲染到 <meta name="csrf-token">
//  3. 前端 axios interceptor 读取 meta，在每次 POST 请求头附带 X-CSRF-Token
//  4. 本中间件对所有非幂等方法（POST/PUT/PATCH/DELETE）校验 header 与 session 值严格相等
//
// 未带 token 或不匹配 → 403。登录接口也参与校验，避免 CSRF-login 攻击。
const (
	SessionKeyCSRFToken = "CSRF_TOKEN"
	HeaderCSRFToken     = "X-CSRF-Token"
)

// CSRF 返回一个 gin.HandlerFunc，依赖前置已挂载的 gin-contrib/sessions。
func CSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		s := sessions.Default(c)
		token, _ := s.Get(SessionKeyCSRFToken).(string)
		if token == "" {
			token = randomToken()
			s.Set(SessionKeyCSRFToken, token)
			if err := s.Save(); err != nil {
				// fail-fast：token 持久化失败时直接 500，避免后续校验路径出现不一致
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
		}
		c.Set(SessionKeyCSRFToken, token)

		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}
		supplied := c.GetHeader(HeaderCSRFToken)
		if supplied == "" || supplied != token {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"msg":     "csrf token mismatch",
			})
			return
		}
		c.Next()
	}
}

// SessionCSRFToken 供模板/控制器从 gin.Context 读出当前请求的 CSRF token。
// 必须在 CSRF 中间件后调用，否则返回空串。
func SessionCSRFToken(c *gin.Context) string {
	if v, ok := c.Get(SessionKeyCSRFToken); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 在主流操作系统上不会失败，此处 fail-fast panic 也符合面板启动阶段预期
		panic("crypto/rand read failed: " + err.Error())
	}
	return hex.EncodeToString(buf)
}
