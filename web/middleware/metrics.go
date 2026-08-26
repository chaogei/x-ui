package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"x-ui/web/metrics"
)

// Metrics 记录每个请求的计数与耗时。
//
// 标签只用 gin 的路由模板（c.FullPath()）。用真实 URL 会让
// /xui/client/del/1、/xui/client/del/2 各自长出一条时间序列，
// 入站一多就把抓取端的内存吃光——这是 Prometheus 集成最常见的事故。
//
// 没有匹配到任何路由的请求（404、扫描器流量）统一归到 "other"：
// 攻击者可以随手构造无限多的不存在路径，逐个建序列等于把基数控制权
// 交给了对面。
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = "other"
		}
		method := c.Request.Method

		metrics.HTTPRequests.WithLabelValues(path, method, strconv.Itoa(c.Writer.Status())).Inc()
		metrics.HTTPDuration.WithLabelValues(path, method).Observe(time.Since(start).Seconds())
	}
}
