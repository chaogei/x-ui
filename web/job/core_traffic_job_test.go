package job

import (
	"strconv"
	"testing"

	"gorm.io/gorm"

	"x-ui/core"
	"x-ui/database/model"
	"x-ui/testutil"
)

func index(t *testing.T, list []*core.Traffic) map[string]core.Traffic {
	t.Helper()
	out := make(map[string]core.Traffic, len(list))
	for _, item := range list {
		if _, dup := out[item.Tag]; dup {
			t.Fatalf("tag %q appears twice in the merged batch", item.Tag)
		}
		out[item.Tag] = *item
	}
	return out
}

// TestCarryOverRetriesUnwrittenBytes 是 reset-on-read 语义的核心保护。
//
// QueryStats(reset=true) 一返回，那批字节就只存在于面板进程的内存里。
// 上一轮写库失败时把增量丢掉，等于让用户白嫖一段流量。
func TestCarryOverRetriesUnwrittenBytes(t *testing.T) {
	pending := []*core.Traffic{
		{IsUser: true, Tag: "alice", Up: 100, Down: 200},
		{IsUser: true, Tag: "bob", Up: 1, Down: 1},
	}
	fresh := []*core.Traffic{
		{IsUser: true, Tag: "alice", Up: 10, Down: 20},
		{IsUser: true, Tag: "carol", Up: 5, Down: 5},
		{IsInbound: true, Tag: "inbound-443-vmess", Up: 999, Down: 999},
	}

	merged := index(t, carryOver(pending, fresh, func(t *core.Traffic) bool { return t.IsUser }))
	if len(merged) != 3 {
		t.Fatalf("merged %d users, want alice/bob/carol", len(merged))
	}
	if got := merged["alice"]; got.Up != 110 || got.Down != 220 {
		t.Errorf("alice = %d/%d, want the pending and the fresh batch summed (110/220)", got.Up, got.Down)
	}
	if got := merged["bob"]; got.Up != 1 || got.Down != 1 {
		t.Errorf("bob = %d/%d, want the retried 1/1", got.Up, got.Down)
	}
	if _, ok := merged["inbound-443-vmess"]; ok {
		t.Error("an inbound counter leaked into the user dimension")
	}
}

// TestCarryOverCopiesFreshEntries 防止把内核响应里的指针存进跨轮的缓冲：
// 下一轮再累加时会写花本轮的响应对象。
func TestCarryOverCopiesFreshEntries(t *testing.T) {
	fresh := []*core.Traffic{{IsInbound: true, Tag: "a", Up: 10, Down: 10}}
	kept := carryOver(nil, fresh, func(t *core.Traffic) bool { return t.IsInbound })
	if len(kept) != 1 {
		t.Fatalf("kept %d entries, want 1", len(kept))
	}
	if kept[0] == fresh[0] {
		t.Fatal("the buffer aliases the core response instead of copying it")
	}

	carryOver(kept, fresh, func(t *core.Traffic) bool { return t.IsInbound })
	if fresh[0].Up != 10 {
		t.Errorf("merging mutated the core response: up = %d, want 10", fresh[0].Up)
	}
}

func TestCarryOverDropsIdleCounters(t *testing.T) {
	fresh := []*core.Traffic{
		{IsInbound: true, Tag: "idle", Up: 0, Down: 0},
		{IsInbound: true, Tag: "", Up: 1, Down: 1},
		{IsInbound: true, Tag: "busy", Up: 1, Down: 0},
		nil,
	}
	got := carryOver(nil, fresh, func(t *core.Traffic) bool { return t.IsInbound })
	if len(got) != 1 || got[0].Tag != "busy" {
		t.Errorf("kept %+v, want only the counter that actually moved", got)
	}
	if carryOver(nil, nil, func(t *core.Traffic) bool { return true }) != nil {
		t.Error("an empty batch must merge to nil")
	}
}

