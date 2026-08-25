package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"x-ui/core"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/testutil"
)

// seedInbound 建一条入站并返回它，供客户端用例挂载。
func seedInbound(t *testing.T, protocol model.Protocol, port int, settings string) *model.Inbound {
	t.Helper()

	inbound := &model.Inbound{
		UserId:   1,
		Enable:   true,
		Remark:   string(protocol),
		Listen:   "",
		Port:     port,
		Protocol: protocol,
		Settings: settings,
		Tag:      "inbound-" + string(protocol),
	}
	if err := (&InboundService{}).AddInbound(inbound); err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	return inbound
}

func TestClientServiceCRUD(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30001, `{"users":[]}`)
	s := &ClientService{}

	client := &model.Client{
		InboundId: inbound.Id,
		Email:     "alice@example.com",
		Enable:    true,
		UUID:      "11111111-1111-1111-1111-111111111111",
	}
	if err := s.AddClient(client); err != nil {
		t.Fatalf("add client: %v", err)
	}
	if client.Id == 0 {
		t.Fatal("the created client has no id")
	}
	if len(client.SubToken) < 24 {
		t.Errorf("sub token is %d chars, want a long random token", len(client.SubToken))
	}

	// token 由服务端生成，客户端提交的值必须被忽略。
	second := &model.Client{
		InboundId: inbound.Id,
		Email:     "bob@example.com",
		Enable:    true,
		UUID:      "22222222-2222-2222-2222-222222222222",
		SubToken:  "attacker-chosen-token",
	}
	if err := s.AddClient(second); err != nil {
		t.Fatalf("add second client: %v", err)
	}
	if second.SubToken == "attacker-chosen-token" {
		t.Error("the caller was allowed to pick its own subscription token")
	}
	if second.SubToken == client.SubToken {
		t.Error("two clients share a subscription token")
	}

	clients, err := s.GetClients(inbound.Id)
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("listed %d clients, want 2", len(clients))
	}

	// 更新不得让调用方伪造用量。
	if err := database.GetDB().Model(model.Client{}).Where("id = ?", client.Id).
		Updates(map[string]interface{}{"up": 500, "down": 700}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	update := &model.Client{
		Id:     client.Id,
		Email:  "alice2@example.com",
		Enable: true,
		UUID:   client.UUID,
		Up:     0,
		Down:   0,
		Total:  1000,
	}
	if err := s.UpdateClient(update); err != nil {
		t.Fatalf("update client: %v", err)
	}
	reloaded, err := s.GetClient(client.Id)
	if err != nil {
		t.Fatalf("reload client: %v", err)
	}
	if reloaded.Email != "alice2@example.com" || reloaded.Total != 1000 {
		t.Errorf("update did not take: %+v", reloaded)
	}
	if reloaded.Up != 500 || reloaded.Down != 700 {
		t.Errorf("up/down = %d/%d, want the counters preserved across an update", reloaded.Up, reloaded.Down)
	}
	if reloaded.SubToken != client.SubToken {
		t.Error("updating a client silently rotated its subscription token")
	}

	if err := s.DelClient(client.Id); err != nil {
		t.Fatalf("delete client: %v", err)
	}
	if _, err := s.GetClient(client.Id); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("error = %v, want ErrClientNotFound", err)
	}
	if err := s.DelClient(client.Id); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("deleting twice returned %v, want ErrClientNotFound", err)
	}
}

func TestClientServiceRejectsDuplicateEmail(t *testing.T) {
	testutil.InitDB(t)
	a := seedInbound(t, model.VMess, 30010, `{"users":[]}`)
	b := seedInbound(t, model.VLESS, 30011, `{"users":[]}`)
	s := &ClientService{}

	if err := s.AddClient(&model.Client{InboundId: a.Id, Email: "dup@x", Enable: true, UUID: "u1"}); err != nil {
		t.Fatalf("add first client: %v", err)
	}
	// email 是 sing-box 的统计键，跨入站也必须唯一，否则两个人的流量会记到一起。
	err := s.AddClient(&model.Client{InboundId: b.Id, Email: "dup@x", Enable: true, UUID: "u2"})
	if err == nil {
		t.Fatal("the same email was accepted on a second inbound")
	}
	if !strings.Contains(err.Error(), "dup@x") {
		t.Errorf("error = %v, want it to name the duplicate email", err)
	}
}

func TestClientServiceRejectsClientsOnUserlessProtocols(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.Direct, 30020, `{"override_port":443}`)

	err := (&ClientService{}).AddClient(&model.Client{InboundId: inbound.Id, Email: "x@y", Enable: true})
	if !errors.Is(err, model.ErrProtocolHasNoUsers) {
		t.Errorf("error = %v, want ErrProtocolHasNoUsers", err)
	}
}

