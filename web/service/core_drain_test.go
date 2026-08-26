package service

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"x-ui/core"
	"x-ui/database/model"
	xtestutil "x-ui/testutil"
)

// drainFixture 建一个入站与一个客户端，并给内核替身编排好待收的计数器。
func drainFixture(t *testing.T) (*gorm.DB, *fakeCore) {
	t.Helper()

	db, _ := xtestutil.InitDB(t)
	if err := db.Create(&model.Inbound{
		Tag: "inbound-443", Protocol: model.VMess, Port: 443,
		Enable: true, Settings: "{}", Up: 1, Down: 2,
	}).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	if err := db.Create(&model.Client{
		InboundId: 1, Email: "alice@example.com", Enable: true,
		SubToken: "token-alice", Up: 5, Down: 6,
	}).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}

	proc := runningFakeCore()
	proc.traffic = []*core.Traffic{
		{IsInbound: true, Tag: "inbound-443", Up: 100, Down: 200},
		{IsUser: true, Tag: "alice@example.com", Up: 30, Down: 40},
	}
	return db, proc
}

func assertDrained(t *testing.T, db *gorm.DB) {
	t.Helper()

	var inbound model.Inbound
	if err := db.Where("tag = ?", "inbound-443").First(&inbound).Error; err != nil {
		t.Fatalf("read inbound back: %v", err)
	}
	if inbound.Up != 101 || inbound.Down != 202 {
		t.Errorf("inbound = %d/%d, want the pre-stop counters folded in (101/202)", inbound.Up, inbound.Down)
	}

	var client model.Client
	if err := db.Where("email = ?", "alice@example.com").First(&client).Error; err != nil {
		t.Fatalf("read client back: %v", err)
	}
	if client.Up != 35 || client.Down != 46 {
		t.Errorf("client = %d/%d, want the pre-stop counters folded in (35/46)", client.Up, client.Down)
	}
}

// indexOf 返回动作在 trace 中的位置，缺失时返回 -1。
func indexOf(trace []string, step string) int {
	for i, s := range trace {
		if s == step {
			return i
		}
	}
	return -1
}

// TestStopCorePersistsTrafficBeforeKillingTheProcess 是这一轮修的窗口。
//
// sing-box 的计数器是 reset-on-read 的，并且只活在子进程的内存里：进程一死，
// 上一次流量任务（10 秒一轮）之后攒下的字节就再也拿不回来了。停机前不收一次，
// 每次重启内核都等于给所有在线用户免掉最多一整轮的配额。
func TestStopCorePersistsTrafficBeforeKillingTheProcess(t *testing.T) {
	db, proc := drainFixture(t)
	withCoreState(t, proc)
	s := &CoreService{}

	if err := s.StopCore(); err != nil {
		t.Fatalf("stop core: %v", err)
	}

	assertDrained(t, db)

	trace := proc.trace()
	queried, stopped := indexOf(trace, "traffic"), indexOf(trace, "stop")
	if queried < 0 {
		t.Fatalf("the core was stopped without ever being asked for its counters: %v", trace)
	}
	if stopped < 0 || queried > stopped {
		t.Errorf("counters were read at step %d but the process was stopped at step %d: %v",
			queried, stopped, trace)
	}
}

// TestRestartCoreDrainsTheOutgoingProcess 覆盖另一半入口。改一个入站、
// 一个客户端到期——面板每天走的是重启这条路，不是停机那条。
func TestRestartCoreDrainsTheOutgoingProcess(t *testing.T) {
	db, proc := drainFixture(t)
	withCoreState(t, proc)
	s := &CoreService{}

	// 起新进程一定会失败（测试环境里没有内核二进制），无所谓：
	// 要验的是旧进程在被换掉之前有没有被收干净。
	_ = s.RestartCore(true)

	assertDrained(t, db)

	trace := proc.trace()
	queried, closed := indexOf(trace, "traffic"), indexOf(trace, "close")
	if queried < 0 {
		t.Fatalf("the outgoing core was replaced without being drained: %v", trace)
	}
	if closed < 0 || queried > closed {
		t.Errorf("counters were read at step %d but the process was released at step %d: %v",
			queried, closed, trace)
	}
}

// TestDrainDoesNotBlockAStopWhenTheStatsAPIIsDown 保证收流量只是尽力而为：
// gRPC 连接早就断了的时候，停机流程不能跟着卡住或失败。
func TestDrainDoesNotBlockAStopWhenTheStatsAPIIsDown(t *testing.T) {
	xtestutil.InitDB(t)
	proc := runningFakeCore()
	proc.trafficErr = errors.New("stats client not initialized")
	withCoreState(t, proc)
	s := &CoreService{}

	if err := s.StopCore(); err != nil {
		t.Fatalf("a failed drain must not fail the stop: %v", err)
	}
	if proc.IsRunning() {
		t.Error("the core is still running after a stop whose drain failed")
	}
}

// TestDrainIsSkippedWhenTheCoreIsAlreadyGone 挡住一次没有意义的 gRPC 往返：
// 进程都没了，计数器跟着一起没了，问也白问。
func TestDrainIsSkippedWhenTheCoreIsAlreadyGone(t *testing.T) {
	xtestutil.InitDB(t)
	proc := newFakeCore(false) // 从未启动，IsRunning 为 false
	withCoreState(t, proc)
	s := &CoreService{}

	_ = s.RestartCore(true)

	if got := indexOf(proc.trace(), "traffic"); got >= 0 {
		t.Errorf("a dead core was still queried for traffic: %v", proc.trace())
	}
}

// TestCoreExitWatcherRaisesTheRestartFlagAtOnce 是"不用等 30s×2"的那一半。
//
// 以前内核崩了要靠 CheckCoreRunningJob 连中两轮探活才会举旗；现在子进程的
// 退出通道一关就举。
func TestCoreExitWatcherRaisesTheRestartFlagAtOnce(t *testing.T) {
	proc := runningFakeCore()
	withCoreState(t, proc)
	s := &CoreService{}
	s.IsNeedRestartAndSetFalse()

	proc.crash()
	s.watchCoreExit(proc, proc.Done())

	if !s.IsNeedRestartAndSetFalse() {
		t.Error("a core that died on its own did not schedule a restart")
	}
}

// TestCoreExitWatcherIgnoresADeliberateStop 是"不许 flap"的那一半：
// 面板自己按下的停止不能被自己的监视器再拉起来。
func TestCoreExitWatcherIgnoresADeliberateStop(t *testing.T) {
	xtestutil.InitDB(t)
	proc := runningFakeCore()
	withCoreState(t, proc)
	s := &CoreService{}
	s.IsNeedRestartAndSetFalse()

	if err := s.StopCore(); err != nil {
		t.Fatalf("stop core: %v", err)
	}
	s.watchCoreExit(proc, proc.Done())

	if s.IsNeedRestartAndSetFalse() {
		t.Error("a deliberate stop was resurrected by the exit watcher")
	}
}

// TestCoreExitWatcherIgnoresAReplacedInstance 覆盖重启期间的时序：
// 旧进程的退出是 RestartCore 亲手造成的，不该被当成意外。
func TestCoreExitWatcherIgnoresAReplacedInstance(t *testing.T) {
	old := runningFakeCore()
	withCoreState(t, old)
	s := &CoreService{}
	s.IsNeedRestartAndSetFalse()

	state.mu.Lock()
	state.proc = runningFakeCore()
	state.mu.Unlock()

	old.crash()
	s.watchCoreExit(old, old.Done())

	if s.IsNeedRestartAndSetFalse() {
		t.Error("the exit of an already-replaced instance scheduled a restart")
	}
}
