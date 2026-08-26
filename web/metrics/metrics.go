// Package metrics 暴露面板的 Prometheus 指标。
//
// 两条贯穿整包的原则：
//
//   - 低基数。标签只允许取自有限集合（路由模板、方法、入站 tag），
//     绝不把原始 URL 或用户输入塞进标签——一个带 id 的路径就能让
//     时间序列数量随入站数量线性膨胀，把抓取端的内存吃光。
//   - 不泄密。指标里没有凭证、订阅 token、用户邮箱。入站维度用 tag 与 id，
//     它们本来就会出现在面板 UI 上。
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Registry 是面板自己的注册表。
//
// 不用 prometheus.DefaultRegisterer：默认注册表是全局可写的，任何依赖库
// 都能往里塞指标，抓取结果会随依赖升级莫名其妙地变化。自己持有一个注册表
// 也让测试可以断言"暴露的就是这些"。
var Registry = prometheus.NewRegistry()

var (
	// Up 恒为 1，用来表达"这个面板此刻在响应抓取"。
	Up = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "xui_up",
		Help: "1 when the x-ui panel is serving requests.",
	})

	// CoreRunning 是 sing-box 子进程是否存活。
	// 取值在抓取时现算，见 SetCoreStateSource。
	CoreRunning = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "xui_core_running",
		Help: "1 when the sing-box process is running, 0 otherwise.",
	})

	// CoreRestarts 统计面板重启内核的次数。
	// 持续增长通常意味着配置有问题，内核起来就崩。
	CoreRestarts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "xui_core_restarts_total",
		Help: "Number of times the panel has (re)started the sing-box process.",
	})

	// LoginFailures 是登录失败次数，按失败原因分。
	// reason 取自固定集合：bad_credentials / two_factor / locked。
	LoginFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "xui_login_fail_total",
		Help: "Failed panel login attempts.",
	}, []string{"reason"})

	// InboundUp / InboundDown 是每条入站的累计流量。
	// 与面板显示的数字同源（数据库里的 up/down 列），抓取时刷新。
	InboundUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "xui_inbound_up_bytes",
		Help: "Total bytes uploaded through an inbound since its counter was last reset.",
	}, []string{"tag", "id", "protocol"})

	InboundDown = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "xui_inbound_down_bytes",
		Help: "Total bytes downloaded through an inbound since its counter was last reset.",
	}, []string{"tag", "id", "protocol"})

	// HTTPRequests / HTTPDuration 描述面板自身的 HTTP 负载。
	//
	// path 标签用 gin 的路由模板（例如 /xui/client/del/:id）而不是真实 URL：
	// 后者会让每个客户端 id 都长出一条独立的时间序列。
	HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "xui_http_requests_total",
		Help: "Panel HTTP requests by route template, method and status class.",
	}, []string{"path", "method", "status"})

	HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "xui_http_request_duration_seconds",
		Help:    "Panel HTTP request latency by route template.",
		Buckets: prometheus.DefBuckets,
	}, []string{"path", "method"})
)

func init() {
	Registry.MustRegister(
		Up,
		CoreRunning,
		CoreRestarts,
		LoginFailures,
		InboundUp,
		InboundDown,
		HTTPRequests,
		HTTPDuration,
		// 进程与 Go 运行时指标：排查面板自身的内存/句柄泄漏时唯一有用的东西。
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)
	Up.Set(1)

	// 把三种失败原因都预置为 0。计数器只在第一次自增时才出现，
	// 而"从未失败"与"这个指标不存在"在抓取端长得一模一样——
	// 基于它写的告警规则会在最需要的时候静默。
	for _, reason := range []string{ReasonBadCredentials, ReasonTwoFactor, ReasonLocked} {
		LoginFailures.WithLabelValues(reason)
	}
}

// 失败原因常量。使用常量而不是随手写字符串，是为了让标签取值保持有限集合。
const (
	ReasonBadCredentials = "bad_credentials"
	ReasonTwoFactor      = "two_factor"
	ReasonLocked         = "locked"
)

// RecordLoginFailure 记一次登录失败。
func RecordLoginFailure(reason string) {
	LoginFailures.WithLabelValues(reason).Inc()
}

// RecordCoreRestart 记一次内核启动。
func RecordCoreRestart() {
	CoreRestarts.Inc()
}

// SetCoreRunning 更新内核存活状态。
func SetCoreRunning(running bool) {
	if running {
		CoreRunning.Set(1)
		return
	}
	CoreRunning.Set(0)
}

// InboundTraffic 是一条入站在抓取时刻的流量快照。
type InboundTraffic struct {
	Tag      string
	ID       string
	Protocol string
	Up       int64
	Down     int64
}

// SetInboundTraffic 用一次快照整体替换入站维度的指标。
//
// 先 Reset 再写：入站被删掉之后，它的时间序列必须跟着消失，
// 否则监控面板上会永远挂着一条不再更新的僵尸曲线。
func SetInboundTraffic(snapshot []InboundTraffic) {
	InboundUp.Reset()
	InboundDown.Reset()
	for _, t := range snapshot {
		InboundUp.WithLabelValues(t.Tag, t.ID, t.Protocol).Set(float64(t.Up))
		InboundDown.WithLabelValues(t.Tag, t.ID, t.Protocol).Set(float64(t.Down))
	}
}
