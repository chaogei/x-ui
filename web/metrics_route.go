package web

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"x-ui/logger"
	"x-ui/web/metrics"
	"x-ui/web/session"
)

// registerMetricsRoute 挂载 GET /metrics。
//
// 鉴权：面板会话 或 metricsToken 配置项对应的 bearer token。二选一，永不放行匿名。
//
// 为什么默认不开放：抓取结果里有入站 tag、端口维度的流量与面板自身的
// 请求路径分布。对一台公网面板来说，这等于把"这里跑着什么、有多少人在用"
// 免费发给任何扫到 /metrics 的人。Prometheus 生态里默认裸奔的
// exporter 太多了，这个面板不做其中之一。
//
// 与 /healthz 的区别是刻意的：探针只回一个状态字，没有任何可供侦察的内容，
// 所以它保持匿名可访问，而 /metrics 不行。
func (s *Server) registerMetricsRoute(engine *gin.Engine, basePath string) {
	handler := promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{
		// 抓取时出错就回 500，不要把半份指标当成正常结果送出去。
		ErrorHandling: promhttp.HTTPErrorOnError,
	})

	serve := func(c *gin.Context) {
		if !s.authorizeMetrics(c) {
			// WWW-Authenticate 让 Prometheus 的 bearer_token 配置错误
			// 在抓取端有个明确的报错，而不是一片空白。
			c.Header("WWW-Authenticate", `Bearer realm="x-ui metrics"`)
			c.String(http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}
		s.refreshMetrics()
		handler.ServeHTTP(c.Writer, c.Request)
	}

	engine.GET("/metrics", serve)
	if basePath != "/" {
		engine.GET(joinBasePath(basePath, "/metrics"), serve)
	}
}

// authorizeMetrics 判定一次抓取请求是否放行。
func (s *Server) authorizeMetrics(c *gin.Context) bool {
	token, err := s.settingService.GetMetricsToken()
	if err != nil {
		logger.Warning("read metrics token failed:", err)
		return false
	}
	token = strings.TrimSpace(token)

	// 未配置 token 时退回会话鉴权：管理员在浏览器里点开 /metrics 能看到内容，
	// 而没登录的人什么也拿不到。这是"默认安全"的那条路径。
	if token == "" {
		return session.IsLogin(c)
	}

	presented := bearerToken(c)
	// 常数时间比较：token 是长期有效的静态凭证，逐字节短路的比较
	// 会把它的前缀通过响应时间漏出去。
	if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1 {
		return true
	}
	// 配了 token 也不排斥已登录的管理员——否则运维想在浏览器里看一眼
	// 还得先去翻配置。
	return session.IsLogin(c)
}

// bearerToken 从 Authorization 头取 token。
//
// 只认请求头，不认 ?token= 查询参数：查询串会被写进访问日志、反向代理日志
// 与浏览器历史，把一个长期凭证撒得到处都是。
func bearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if value, ok := strings.CutPrefix(header, "Bearer "); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// refreshMetrics 在抓取时刷新那些"现算"的指标。
//
// 用拉取时刷新而不是后台定时写入：抓取周期由 Prometheus 决定，
// 多起一个 goroutine 定时写 gauge 只会让数据比抓取时刻更旧。
func (s *Server) refreshMetrics() {
	metrics.SetCoreRunning(s.coreService.IsCoreRunning())

	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		// 指标端点不该因为数据库抖动就整体失败：其余指标仍然有价值。
		logger.Warning("collect inbound metrics failed:", err)
		return
	}
	snapshot := make([]metrics.InboundTraffic, 0, len(inbounds))
	for _, ib := range inbounds {
		snapshot = append(snapshot, metrics.InboundTraffic{
			Tag:      ib.Tag,
			ID:       strconv.Itoa(ib.Id),
			Protocol: string(ib.Protocol),
			Up:       ib.Up,
			Down:     ib.Down,
		})
	}
	metrics.SetInboundTraffic(snapshot)
}
