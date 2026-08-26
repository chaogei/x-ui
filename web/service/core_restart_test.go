package service

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"x-ui/core"
	xtestutil "x-ui/testutil"
	"x-ui/web/metrics"
)

// fakeCore 是 core.Core 的可编排替身。
//
// CI 上没有 sing-box 二进制，而这里要验的恰恰是"进程起来之后又没了"
// 这条时序——用替身能精确摆出那个状态，用真二进制反而摆不出来。
type fakeCore struct {
	mu sync.Mutex

	// aliveAfterStart 决定 Start 之后 IsRunning 报什么。
	// false 模拟配置非法：exec 成功，内核自检失败后立刻退出。
	aliveAfterStart bool

	// traffic / trafficErr 是 GetTraffic 的编排结果，用来替代真实的
	// v2ray_api 统计连接。
	traffic    []*core.Traffic
	trafficErr error

	running bool
	starts  int
	closes  int

	// steps 按发生顺序记录关键动作，用来断言"先收流量再停进程"。
	steps []string

	done chan struct{}
}

func newFakeCore(aliveAfterStart bool) *fakeCore {
	return &fakeCore{aliveAfterStart: aliveAfterStart, done: make(chan struct{})}
}

// runningFakeCore 返回一个已经在跑的替身，省去先 Start 一次。
func runningFakeCore() *fakeCore {
	f := newFakeCore(true)
	f.running = true
	return f
}

func (f *fakeCore) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.steps = append(f.steps, "start")
	f.running = f.aliveAfterStart
	f.done = make(chan struct{})
	if !f.running {
		// 起来即崩：退出通道当场关闭，正如真实内核自检失败时那样。
		close(f.done)
	}
	return nil
}

func (f *fakeCore) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, "stop")
	f.stopLocked()
	return nil
}

func (f *fakeCore) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	f.steps = append(f.steps, "close")
	f.stopLocked()
	return nil
}

// crash 模拟内核自己没了：没人调过 Stop/Close，退出通道却关闭了。
func (f *fakeCore) crash() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, "crash")
	f.stopLocked()
}

func (f *fakeCore) stopLocked() {
	f.running = false
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}

func (f *fakeCore) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *fakeCore) Done() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.done
}

func (f *fakeCore) GetErr() error          { return nil }
func (f *fakeCore) GetResult() string      { return "" }
func (f *fakeCore) GetVersion() string     { return "fake" }
func (f *fakeCore) GetConfig() core.Config { return staleConfig{} }

func (f *fakeCore) GetTraffic(bool) ([]*core.Traffic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, "traffic")
	return f.traffic, f.trafficErr
}

func (f *fakeCore) counts() (starts, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.closes
}

func (f *fakeCore) trace() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.steps...)
}

// staleConfig 与任何配置都不等价，逼 RestartCore 走真正的重启分支。
type staleConfig struct{}

func (staleConfig) Equals(core.Config) bool      { return false }
func (staleConfig) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

// restartClock 是可手动推进的时间源。
type restartClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *restartClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *restartClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// withCoreState 装一个干净的 coreState，并在用例结束后还原全局单例。
func withCoreState(t *testing.T, proc core.Core) *restartClock {
	t.Helper()

	clock := &restartClock{now: time.Unix(1700000000, 0)}
	backoff := NewBackoff(coreRestartBackoffBase, coreRestartBackoffMax)
	backoff.SetClock(clock.Now)

	previous := state
	state = &coreState{proc: proc, backoff: backoff}
	t.Cleanup(func() { state = previous })
	return clock
}

func markStartPending(t *testing.T) {
	t.Helper()
	state.mu.Lock()
	state.startPending = true
	state.mu.Unlock()
}

// TestSettleLastStartBacksOffWhenTheCoreDiesImmediately 是这次修的 bug。
//
// 配置非法时 sing-box 正常 fork、校验、然后自己退出。RestartCore 返回 nil，
// 于是旧实现每次都把退避复位——退避看着装好了，实际上从来没生效过。
func TestSettleLastStartBacksOffWhenTheCoreDiesImmediately(t *testing.T) {
	clock := withCoreState(t, newFakeCore(false))
	s := &CoreService{}
	markStartPending(t)

	s.settleLastStart()

	if state.backoff.Ready() {
		t.Fatal("a core that died right after start left the backoff wide open")
	}
	if !s.IsNeedRestartAndSetFalse() {
		t.Fatal("a crashed core must leave the restart flag raised")
	}
	if got := state.backoff.Current(); got != 2*coreRestartBackoffBase {
		t.Errorf("next delay = %v, want the base %v doubled", got, coreRestartBackoffBase)
	}

	clock.Advance(coreRestartBackoffBase - time.Millisecond)
	if state.backoff.Ready() {
		t.Error("a retry inside the backoff window was allowed")
	}
	clock.Advance(2 * time.Millisecond)
	if !state.backoff.Ready() {
		t.Error("the backoff never reopened after its window elapsed")
	}
}

