package service

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"x-ui/core"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/testutil"
)

func TestFoldTrafficDropsNoiseAndMergesDuplicates(t *testing.T) {
	in := []*core.Traffic{
		{IsInbound: true, Tag: "b", Up: 1, Down: 2},
		{IsInbound: true, Tag: "a", Up: 10, Down: 0},
		// 同一个键出现两次：必须合并，否则 SQL 的 CASE 只会命中第一个分支。
		{IsInbound: true, Tag: "a", Up: 5, Down: 7},
		// 零增量、空 tag、别的维度：全部丢弃。
		{IsInbound: true, Tag: "idle", Up: 0, Down: 0},
		{IsInbound: true, Tag: "", Up: 3, Down: 3},
		{IsUser: true, Tag: "alice", Up: 100, Down: 100},
		nil,
	}

	got := foldTraffic(in, isInboundTraffic)
	want := []trafficDelta{
		{Key: "a", Up: 15, Down: 7},
		{Key: "b", Up: 1, Down: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("folded %d deltas (%+v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("delta %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if users := foldTraffic(in, isUserTraffic); len(users) != 1 || users[0].Key != "alice" {
		t.Errorf("user dimension folded to %+v, want just alice", users)
	}
	if foldTraffic(nil, isInboundTraffic) != nil {
		t.Error("an empty batch must fold to nil")
	}
}

// TestAddTrafficIsOneStatementPerBatch 是这次改动的核心断言。
//
// 历史实现对每个 tag 发两条 UPDATE（up 一条、down 一条）。流量任务
// 每 10 秒跑一次，一台有几百个客户端的机器就是每 10 秒上千条语句，
// 全压在同一个写事务里，期间面板的任何写请求都在等锁。
func TestAddTrafficIsOneStatementPerBatch(t *testing.T) {
	testutil.InitDB(t)

	const clients = 25
	inbound := seedInbound(t, model.VMess, 30600, `{"users":[]}`)
	cs := &ClientService{}
	traffics := make([]*core.Traffic, 0, clients+1)
	traffics = append(traffics, &core.Traffic{IsInbound: true, Tag: inbound.Tag, Up: 1, Down: 1})
	for i := 0; i < clients; i++ {
		email := "batch" + strconv.Itoa(i) + "@x"
		if err := cs.AddClient(&model.Client{
			InboundId: inbound.Id, Email: email, Enable: true,
			UUID: "uuid-" + strconv.Itoa(i),
		}); err != nil {
			t.Fatalf("seed client %d: %v", i, err)
		}
		traffics = append(traffics, &core.Traffic{IsUser: true, Tag: email, Up: int64(i + 1), Down: int64(i + 2)})
	}

	stop := countUpdates(t)
	if err := cs.AddTraffic(traffics); err != nil {
		t.Fatalf("add client traffic: %v", err)
	}
	if n := stop(); n != 1 {
		t.Errorf("writing %d client counters took %d UPDATE statements, want 1", clients, n)
	}

	for i := 0; i < clients; i++ {
		got := loadClientByEmail(t, "batch"+strconv.Itoa(i)+"@x")
		if got.Up != int64(i+1) || got.Down != int64(i+2) {
			t.Fatalf("client %d up/down = %d/%d, want %d/%d", i, got.Up, got.Down, i+1, i+2)
		}
		if got.LastSeen == 0 {
			t.Fatalf("client %d has no last_seen stamp", i)
		}
	}
}

// TestAddTrafficChunksLargeBatches 确认超过一个 chunk 的批次依然正确，
// 且语句数按 chunk 数线性增长而不是按键数。
func TestAddTrafficChunksLargeBatches(t *testing.T) {
	testutil.InitDB(t)

	const inbounds = trafficBatchSize + 5
	is := &InboundService{}
	traffics := make([]*core.Traffic, 0, inbounds)
	for i := 0; i < inbounds; i++ {
		ib := seedInbound(t, model.VMess, 31000+i, `{"users":[]}`)
		traffics = append(traffics, &core.Traffic{IsInbound: true, Tag: ib.Tag, Up: 7, Down: 11})
	}

	stop := countUpdates(t)
	if err := is.AddTraffic(traffics); err != nil {
		t.Fatalf("add inbound traffic: %v", err)
	}
	if n := stop(); n != 2 {
		t.Errorf("%d inbounds took %d UPDATE statements, want 2 chunks", inbounds, n)
	}

	all, err := is.GetAllInbounds()
	if err != nil {
		t.Fatalf("list inbounds: %v", err)
	}
	for _, ib := range all {
		if ib.Up != 7 || ib.Down != 11 {
			t.Fatalf("inbound %s up/down = %d/%d, want 7/11", ib.Tag, ib.Up, ib.Down)
		}
	}
}

// TestAddTrafficAccumulates 保证增量是累加而不是覆盖：
// 计数器是 reset-on-read 的，每一批都只是"上一个 10 秒的新增"。
func TestAddTrafficAccumulates(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30700, `{"users":[]}`)
	is := &InboundService{}

	for i := 0; i < 3; i++ {
		if err := is.AddTraffic([]*core.Traffic{{IsInbound: true, Tag: inbound.Tag, Up: 100, Down: 200}}); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	got, err := is.GetInbound(inbound.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if got.Up != 300 || got.Down != 600 {
		t.Errorf("up/down = %d/%d, want 300/600 after three batches", got.Up, got.Down)
	}
}

// TestAddTrafficLeavesUnrelatedRowsAlone 覆盖合成语句的 ELSE 0 分支：
// WHERE 里没点到名的行一个字节都不该动。
func TestAddTrafficLeavesUnrelatedRowsAlone(t *testing.T) {
	testutil.InitDB(t)
	touched := seedInbound(t, model.VMess, 30800, `{"users":[]}`)
	untouched := seedInbound(t, model.VMess, 30801, `{"users":[]}`)
	is := &InboundService{}

	if err := is.AddTraffic([]*core.Traffic{{IsInbound: true, Tag: touched.Tag, Up: 5, Down: 5}}); err != nil {
		t.Fatalf("add traffic: %v", err)
	}

	got, err := is.GetInbound(untouched.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Up != 0 || got.Down != 0 {
		t.Errorf("an inbound outside the batch moved to %d/%d", got.Up, got.Down)
	}
}

// TestAddTrafficDimensionsDoNotCrossOver 固化"两张表各记一次"的语义。
//
// 入站维度与用户维度携带的是同一批字节。把它们写进各自的表是设计，
// 把用户的字节加到入站行上（或反过来）是 bug。
func TestAddTrafficDimensionsDoNotCrossOver(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30900, `{"users":[]}`)
	cs := &ClientService{}
	if err := cs.AddClient(&model.Client{
		InboundId: inbound.Id, Email: "cross@x", Enable: true, UUID: "u1",
	}); err != nil {
		t.Fatalf("seed client: %v", err)
	}

	batch := []*core.Traffic{
		{IsInbound: true, Tag: inbound.Tag, Up: 1000, Down: 2000},
		{IsUser: true, Tag: "cross@x", Up: 400, Down: 600},
	}
	is := &InboundService{}
	if err := is.AddTraffic(batch); err != nil {
		t.Fatalf("inbound side: %v", err)
	}
	if err := cs.AddTraffic(batch); err != nil {
		t.Fatalf("client side: %v", err)
	}

	gotInbound, err := is.GetInbound(inbound.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if gotInbound.Up != 1000 || gotInbound.Down != 2000 {
		t.Errorf("inbound up/down = %d/%d, want exactly the inbound-dimension counters 1000/2000",
			gotInbound.Up, gotInbound.Down)
	}
	gotClient := loadClientByEmail(t, "cross@x")
	if gotClient.Up != 400 || gotClient.Down != 600 {
		t.Errorf("client up/down = %d/%d, want exactly the user-dimension counters 400/600",
			gotClient.Up, gotClient.Down)
	}
}

// TestAddTrafficReportsCommitFailures 覆盖历史上被吞掉的那类错误。
//
// 计数器已经在内核侧清零，写库失败必须让调用方知道——CoreTrafficJob
// 靠这个返回值决定是否把增量留到下一轮重投。
func TestAddTrafficReportsCommitFailures(t *testing.T) {
	testutil.InitDB(t)
	if err := database.GetDB().Migrator().DropTable(&model.Inbound{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	err := (&InboundService{}).AddTraffic([]*core.Traffic{{IsInbound: true, Tag: "gone", Up: 1, Down: 1}})
	if err == nil {
		t.Fatal("AddTraffic swallowed a database failure; the job would drop the batch silently")
	}
}

func loadClientByEmail(t *testing.T, email string) *model.Client {
	t.Helper()
	client := &model.Client{}
	if err := database.GetDB().Model(model.Client{}).Where("email = ?", email).First(client).Error; err != nil {
		t.Fatalf("load client %s: %v", email, err)
	}
	return client
}

// countUpdates 统计区间内执行了多少条 UPDATE 语句。
//
// 通过 gorm 回调实现：不需要驱动层的钩子，也不依赖日志格式。
// 同时挂在 Update 与 Raw 两条链上——批量入账走的是 Exec（Raw），
// 而别处的写仍然走 Updates（Update）。
func countUpdates(t *testing.T) func() int {
	t.Helper()

	db := database.GetDB()
	const name = "test:count_updates"
	var mu sync.Mutex
	count := 0
	tally := func(tx *gorm.DB) {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(tx.Statement.SQL.String())), "UPDATE") {
			mu.Lock()
			count++
			mu.Unlock()
		}
	}
	if err := db.Callback().Update().After("gorm:update").Register(name, tally); err != nil {
		t.Fatalf("register update callback: %v", err)
	}
	if err := db.Callback().Raw().After("gorm:raw").Register(name, tally); err != nil {
		t.Fatalf("register raw callback: %v", err)
	}
	return func() int {
		if err := db.Callback().Update().Remove(name); err != nil {
			t.Fatalf("remove update callback: %v", err)
		}
		if err := db.Callback().Raw().Remove(name); err != nil {
			t.Fatalf("remove raw callback: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		return count
	}
}