// TestBoundPendingKeepsTheHeaviestCounters 覆盖"数据库一直写不进去"的退化路径。
// 缓冲必须封顶，而被牺牲的应该是几个字节的零头，不是大流量用户。
func TestBoundPendingKeepsTheHeaviestCounters(t *testing.T) {
	pending := make([]*core.Traffic, 0, maxPendingTraffic+10)
	for i := 0; i < maxPendingTraffic+10; i++ {
		pending = append(pending, &core.Traffic{IsUser: true, Tag: "u" + strconv.Itoa(i), Up: int64(i), Down: 0})
	}

	got := boundPending(pending, "client")
	if len(got) != maxPendingTraffic {
		t.Fatalf("buffer holds %d entries, want the %d cap", len(got), maxPendingTraffic)
	}
	var smallest int64 = 1 << 62
	for _, item := range got {
		if item.Up < smallest {
			smallest = item.Up
		}
	}
	if smallest < 10 {
		t.Errorf("kept a %d-byte counter while dropping heavier ones", smallest)
	}

	short := []*core.Traffic{{IsUser: true, Tag: "a", Up: 1}}
	if len(boundPending(short, "client")) != 1 {
		t.Error("a buffer below the cap must be kept verbatim")
	}
}

// TestFlushWritesTheRetryBufferOnShutdown 覆盖停机这条出口。
//
// 缓冲里的增量之所以还在内存里，是因为之前有一轮写库失败了；面板一停就
// 再没有下一轮去重投它们。不冲这一次，用户白嫖的就不止一个周期。
func TestFlushWritesTheRetryBufferOnShutdown(t *testing.T) {
	db := seedLedger(t)

	j := bufferedJob()
	j.Flush()

	assertLedger(t, db, 101, 202, 35, 46)

	// 冲过的增量必须清掉：再冲一次不能把同一批字节记第二遍。
	j.Flush()
	assertLedger(t, db, 101, 202, 35, 46)
}

// TestRunRetriesTheBufferWhileTheCoreIsDown 覆盖内核不在跑的那一轮。
//
// 缓冲里的增量是之前写库失败留下的，与内核此刻的死活无关。以前这一轮
// 在探活门那里就掉头走了，于是内核停多久这些字节就卡多久——面板先停机
// 就全丢了。（测试进程里从来没有内核，所以这条路径是默认路径。）
func TestRunRetriesTheBufferWhileTheCoreIsDown(t *testing.T) {
	db := seedLedger(t)

	j := bufferedJob()
	j.Run()

	assertLedger(t, db, 101, 202, 35, 46)
	if len(j.pendingInbound) != 0 || len(j.pendingUser) != 0 {
		t.Errorf("the buffer still holds %d inbound and %d client entries after a successful write",
			len(j.pendingInbound), len(j.pendingUser))
	}
}

// seedLedger 建一个入站与一个客户端，各自带一点历史流量。
func seedLedger(t *testing.T) *gorm.DB {
	t.Helper()

	db, _ := testutil.InitDB(t)
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
	return db
}

// bufferedJob 返回一个重投缓冲里攒着两条增量的任务。
func bufferedJob() *CoreTrafficJob {
	j := NewCoreTrafficJob()
	j.pendingInbound = []*core.Traffic{{IsInbound: true, Tag: "inbound-443", Up: 100, Down: 200}}
	j.pendingUser = []*core.Traffic{{IsUser: true, Tag: "alice@example.com", Up: 30, Down: 40}}
	return j
}

func assertLedger(t *testing.T, db *gorm.DB, inUp, inDown, cliUp, cliDown int64) {
	t.Helper()

	var inbound model.Inbound
	if err := db.Where("tag = ?", "inbound-443").First(&inbound).Error; err != nil {
		t.Fatalf("read inbound back: %v", err)
	}
	if inbound.Up != inUp || inbound.Down != inDown {
		t.Errorf("inbound = %d/%d, want %d/%d", inbound.Up, inbound.Down, inUp, inDown)
	}

	var client model.Client
	if err := db.Where("email = ?", "alice@example.com").First(&client).Error; err != nil {
		t.Fatalf("read client back: %v", err)
	}
	if client.Up != cliUp || client.Down != cliDown {
		t.Errorf("client = %d/%d, want %d/%d", client.Up, client.Down, cliUp, cliDown)
	}
}

// TestFlushYieldsToAnInFlightRun 保证冲刷不会和一轮还没跑完的常规任务
// 抢同一批增量——cron 排空超时时这是可能发生的。
func TestFlushYieldsToAnInFlightRun(t *testing.T) {
	testutil.InitDB(t)

	j := NewCoreTrafficJob()
	j.pendingUser = []*core.Traffic{{IsUser: true, Tag: "alice@example.com", Up: 30, Down: 40}}
	j.running.Store(true)

	j.Flush()

	if len(j.pendingUser) != 1 {
		t.Error("the retry buffer was consumed while a regular tick was still in flight")
	}
}
