package middleware

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"x-ui/config"
)

var (
	accessOnce   sync.Once
	accessLogger *slog.Logger
)

// accessLevel 决定访问日志的记录级别：
// 调试模式下 Info（默认可见），生产模式下 Debug（默认被 handler 过滤掉），
// 避免面板每 2 秒一次的状态轮询把磁盘写满。
func accessLevel() slog.Level {
	if config.IsDebug() {
		return slog.LevelInfo
	}
	return slog.LevelDebug
}

func getAccessLogger() *slog.Logger {
	accessOnce.Do(func() {
		level := slog.LevelInfo
		if !config.IsDebug() {
			level = slog.LevelWarn // 生产模式默认过滤掉 Debug 级访问日志
		}
		accessLogger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	})
	return accessLogger
}

// AccessLog 以结构化 JSON 记录 HTTP 访问日志。
//
// 只记录方法、路径、状态码、耗时与客户端 IP —— 绝不记录请求体或 query 之外的内容，
// 因为登录表单里带明文密码。查询串也不落盘，避免 token 通过 URL 泄漏到日志。
func AccessLog() gin.HandlerFunc {
	level := accessLevel()
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		getAccessLogger().Log(c.Request.Context(), level, "http_access",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("ip", c.ClientIP()),
		)
	}
}
