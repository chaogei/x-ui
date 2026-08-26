package job

import (
	"strconv"
	"testing"

	"x-ui/core"
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
