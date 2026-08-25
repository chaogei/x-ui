package web

import (
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"x-ui/web/global"
	"x-ui/web/service"
)

// TestE2ETrustedProxiesEnableXFF 是 H-1 的对照实验。
//
// 默认不信任任何代理时伪造 X-Forwarded-For 毫无作用（见
// TestE2EForgedXFFDoesNotBypassLockout）。运维显式声明前置代理之后，
// XFF 才会被采信——这是反向代理部署下按真实来源限流的前提，
// 同时也说明这个开关必须谨慎打开。
func TestE2ETrustedProxiesEnableXFF(t *testing.T) {
	p := newPanel(t, withTrustedProxies("127.0.0.1/32"))
	limit := p.server.loginLimiter.MaxFailures

	// 把某一个来源 IP 打到锁定。
	for i := 0; i < limit; i++ {
		resp := p.postForm("login", url.Values{
			"username": {p.username},
			"password": {"wrong-password"},
		}, [2]string{"X-Forwarded-For", "198.51.100.7"})
		if msg := p.decode(resp); msg.Success {
			t.Fatalf("attempt %d unexpectedly succeeded", i)
		}
	}
	locked := p.decode(p.postForm("login", url.Values{
		"username": {p.username},
		"password": {p.password},
	}, [2]string{"X-Forwarded-For", "198.51.100.7"}))
	if locked.Success {
		t.Fatal("the reported client IP was not locked out")
	}

	// 另一个来源 IP 有自己的额度，不受牵连。
	other := p.decode(p.postForm("login", url.Values{
		"username": {p.username},
		"password": {p.password},
	}, [2]string{"X-Forwarded-For", "198.51.100.8"}))
	if !other.Success {
		t.Errorf("a different forwarded client was blocked by another IP's lockout: %s", other.Msg)
	}
}

// TestE2EGracefulShutdown 覆盖 M-5。
//
// 旧实现先 s.cancel() 再把已取消的 context 交给 httpServer.Shutdown，
// 等于立刻强杀所有在途请求。现在 Shutdown 用独立的带超时 context，
// 服务级 context 在排空之后才取消。
func TestE2EGracefulShutdown(t *testing.T) {
	newBareDB(t)
	// 端口 0 让内核挑一个空闲端口，测试之间不会互相抢 54321。
	writeSetting(t, "webPort", "0")

	server := NewServer()
	global.SetWebServer(server)
	if err := server.Start(); err != nil {
		t.Fatalf("start panel: %v", err)
	}

	if server.GetCtx().Err() != nil {
		t.Fatal("the server context is already cancelled right after Start")
	}

	done := make(chan error, 1)
	go func() { done <- server.Stop() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Stop returned %v, want a clean shutdown", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("Stop hung past the shutdown timeout")
	}

	// 排空完成之后服务级 context 才该被取消。
	if server.GetCtx().Err() == nil {
		t.Error("the server context should be cancelled once Stop returns")
	}
}

// TestE2EStartFailsCleanlyOnOccupiedPort 启动失败时不能留下半开的监听，
// 而且 Start 内部 defer 的 Stop 与调用方再调一次都不能 panic 或阻塞。
func TestE2EStartFailsCleanlyOnOccupiedPort(t *testing.T) {
	newBareDB(t)

	writeSetting(t, "webPort", "0")
	first := NewServer()
	global.SetWebServer(first)
	if err := first.Start(); err != nil {
		t.Fatalf("start first panel: %v", err)
	}
	t.Cleanup(func() { _ = first.Stop() })

	port := first.listener.Addr().(*net.TCPAddr).Port
	writeSetting(t, "webPort", strconv.Itoa(port))

	second := NewServer()
	global.SetWebServer(second)
	if err := second.Start(); err == nil {
		_ = second.Stop()
		t.Fatal("binding an occupied port must fail")
	}

	done := make(chan error, 1)
	go func() { done <- second.Stop() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hung after a failed Start")
	}
}

// TestE2EBackoffThrottlesFailingRestarts 覆盖 P2-18 的退避。
//
// 配置永久非法时，没有退避的实现会每 10 秒 fork 一次 sing-box，
// 无限期烧 CPU 和磁盘日志。
func TestE2EBackoffThrottlesFailingRestarts(t *testing.T) {
	b := service.NewBackoff(10*time.Millisecond, 100*time.Millisecond)

	if !b.Ready() {
		t.Fatal("a fresh backoff must allow the first attempt immediately")
	}

	delay := b.Fail()
	if delay <= 0 {
		t.Fatalf("Fail returned %v, want a positive delay", delay)
	}
	if b.Ready() {
		t.Error("a retry inside the backoff window must be refused")
	}

	// 连续失败时间隔单调增长，直到封顶。
	prev := delay
	for i := 0; i < 8; i++ {
		next := b.Fail()
		if next < prev {
			t.Errorf("delay shrank from %v to %v", prev, next)
		}
		prev = next
	}
	if prev > 100*time.Millisecond {
		t.Errorf("delay %v exceeded the configured ceiling", prev)
	}

	// 一次成功立即复位，用户改好配置后不必再等一个长窗口。
	b.Succeed()
	if !b.Ready() {
		t.Error("a successful attempt must reset the backoff immediately")
	}
}
