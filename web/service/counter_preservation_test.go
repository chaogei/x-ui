package service

import (
	"errors"
	"testing"

	"x-ui/core"
	"x-ui/database/model"
	"x-ui/testutil"
)

// TestUpdateClientDuringTrafficWrites 是客户端侧的同一条时序：UpdateClient
// 早就忽略请求里的 up/down，但它读出整行再 Save 回去，照样会覆盖这中间
// 入账的字节。
func TestUpdateClientDuringTrafficWrites(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 31020, `{"users":[]}`)
	s := &ClientService{}

	client := &model.Client{
		InboundId: inbound.Id,
		Email:     "counters@example.com",
		Enable:    true,
		UUID:      "11111111-1111-1111-1111-111111111111",
	}
	if err := s.AddClient(client); err != nil {
		t.Fatalf("add client: %v", err)
	}

	const rounds = 20
	traffic := make(chan error, 1)
	go func() {
		for i := 0; i < rounds; i++ {
			if err := s.AddTraffic([]*core.Traffic{{
				IsUser: true, Tag: client.Email, Up: 3, Down: 4,
			}}); err != nil {
				traffic <- err
				return
			}
		}
		traffic <- nil
	}()

	for i := 0; i < rounds; i++ {
		if err := s.UpdateClient(&model.Client{
			Id:     client.Id,
			Email:  client.Email,
			Enable: true,
			UUID:   client.UUID,
			Total:  int64(1000 + i),
		}); err != nil {
			t.Fatalf("update client: %v", err)
		}
	}
	if err := <-traffic; err != nil {
		t.Fatalf("add traffic: %v", err)
	}

	reloaded, err := s.GetClient(client.Id)
	if err != nil {
		t.Fatalf("reload client: %v", err)
	}
	if reloaded.Up != 3*rounds || reloaded.Down != 4*rounds {
		t.Errorf("up/down = %d/%d, want %d/%d — edits swallowed traffic",
			reloaded.Up, reloaded.Down, 3*rounds, 4*rounds)
	}
}

// TestUpdateInboundKeepsLiveCounters 是这条修复的核心断言：一次带着陈旧
// up/down 的更新（正是前端表单会提交的东西）不许碰计数器。
//
// 复现的是真实时序：管理员打开编辑框（看到 100/200），流量任务在这期间又记了
// 一批字节（变成 1100/1200），管理员点保存。旧实现会把行写回 100/200，
// 那 2000 字节就此消失。
func TestUpdateInboundKeepsLiveCounters(t *testing.T) {
	testutil.InitDB(t)
	s := &InboundService{}
	inbound := seedInbound(t, model.VMess, 31010, `{"users":[]}`)

	if err := s.AddTraffic([]*core.Traffic{{
		IsInbound: true, Tag: inbound.Tag, Up: 1100, Down: 1200,
	}}); err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	// 表单快照：页面加载时看到的是更早的计数。
	form := &model.Inbound{
		Id:       inbound.Id,
		UserId:   inbound.UserId,
		Enable:   true,
		Remark:   "renamed",
		Port:     inbound.Port,
		Protocol: inbound.Protocol,
		Settings: inbound.Settings,
		Total:    5000,
		Up:       100,
		Down:     200,
	}
	if err := s.UpdateInbound(form); err != nil {
		t.Fatalf("update inbound: %v", err)
	}

	reloaded, err := s.GetInbound(inbound.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if reloaded.Up != 1100 || reloaded.Down != 1200 {
		t.Errorf("up/down = %d/%d after an edit, want the live 1100/1200 — the form snapshot clobbered them",
			reloaded.Up, reloaded.Down)
	}
	// 其余可编辑字段照常生效。
	if reloaded.Remark != "renamed" || reloaded.Total != 5000 {
		t.Errorf("remark/total = %q/%d, want \"renamed\"/5000", reloaded.Remark, reloaded.Total)
	}
}

// TestResetInboundTraffic 覆盖专用清零路径，包括不存在的 id。
func TestResetInboundTraffic(t *testing.T) {
	testutil.InitDB(t)
	s := &InboundService{}
	inbound := seedInbound(t, model.VMess, 31011, `{"users":[]}`)

	if err := s.AddTraffic([]*core.Traffic{{
		IsInbound: true, Tag: inbound.Tag, Up: 7, Down: 11,
	}}); err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	if err := s.ResetTraffic(inbound.Id); err != nil {
		t.Fatalf("reset traffic: %v", err)
	}

	reloaded, err := s.GetInbound(inbound.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if reloaded.Up != 0 || reloaded.Down != 0 {
		t.Errorf("up/down = %d/%d after a reset, want 0/0", reloaded.Up, reloaded.Down)
	}
	// 清零只碰计数器。
	if reloaded.Port != inbound.Port || reloaded.Tag != inbound.Tag {
		t.Errorf("reset rewrote port/tag: %d/%q", reloaded.Port, reloaded.Tag)
	}

	if err := s.ResetTraffic(inbound.Id + 999); !errors.Is(err, ErrInboundNotFound) {
		t.Errorf("resetting a missing inbound returned %v, want ErrInboundNotFound", err)
	}
}

// TestUpdateInboundDuringTrafficWrites 让编辑与流量入账并发跑：无论交错如何，
// 最终计数都必须等于全部增量之和。
func TestUpdateInboundDuringTrafficWrites(t *testing.T) {
	testutil.InitDB(t)
	s := &InboundService{}
	inbound := seedInbound(t, model.VMess, 31012, `{"users":[]}`)

	// UpdateInbound 会把 tag 重算成 inbound-<port>-<protocol>，先落一次，
	// 免得流量因为 tag 对不上而被静默丢弃。
	form0 := &model.Inbound{
		Id: inbound.Id, UserId: inbound.UserId, Enable: true, Remark: "edit",
		Port: inbound.Port, Protocol: inbound.Protocol, Settings: inbound.Settings,
	}
	if err := s.UpdateInbound(form0); err != nil {
		t.Fatalf("canonicalise tag: %v", err)
	}
	settled, err := s.GetInbound(inbound.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}

	const rounds = 20
	traffic := make(chan error, 1)
	go func() {
		for i := 0; i < rounds; i++ {
			if err := s.AddTraffic([]*core.Traffic{{
				IsInbound: true, Tag: settled.Tag, Up: 1, Down: 2,
			}}); err != nil {
				traffic <- err
				return
			}
		}
		traffic <- nil
	}()

	for i := 0; i < rounds; i++ {
		form := &model.Inbound{
			Id:       inbound.Id,
			UserId:   inbound.UserId,
			Enable:   true,
			Remark:   "edit",
			Port:     inbound.Port,
			Protocol: inbound.Protocol,
			Settings: inbound.Settings,
			// 一份从未刷新过的快照。
			Up:   0,
			Down: 0,
		}
		if err := s.UpdateInbound(form); err != nil {
			t.Fatalf("update inbound: %v", err)
		}
	}
	if err := <-traffic; err != nil {
		t.Fatalf("add traffic: %v", err)
	}

	reloaded, err := s.GetInbound(inbound.Id)
	if err != nil {
		t.Fatalf("reload inbound after the concurrent phase: %v", err)
	}
	if reloaded.Up != rounds || reloaded.Down != 2*rounds {
		t.Errorf("up/down = %d/%d, want %d/%d — edits swallowed traffic",
			reloaded.Up, reloaded.Down, rounds, 2*rounds)
	}
}