// TestSettleLastStartResetsWhenTheCoreSurvives 保证用户改好配置之后
// 不必再等一个长窗口。
func TestSettleLastStartResetsWhenTheCoreSurvives(t *testing.T) {
	withCoreState(t, runningFakeCore())
	s := &CoreService{}

	state.backoff.Fail()
	state.backoff.Fail()
	markStartPending(t)

	s.settleLastStart()

	if !state.backoff.Ready() {
		t.Error("a healthy core must reopen the backoff immediately")
	}
	if got := state.backoff.Current(); got != coreRestartBackoffBase {
		t.Errorf("next delay = %v, want a reset to the base %v", got, coreRestartBackoffBase)
	}
	if s.IsNeedRestartAndSetFalse() {
		t.Error("a healthy core must not ask for another restart")
	}
}

// TestSettleLastStartIsANoopWithoutAPendingStart 防止每个 tick 都去动退避。
func TestSettleLastStartIsANoopWithoutAPendingStart(t *testing.T) {
	withCoreState(t, newFakeCore(false))
	s := &CoreService{}

	state.backoff.Fail()
	before := state.backoff.Current()
	s.settleLastStart()
	s.settleLastStart()

	if got := state.backoff.Current(); got != before {
		t.Errorf("the backoff moved from %v to %v without a start to settle", before, got)
	}
	if s.IsNeedRestartAndSetFalse() {
		t.Error("settling nothing raised the restart flag")
	}
}

// TestRestartCoreIfNeededKeepsTheFlagWhileBackingOff 保证退避期间标志不丢：
// 丢了的话内核就再也不会被拉起来。
func TestRestartCoreIfNeededKeepsTheFlagWhileBackingOff(t *testing.T) {
	proc := newFakeCore(false)
	withCoreState(t, proc)
	s := &CoreService{}

	state.backoff.Fail() // 进入退避窗口
	s.SetToNeedRestart()
	s.RestartCoreIfNeeded()

	if !s.IsNeedRestartAndSetFalse() {
		t.Fatal("the restart flag was dropped while the backoff window was open")
	}
	if _, closes := proc.counts(); closes != 0 {
		t.Errorf("the running core was replaced %d times inside the backoff window, want 0", closes)
	}
}

// TestStopCoreClearsThePendingStart 保证运维主动停机不会被误判成崩溃，
// 从而被自动拉起来。
func TestStopCoreClearsThePendingStart(t *testing.T) {
	withCoreState(t, runningFakeCore())
	s := &CoreService{}
	markStartPending(t)

	if err := s.StopCore(); err != nil {
		t.Fatalf("stop core: %v", err)
	}
	s.settleLastStart()

	if s.IsNeedRestartAndSetFalse() {
		t.Error("a deliberate stop was mistaken for a crash and scheduled a restart")
	}
	if !state.backoff.Ready() {
		t.Error("a deliberate stop must not consume the backoff budget")
	}
}

// TestRestartCoreIfNeededThrottlesACrashLoop 是端到端的那条：
// 每个 tick 都举起标志（探活任务发现内核不在时就是这么做的），内核每次
// 都起不来，15 分钟里真正的 fork 次数必须是个位数而不是每个 tick 一次。
//
// 计数取自 xui_core_restarts_total —— 那也正是运维在曲线上看到的那个数。
func TestRestartCoreIfNeededThrottlesACrashLoop(t *testing.T) {
	xtestutil.InitDB(t)
	clock := withCoreState(t, nil)
	s := &CoreService{}

	before := testutil.ToFloat64(metrics.CoreRestarts)

	const tick = 10 * time.Second
	const ticks = 90 // 15 分钟
	for i := 0; i < ticks; i++ {
		s.SetToNeedRestart()
		s.RestartCoreIfNeeded()
		clock.Advance(tick)
	}

	attempts := int(testutil.ToFloat64(metrics.CoreRestarts) - before)
	if attempts == 0 {
		t.Fatal("the panel never even tried to restart the core")
	}
	// 10s 起步、每次翻倍：15 分钟里最多 7 次。放宽到 10 留一点余量，
	// 但离"每个 tick 一次"的 90 差着一个数量级。
	if attempts > 10 {
		t.Errorf("a permanently broken core was restarted %d times in %v; the backoff is not engaging",
			attempts, time.Duration(ticks)*tick)
	}
	if !s.IsNeedRestartAndSetFalse() {
		t.Error("the panel gave up on restarting the core entirely")
	}
}
