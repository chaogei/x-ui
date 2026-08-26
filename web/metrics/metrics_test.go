package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// 入站被删掉之后，它的时间序列必须跟着消失。否则监控面板上会永远挂着
// 一条不再更新的僵尸曲线，而"流量停止增长"恰恰是告警最该触发的形态。
func TestSetInboundTrafficDropsRemovedInbounds(t *testing.T) {
	SetInboundTraffic([]InboundTraffic{
		{Tag: "in-a", ID: "1", Protocol: "vmess", Up: 10, Down: 20},
		{Tag: "in-b", ID: "2", Protocol: "trojan", Up: 30, Down: 40},
	})
	if got := testutil.CollectAndCount(InboundUp); got != 2 {
		t.Fatalf("collected %d up series, want 2", got)
	}

	SetInboundTraffic([]InboundTraffic{
		{Tag: "in-a", ID: "1", Protocol: "vmess", Up: 11, Down: 21},
	})
	if got := testutil.CollectAndCount(InboundUp); got != 1 {
		t.Errorf("collected %d up series after a deletion, want 1", got)
	}
	if got := testutil.ToFloat64(InboundUp.WithLabelValues("in-a", "1", "vmess")); got != 11 {
		t.Errorf("in-a up = %v, want the refreshed value 11", got)
	}

	// 清空快照（所有入站都被删了）之后不该剩下任何序列。
	SetInboundTraffic(nil)
	if got := testutil.CollectAndCount(InboundUp); got != 0 {
		t.Errorf("collected %d series after every inbound was removed, want 0", got)
	}
}

func TestCoreRunningGauge(t *testing.T) {
	SetCoreRunning(true)
	if got := testutil.ToFloat64(CoreRunning); got != 1 {
		t.Errorf("xui_core_running = %v, want 1", got)
	}
	SetCoreRunning(false)
	if got := testutil.ToFloat64(CoreRunning); got != 0 {
		t.Errorf("xui_core_running = %v, want 0", got)
	}
}

// 三种失败原因在进程启动时就必须存在（值为 0）。
// "从未失败"与"指标不存在"在抓取端长得一样，基于后者写的告警会静默。
func TestLoginFailureReasonsArePreInitialised(t *testing.T) {
	if got := testutil.CollectAndCount(LoginFailures); got != 3 {
		t.Fatalf("collected %d login-failure series, want the three known reasons", got)
	}
	expected := `
# HELP xui_login_fail_total Failed panel login attempts.
# TYPE xui_login_fail_total counter
xui_login_fail_total{reason="bad_credentials"} 0
xui_login_fail_total{reason="locked"} 0
xui_login_fail_total{reason="two_factor"} 0
`
	if err := testutil.CollectAndCompare(LoginFailures, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

// 指标名是与运维告警规则之间的契约，改名等于悄悄让别人的看板失效。
func TestMetricNamesAreStable(t *testing.T) {
	// 向量型指标在没有任何子序列时不会出现在 Gather 结果里，
	// 所以先喂一条样本，否则这条断言测的是"上一个用例留下了什么"。
	SetInboundTraffic([]InboundTraffic{{Tag: "probe", ID: "1", Protocol: "vmess"}})
	t.Cleanup(func() { SetInboundTraffic(nil) })
	HTTPRequests.WithLabelValues("/probe", "GET", "200")
	HTTPDuration.WithLabelValues("/probe", "GET")
	t.Cleanup(func() {
		HTTPRequests.Reset()
		HTTPDuration.Reset()
	})

	families, err := Registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	present := map[string]bool{}
	for _, f := range families {
		present[f.GetName()] = true
	}
	for _, name := range []string{
		"xui_up",
		"xui_core_running",
		"xui_core_restarts_total",
		"xui_login_fail_total",
		"xui_inbound_up_bytes",
		"xui_inbound_down_bytes",
		"xui_http_requests_total",
		"xui_http_request_duration_seconds",
	} {
		if !present[name] {
			t.Errorf("the registry no longer exposes %q", name)
		}
	}
}