func TestClientServiceEnforcesTheShadowsocksSingleClientLimit(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.Shadowsocks, 30030, `{"method":"aes-256-gcm","password":"seed"}`)
	s := &ClientService{}

	if err := s.AddClient(&model.Client{InboundId: inbound.Id, Email: "ss1@x", Enable: true, Password: "p1"}); err != nil {
		t.Fatalf("add first shadowsocks client: %v", err)
	}
	err := s.AddClient(&model.Client{InboundId: inbound.Id, Email: "ss2@x", Enable: true, Password: "p2"})
	if err == nil {
		t.Fatal("a second shadowsocks client was accepted; the generated config would be rejected by sing-box")
	}
	if !strings.Contains(err.Error(), "single client") {
		t.Errorf("error = %v, want it to explain the limit", err)
	}
}

func TestRotateSubTokenInvalidatesTheOldLink(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30040, `{"users":[]}`)
	s := &ClientService{}

	client := &model.Client{InboundId: inbound.Id, Email: "rot@x", Enable: true, UUID: "u"}
	if err := s.AddClient(client); err != nil {
		t.Fatalf("add client: %v", err)
	}
	old := client.SubToken

	fresh, err := s.RotateSubToken(client.Id)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if fresh == old {
		t.Fatal("rotation produced the same token")
	}
	if _, err := s.GetClientBySubToken(old); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("the old token still resolves (%v)", err)
	}
	found, err := s.GetClientBySubToken(fresh)
	if err != nil {
		t.Fatalf("the new token does not resolve: %v", err)
	}
	if found.Id != client.Id {
		t.Errorf("the new token resolved to client %d, want %d", found.Id, client.Id)
	}
}

func TestGetClientBySubTokenRejectsEmptyToken(t *testing.T) {
	testutil.InitDB(t)
	for _, token := range []string{"", "   "} {
		if _, err := (&ClientService{}).GetClientBySubToken(token); !errors.Is(err, ErrClientNotFound) {
			t.Errorf("token %q returned %v, want ErrClientNotFound", token, err)
		}
	}
}

func TestClientAddTrafficOnlyCountsTheUserDimension(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30050, `{"users":[]}`)
	s := &ClientService{}

	client := &model.Client{InboundId: inbound.Id, Email: "traffic@x", Enable: true, UUID: "u"}
	if err := s.AddClient(client); err != nil {
		t.Fatalf("add client: %v", err)
	}

	err := s.AddTraffic([]*core.Traffic{
		{IsUser: true, Tag: "traffic@x", Up: 100, Down: 200},
		// 入站维度会算进 inbounds 表，绝不能同时加到客户端上。
		{IsInbound: true, Tag: inbound.Tag, Up: 9999, Down: 9999},
		// 已删除用户留下的最后一批字节：匹配不到就静默丢弃。
		{IsUser: true, Tag: "ghost@x", Up: 5, Down: 5},
	})
	if err != nil {
		t.Fatalf("add traffic: %v", err)
	}

	reloaded, err := s.GetClient(client.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Up != 100 || reloaded.Down != 200 {
		t.Errorf("up/down = %d/%d, want 100/200", reloaded.Up, reloaded.Down)
	}
	if reloaded.LastSeen == 0 {
		t.Error("last_seen was not stamped despite observed traffic")
	}
}

func TestDisableInvalidClients(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30060, `{"users":[]}`)
	s := &ClientService{}

	now := time.Now().UnixMilli()
	seed := []*model.Client{
		{InboundId: inbound.Id, Email: "ok@x", Enable: true, UUID: "u1", Total: 1000},
		{InboundId: inbound.Id, Email: "overquota@x", Enable: true, UUID: "u2", Total: 100},
		{InboundId: inbound.Id, Email: "expired@x", Enable: true, UUID: "u3", ExpiryTime: now - 1000},
		{InboundId: inbound.Id, Email: "unlimited@x", Enable: true, UUID: "u4"},
	}
	for _, c := range seed {
		if err := s.AddClient(c); err != nil {
			t.Fatalf("seed %s: %v", c.Email, err)
		}
	}
	if err := s.AddTraffic([]*core.Traffic{{IsUser: true, Tag: "overquota@x", Up: 60, Down: 60}}); err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	count, err := s.DisableInvalidClients()
	if err != nil {
		t.Fatalf("disable invalid clients: %v", err)
	}
	if count != 2 {
		t.Errorf("disabled %d clients, want 2 (over quota + expired)", count)
	}

	clients, err := s.GetClients(inbound.Id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	state := map[string]bool{}
	for _, c := range clients {
		state[c.Email] = c.Enable
	}
	if !state["ok@x"] || !state["unlimited@x"] {
		t.Errorf("healthy clients were disabled: %v", state)
	}
	if state["overquota@x"] || state["expired@x"] {
		t.Errorf("invalid clients are still enabled: %v", state)
	}
}

