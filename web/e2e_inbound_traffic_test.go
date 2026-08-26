package web

import (
	"strconv"
	"sync"
	"testing"

	"x-ui/core"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/web/service"
)

// seedInboundWithTraffic 建一条入站并给它记上一笔流量，返回入站行。
func seedInboundWithTraffic(t *testing.T, p *panel, port int, up, down int64) *model.Inbound {
	t.Helper()

	if msg := p.decode(p.postForm("xui/inbound/add", inboundForm(port, "vmess", vmessSettings))); !msg.Success {
		t.Fatalf("add inbound: %s", msg.Msg)
	}
	created := p.listInbounds()[0]
	if err := (&service.InboundService{}).AddTraffic([]*core.Traffic{{
		IsInbound: true, Tag: created.Tag, Up: up, Down: down,
	}}); err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	return created
}

func reloadInbound(t *testing.T, id int) *model.Inbound {
	t.Helper()

	row := &model.Inbound{}
	if err := database.GetDB().First(row, id).Error; err != nil {
		t.Fatalf("reload inbound %d: %v", id, err)
	}
	return row
}

// TestE2EInboundEditKeepsCounters 走完整的 HTTP 栈验证 H3：编辑入站不许
// 把表单里的陈旧 up/down 写回库。
func TestE2EInboundEditKeepsCounters(t *testing.T) {
	p := newPanel(t)
	p.login()

	created := seedInboundWithTraffic(t, p, 20500, 4096, 8192)

	// 前端提交的是页面加载那一刻的快照。
	form := inboundForm(20500, "vmess", vmessSettings)
	form.Set("remark", "renamed")
	form.Set("up", "100")
	form.Set("down", "200")
	if msg := p.decode(p.postForm("xui/inbound/update/"+strconv.Itoa(created.Id), form)); !msg.Success {
		t.Fatalf("update inbound: %s", msg.Msg)
	}

	row := reloadInbound(t, created.Id)
	if row.Up != 4096 || row.Down != 8192 {
		t.Errorf("up/down = %d/%d after an edit, want the live 4096/8192", row.Up, row.Down)
	}
	if row.Remark != "renamed" {
		t.Errorf("remark = %q, want the edit to have landed", row.Remark)
	}
}

// TestE2EInboundResetTraffic 覆盖专用清零接口。
func TestE2EInboundResetTraffic(t *testing.T) {
	p := newPanel(t)
	p.login()

	created := seedInboundWithTraffic(t, p, 20501, 4096, 8192)

	if msg := p.decode(p.postForm("xui/inbound/resetTraffic/"+strconv.Itoa(created.Id), nil)); !msg.Success {
		t.Fatalf("reset inbound traffic: %s", msg.Msg)
	}
	if row := reloadInbound(t, created.Id); row.Up != 0 || row.Down != 0 {
		t.Errorf("up/down = %d/%d after the reset, want 0/0", row.Up, row.Down)
	}

	// 不存在的入站要报错，而不是静默成功。
	if msg := p.decode(p.postForm("xui/inbound/resetTraffic/9999", nil)); msg.Success {
		t.Error("resetting a nonexistent inbound reported success")
	}
	// 未登录的请求进不来。
	other := newPanelClient(t, p)
	if msg := other.decode(other.postForm("xui/inbound/resetTraffic/"+strconv.Itoa(created.Id), nil)); msg.Success {
		t.Error("an anonymous client reset an inbound's traffic")
	}
}

// TestE2EInboundLegacyZeroPayloadStillResets 固定住过渡期的兼容行为：
// 仓库里内嵌的前端产物仍然靠"提交 up=0&down=0"来清零。这个用例连同
// legacyCounterReset 一起，在前端改调 /inbound/resetTraffic 之后可以删掉。
func TestE2EInboundLegacyZeroPayloadStillResets(t *testing.T) {
	p := newPanel(t)
	p.login()

	created := seedInboundWithTraffic(t, p, 20502, 4096, 8192)

	form := inboundForm(20502, "vmess", vmessSettings)
	form.Set("up", "0")
	form.Set("down", "0")
	if msg := p.decode(p.postForm("xui/inbound/update/"+strconv.Itoa(created.Id), form)); !msg.Success {
		t.Fatalf("legacy reset: %s", msg.Msg)
	}
	if row := reloadInbound(t, created.Id); row.Up != 0 || row.Down != 0 {
		t.Errorf("up/down = %d/%d, want the legacy zeroing payload to still reset", row.Up, row.Down)
	}
}

// TestE2EInboundEditWithoutCounterFieldsKeepsThem 是上面那条兼容路径的边界：
// 完全不提交 up/down 的请求（脚本、curl）不表达任何清零意图，计数器必须原封不动。
func TestE2EInboundEditWithoutCounterFieldsKeepsThem(t *testing.T) {
	p := newPanel(t)
	p.login()

	created := seedInboundWithTraffic(t, p, 20503, 4096, 8192)

	form := inboundForm(20503, "vmess", vmessSettings)
	form.Set("remark", "scripted")
	if _, sent := form["up"]; sent {
		t.Fatal("the fixture must not send up/down for this case")
	}
	if msg := p.decode(p.postForm("xui/inbound/update/"+strconv.Itoa(created.Id), form)); !msg.Success {
		t.Fatalf("update inbound: %s", msg.Msg)
	}
	if row := reloadInbound(t, created.Id); row.Up != 4096 || row.Down != 8192 {
		t.Errorf("up/down = %d/%d, want an update that never mentions them to leave them alone",
			row.Up, row.Down)
	}
}

// TestE2EInboundEditsDuringTrafficAccounting 把编辑与流量入账并发起来跑：
// 无论怎么交错，最终计数都等于全部增量之和。
func TestE2EInboundEditsDuringTrafficAccounting(t *testing.T) {
	p := newPanel(t)
	p.login()

	created := seedInboundWithTraffic(t, p, 20504, 0, 0)
	// 更新会把 tag 重算成 inbound-<port>-<protocol>，先落一次再取实际值。
	base := inboundForm(20504, "vmess", vmessSettings)
	if msg := p.decode(p.postForm("xui/inbound/update/"+strconv.Itoa(created.Id), base)); !msg.Success {
		t.Fatalf("canonicalise tag: %s", msg.Msg)
	}
	tag := reloadInbound(t, created.Id).Tag
	token := p.csrfToken()

	const rounds = 15
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		svc := &service.InboundService{}
		for i := 0; i < rounds; i++ {
			if err := svc.AddTraffic([]*core.Traffic{{
				IsInbound: true, Tag: tag, Up: 10, Down: 20,
			}}); err != nil {
				t.Errorf("add traffic: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			form := inboundForm(20504, "vmess", vmessSettings)
			form.Set("remark", "edit-"+strconv.Itoa(i))
			resp := p.postFormWithToken("xui/inbound/update/"+strconv.Itoa(created.Id), form, token)
			resp.Body.Close()
		}
	}()
	wg.Wait()

	row := reloadInbound(t, created.Id)
	if row.Up != rounds*10 || row.Down != rounds*20 {
		t.Errorf("up/down = %d/%d, want %d/%d — concurrent edits swallowed traffic",
			row.Up, row.Down, rounds*10, rounds*20)
	}
}
