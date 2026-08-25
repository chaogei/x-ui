package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"x-ui/core/singbox/spec"
)

// TestCredentialFieldNamesCoverTheRegistry 是 spec 与 Client 列之间的闸门。
//
// Client 用三个定型列存凭证，前提是 UserSchema 只会用到这三个字段名。
// 新协议若引入第四种（例如 "token"），这条用例会先炸，而不是等到运行期
// 悄悄把该字段写成空串、用户连不上又查不出原因。
func TestCredentialFieldNamesCoverTheRegistry(t *testing.T) {
	known := map[string]bool{}
	for _, f := range credentialFieldNames {
		known[f] = true
	}
	for _, s := range spec.All() {
		if s.Users.Identifier == "" {
			continue
		}
		for _, field := range append([]string{s.Users.Identifier}, s.Users.Credentials...) {
			if !known[field] {
				t.Errorf("protocol %s declares credential field %q, which has no column on model.Client",
					s.Key, field)
			}
		}
	}
}

func TestClientIsActive(t *testing.T) {
	const now int64 = 1_700_000_000_000

	cases := map[string]struct {
		client Client
		want   bool
	}{
		"plain enabled client":     {client: Client{Enable: true}, want: true},
		"disabled":                 {client: Client{Enable: false}, want: false},
		"under quota":              {client: Client{Enable: true, Total: 100, Up: 40, Down: 40}, want: true},
		"exactly at quota":         {client: Client{Enable: true, Total: 100, Up: 60, Down: 40}, want: false},
		"over quota":               {client: Client{Enable: true, Total: 100, Up: 500}, want: false},
		"unlimited quota":          {client: Client{Enable: true, Total: 0, Up: 1 << 40}, want: true},
		"not expired":              {client: Client{Enable: true, ExpiryTime: now + 1}, want: true},
		"expired at the exact ms":  {client: Client{Enable: true, ExpiryTime: now}, want: false},
		"expired":                  {client: Client{Enable: true, ExpiryTime: now - 1}, want: false},
		"never expires":            {client: Client{Enable: true, ExpiryTime: 0}, want: true},
		"expired beats everything": {client: Client{Enable: true, Total: 0, ExpiryTime: 1}, want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.client.IsActive(now); got != tc.want {
				t.Errorf("IsActive = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateClientForProtocol(t *testing.T) {
	cases := map[string]struct {
		protocol Protocol
		client   Client
		wantErr  string
	}{
		"vmess needs a uuid": {
			protocol: VMess,
			client:   Client{Email: "a@b.c"},
			wantErr:  `"uuid"`,
		},
		"vmess with a uuid is fine": {
			protocol: VMess,
			client:   Client{Email: "a@b.c", UUID: "11111111-1111-1111-1111-111111111111"},
		},
		"tuic needs both uuid and password": {
			protocol: TUIC,
			client:   Client{Email: "a@b.c", UUID: "11111111-1111-1111-1111-111111111111"},
			wantErr:  `"password"`,
		},
		"tuic with both is fine": {
			protocol: TUIC,
			client:   Client{Email: "a@b.c", UUID: "1111", Password: "pw"},
		},
		"socks needs username and password": {
			protocol: Socks,
			client:   Client{Email: "a@b.c", Username: "u"},
			wantErr:  `"password"`,
		},
		"email is mandatory": {
			protocol: VMess,
			client:   Client{UUID: "1111"},
			wantErr:  "email",
		},
		"whitespace-only email is rejected": {
			protocol: VMess,
			client:   Client{Email: "   ", UUID: "1111"},
			wantErr:  "email",
		},
		"direct has no users": {
			protocol: Direct,
			client:   Client{Email: "a@b.c"},
			wantErr:  "no user dimension",
		},
		"wireguard has no users": {
			protocol: WireGuard,
			client:   Client{Email: "a@b.c"},
			wantErr:  "no user dimension",
		},
		"unknown protocol": {
			protocol: Protocol("carrier-pigeon"),
			client:   Client{Email: "a@b.c"},
			wantErr:  "unsupported protocol",
		},
		"extra must be an object": {
			protocol: VLESS,
			client:   Client{Email: "a@b.c", UUID: "1111", Extra: `[1,2,3]`},
			wantErr:  "JSON object",
		},
		"extra must not override the identifier": {
			protocol: VLESS,
			client:   Client{Email: "a@b.c", UUID: "1111", Extra: `{"uuid":"stolen"}`},
			wantErr:  "must not override",
		},
		"extra must not override name": {
			protocol: VLESS,
			client:   Client{Email: "a@b.c", UUID: "1111", Extra: `{"name":"someone-else"}`},
			wantErr:  "must not override",
		},
		"extra with protocol fields is fine": {
			protocol: VLESS,
			client:   Client{Email: "a@b.c", UUID: "1111", Extra: `{"flow":"xtls-rprx-vision"}`},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateClientForProtocol(tc.protocol, &tc.client)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateClientRejectsNonUserProtocolsWithSentinel(t *testing.T) {
	err := ValidateClientForProtocol(Direct, &Client{Email: "a@b.c"})
	if !errors.Is(err, ErrProtocolHasNoUsers) {
		t.Errorf("error = %v, want it to wrap ErrProtocolHasNoUsers", err)
	}
}

// TestApplyClientsExpandsUserContainers 覆盖 UserSchema 三种形态的展开。
func TestApplyClientsExpandsUserContainers(t *testing.T) {
	t.Run("vmess users carry uuid and the stats name", func(t *testing.T) {
		out := mustApply(t, VMess,
			`{"tls":{"enabled":false},"users":[{"uuid":"old"}]}`,
			[]*Client{
				{Email: "alice@x", UUID: "aaaa"},
				{Email: "bob@x", UUID: "bbbb"},
			})

		users := usersOf(t, out)
		if len(users) != 2 {
			t.Fatalf("expanded %d users, want 2", len(users))
		}
		if users[0]["uuid"] != "aaaa" || users[0]["name"] != "alice@x" {
			t.Errorf("user 0 = %v", users[0])
		}
		if users[1]["uuid"] != "bbbb" || users[1]["name"] != "bob@x" {
			t.Errorf("user 1 = %v", users[1])
		}
		// 非用户字段必须原样保留，否则改一次客户端就把 TLS 配置冲掉了。
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &probe); err != nil {
			t.Fatalf("result is not an object: %v", err)
		}
		if _, ok := probe["tls"]; !ok {
			t.Error("expanding clients dropped the tls block")
		}
	})

	t.Run("tuic carries uuid plus the password credential", func(t *testing.T) {
		out := mustApply(t, TUIC, `{"congestion_control":"bbr"}`,
			[]*Client{{Email: "carol@x", UUID: "uuid-1", Password: "pw-1"}})

		users := usersOf(t, out)
		if len(users) != 1 {
			t.Fatalf("expanded %d users, want 1", len(users))
		}
		if users[0]["uuid"] != "uuid-1" || users[0]["password"] != "pw-1" {
			t.Errorf("tuic user = %v, want both credentials", users[0])
		}
	})

	t.Run("extra fields are merged into the user entry", func(t *testing.T) {
		out := mustApply(t, VLESS, `{}`,
			[]*Client{{Email: "d@x", UUID: "uuid-2", Extra: `{"flow":"xtls-rprx-vision"}`}})

		users := usersOf(t, out)
		if users[0]["flow"] != "xtls-rprx-vision" {
			t.Errorf("user = %v, want the extra flow field", users[0])
		}
	})

	t.Run("shadowsocks writes the credential at the top level", func(t *testing.T) {
		out := mustApply(t, Shadowsocks, `{"method":"aes-256-gcm","password":"old"}`,
			[]*Client{{Email: "e@x", Password: "new-secret"}})

		var probe map[string]interface{}
		if err := json.Unmarshal([]byte(out), &probe); err != nil {
			t.Fatalf("result is not an object: %v", err)
		}
		if probe["password"] != "new-secret" {
			t.Errorf("password = %v, want the client credential at the top level", probe["password"])
		}
		if probe["method"] != "aes-256-gcm" {
			t.Errorf("method = %v, want it preserved", probe["method"])
		}
		if _, hasUsers := probe["users"]; hasUsers {
			t.Error("shadowsocks must not grow a users array; its UserSchema has an empty Container")
		}
	})

	t.Run("shadowsocks refuses a second client", func(t *testing.T) {
		_, err := ApplyClients(Shadowsocks, `{"method":"aes-256-gcm"}`, []*Client{
			{Email: "f@x", Password: "one"},
			{Email: "g@x", Password: "two"},
		})
		if err == nil {
			t.Fatal("two clients on a top-level-credential protocol were silently accepted")
		}
		if !strings.Contains(err.Error(), "single credential") {
			t.Errorf("error = %v, want it to explain the single-credential limit", err)
		}
	})
}

// TestApplyClientsFallsBackToSettings 是老数据的迁移路径：
// clients 表为空时入站必须继续按 settings 里的凭证工作。
func TestApplyClientsFallsBackToSettings(t *testing.T) {
	const settings = `{"users":[{"uuid":"legacy-uuid"}]}`

	got, err := ApplyClients(VMess, settings, nil)
	if err != nil {
		t.Fatalf("ApplyClients: %v", err)
	}
	if got != settings {
		t.Errorf("settings = %q, want the original %q", got, settings)
	}

	got, err = ApplyClients(VMess, settings, []*Client{})
	if err != nil {
		t.Fatalf("ApplyClients: %v", err)
	}
	if got != settings {
		t.Errorf("settings = %q, want the original", got)
	}
}

func TestApplyClientsLeavesUserlessProtocolsAlone(t *testing.T) {
	const settings = `{"private_key":"k","peers":[]}`
	got, err := ApplyClients(WireGuard, settings, []*Client{{Email: "x@y"}})
	if err != nil {
		t.Fatalf("ApplyClients: %v", err)
	}
	if got != settings {
		t.Errorf("settings = %q, want wireguard settings untouched", got)
	}
}

func TestApplyClientsRejectsMalformedSettings(t *testing.T) {
	if _, err := ApplyClients(VMess, `[1,2,3]`, []*Client{{Email: "a@b", UUID: "u"}}); err == nil {
		t.Error("a non-object settings blob was accepted")
	}
	if _, err := ApplyClients(Protocol("nope"), `{}`, []*Client{{Email: "a@b"}}); err == nil {
		t.Error("an unknown protocol was accepted")
	}
}

func TestApplyClientsHandlesEmptySettings(t *testing.T) {
	out := mustApply(t, VMess, "", []*Client{{Email: "a@b", UUID: "u"}})
	users := usersOf(t, out)
	if len(users) != 1 || users[0]["uuid"] != "u" {
		t.Errorf("users = %v", users)
	}
}

func TestStatsUserNames(t *testing.T) {
	clients := []*Client{{Email: "a@x"}, {Email: "b@x"}}

	if got := StatsUserNames(VMess, clients); len(got) != 2 || got[0] != "a@x" || got[1] != "b@x" {
		t.Errorf("vmess stats names = %v, want both emails", got)
	}
	// shadowsocks 的凭证没有 name 字段，sing-box 无从按用户计数——
	// 把它列进 stats.users 只会制造一个永远为 0 的计数器。
	if got := StatsUserNames(Shadowsocks, clients); len(got) != 0 {
		t.Errorf("shadowsocks stats names = %v, want none", got)
	}
	if got := StatsUserNames(Direct, clients); len(got) != 0 {
		t.Errorf("direct stats names = %v, want none", got)
	}
	if got := StatsUserNames(Protocol("nope"), clients); len(got) != 0 {
		t.Errorf("unknown protocol stats names = %v, want none", got)
	}
}

// TestApplyClientsCoversEveryUserProtocol 走一遍注册表，确保每种带用户的协议
// 都能被展开成 sing-box 能接受的形状——新增协议时这条会自动覆盖到。
func TestApplyClientsCoversEveryUserProtocol(t *testing.T) {
	for _, s := range spec.All() {
		if s.Users.Identifier == "" {
			continue
		}
		t.Run(s.Key, func(t *testing.T) {
			client := &Client{Email: "user@" + s.Key, UUID: "uuid-x", Password: "pw-x", Username: "name-x"}
			if err := ValidateClientForProtocol(Protocol(s.Key), client); err != nil {
				t.Fatalf("a fully-populated client was rejected: %v", err)
			}
			out, err := ApplyClients(Protocol(s.Key), `{}`, []*Client{client})
			if err != nil {
				t.Fatalf("ApplyClients: %v", err)
			}
			if s.Users.Container == "" {
				var probe map[string]interface{}
				if err := json.Unmarshal([]byte(out), &probe); err != nil {
					t.Fatalf("result is not an object: %v", err)
				}
				if probe[s.Users.Identifier] == "" {
					t.Errorf("top-level %q was not populated", s.Users.Identifier)
				}
				return
			}
			users := usersOf(t, out)
			if len(users) != 1 {
				t.Fatalf("expanded %d users, want 1", len(users))
			}
			if users[0]["name"] != client.Email {
				t.Errorf("user name = %v, want the client email so sing-box can count it", users[0]["name"])
			}
			for _, field := range append([]string{s.Users.Identifier}, s.Users.Credentials...) {
				if users[0][field] == "" {
					t.Errorf("user field %q is empty", field)
				}
			}
		})
	}
}

/* ---------- helpers ---------- */

func mustApply(t *testing.T, protocol Protocol, settings string, clients []*Client) string {
	t.Helper()
	out, err := ApplyClients(protocol, settings, clients)
	if err != nil {
		t.Fatalf("ApplyClients(%s): %v", protocol, err)
	}
	return out
}

// usersOf 解析出 settings.users，值统一按字符串读取（本包写入的都是字符串字段）。
func usersOf(t *testing.T, settings string) []map[string]string {
	t.Helper()

	var probe struct {
		Users []map[string]interface{} `json:"users"`
	}
	if err := json.Unmarshal([]byte(settings), &probe); err != nil {
		t.Fatalf("parse settings %q: %v", settings, err)
	}
	out := make([]map[string]string, 0, len(probe.Users))
	for _, u := range probe.Users {
		row := map[string]string{}
		for k, v := range u {
			if s, ok := v.(string); ok {
				row[k] = s
			}
		}
		out = append(out, row)
	}
	return out
}