// TestGetCoreConfigExpandsClients 是多用户能力的端到端断言（内核配置这一侧）。
func TestGetCoreConfigExpandsClients(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30070, `{"users":[{"uuid":"legacy"}],"tls":{"enabled":false}}`)
	clients := &ClientService{}

	now := time.Now().UnixMilli()
	seed := []*model.Client{
		{InboundId: inbound.Id, Email: "active@x", Enable: true, UUID: "uuid-active"},
		{InboundId: inbound.Id, Email: "disabled@x", Enable: false, UUID: "uuid-disabled"},
		{InboundId: inbound.Id, Email: "expired@x", Enable: true, UUID: "uuid-expired", ExpiryTime: now - 1},
		{InboundId: inbound.Id, Email: "spent@x", Enable: true, UUID: "uuid-spent", Total: 10},
	}
	for _, c := range seed {
		if err := clients.AddClient(c); err != nil {
			t.Fatalf("seed %s: %v", c.Email, err)
		}
	}
	if err := clients.AddTraffic([]*core.Traffic{{IsUser: true, Tag: "spent@x", Up: 10, Down: 0}}); err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	cfg, err := (&CoreService{}).GetCoreConfig()
	if err != nil {
		t.Fatalf("build core config: %v", err)
	}
	if len(cfg.Inbounds) != 1 {
		t.Fatalf("config has %d inbounds, want 1", len(cfg.Inbounds))
	}

	var settings struct {
		Users []struct {
			Name string `json:"name"`
			UUID string `json:"uuid"`
		} `json:"users"`
	}
	if err := json.Unmarshal(cfg.Inbounds[0].Settings, &settings); err != nil {
		t.Fatalf("parse generated settings %s: %v", cfg.Inbounds[0].Settings, err)
	}
	if len(settings.Users) != 1 {
		t.Fatalf("generated %d users, want only the active one: %s", len(settings.Users), cfg.Inbounds[0].Settings)
	}
	if settings.Users[0].Name != "active@x" || settings.Users[0].UUID != "uuid-active" {
		t.Errorf("user = %+v, want the active client", settings.Users[0])
	}

	// 统计白名单必须同时列出入站 tag 与在用用户名，否则计数器根本不会被创建。
	var experimental struct {
		V2RayAPI struct {
			Stats struct {
				Inbounds []string `json:"inbounds"`
				Users    []string `json:"users"`
			} `json:"stats"`
		} `json:"v2ray_api"`
	}
	if err := json.Unmarshal(cfg.Experimental, &experimental); err != nil {
		t.Fatalf("parse experimental: %v", err)
	}
	if len(experimental.V2RayAPI.Stats.Inbounds) != 1 || experimental.V2RayAPI.Stats.Inbounds[0] != inbound.Tag {
		t.Errorf("stats.inbounds = %v, want [%s]", experimental.V2RayAPI.Stats.Inbounds, inbound.Tag)
	}
	if len(experimental.V2RayAPI.Stats.Users) != 1 || experimental.V2RayAPI.Stats.Users[0] != "active@x" {
		t.Errorf("stats.users = %v, want [active@x]", experimental.V2RayAPI.Stats.Users)
	}
}

// TestGetCoreConfigKeepsLegacyInboundsWorking 是迁移路径：
// clients 表为空的老入站必须继续按 settings 里的凭证下发。
func TestGetCoreConfigKeepsLegacyInboundsWorking(t *testing.T) {
	testutil.InitDB(t)
	seedInbound(t, model.VMess, 30080, `{"users":[{"uuid":"legacy-uuid","name":"legacy"}]}`)

	cfg, err := (&CoreService{}).GetCoreConfig()
	if err != nil {
		t.Fatalf("build core config: %v", err)
	}
	if !strings.Contains(string(cfg.Inbounds[0].Settings), "legacy-uuid") {
		t.Errorf("settings = %s, want the legacy credential preserved", cfg.Inbounds[0].Settings)
	}
}

// TestDelInboundRemovesItsClients 孤儿客户端会继续占着全局唯一的 email。
func TestDelInboundRemovesItsClients(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30090, `{"users":[]}`)
	clients := &ClientService{}

	if err := clients.AddClient(&model.Client{InboundId: inbound.Id, Email: "orphan@x", Enable: true, UUID: "u"}); err != nil {
		t.Fatalf("add client: %v", err)
	}
	if err := (&InboundService{}).DelInbound(inbound.Id); err != nil {
		t.Fatalf("delete inbound: %v", err)
	}

	remaining, err := clients.GetClients(inbound.Id)
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d clients survived their inbound", len(remaining))
	}

	// 同一个 email 现在应当可以重新使用。
	next := seedInbound(t, model.VMess, 30091, `{"users":[]}`)
	if err := clients.AddClient(&model.Client{InboundId: next.Id, Email: "orphan@x", Enable: true, UUID: "u"}); err != nil {
		t.Errorf("the freed email could not be reused: %v", err)
	}
}
