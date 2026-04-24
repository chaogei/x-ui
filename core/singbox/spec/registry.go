package spec

import "fmt"

// order 固定协议展示顺序，避免依赖 map 无序遍历带来的前端列表抖动。
// 顺序与 database/model.Protocol 常量声明顺序一致，便于维护时对照。
var order = []string{
	"vmess",
	"vless",
	"trojan",
	"shadowsocks",
	"hysteria2",
	"tuic",
	"anytls",
	"shadowtls",
	"naive",
	"wireguard",
	"socks",
	"http",
	"mixed",
	"direct",
}

// registry 存放全部 14 种协议的权威元数据。
//
// 维护要求：
//  1. 新增协议必须同时补齐 order 列表与 registry map，二者长度必须一致（见 init 自检）
//  2. Network 字段必须与 sing-box 实际监听传输层一致，否则端口冲突校验会错判
//  3. UserSchema 字段决定 multi-user 展开逻辑，必须与前端 form/protocol/*.html 模板
//     绑定的字段路径一致（例如模板里写 inbound.settings.users[0].uuid，则 Container="users",
//     Identifier="uuid"）
var registry = map[string]Spec{
	// —— 标准代理协议 —— //
	"vmess": {
		Key: "vmess", Network: "tcp", IsEndpoint: false, Shareable: true, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "uuid"},
	},
	"vless": {
		Key: "vless", Network: "tcp", IsEndpoint: false, Shareable: true, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "uuid"},
	},
	"trojan": {
		Key: "trojan", Network: "tcp", IsEndpoint: false, Shareable: true, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "password"},
	},
	// shadowsocks 单用户凭证直接挂在 settings 顶层，Container 为空串表达此特殊形态。
	"shadowsocks": {
		Key: "shadowsocks", Network: "tcp", IsEndpoint: false, Shareable: true, Sniffable: true,
		Users: UserSchema{Container: "", Identifier: "password"},
	},

	// —— QUIC / UDP 代理 —— //
	"hysteria2": {
		Key: "hysteria2", Network: "udp", IsEndpoint: false, Shareable: true, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "password"},
	},
	// TUIC 用户需要 uuid + password 成对出现，password 放 Credentials。
	"tuic": {
		Key: "tuic", Network: "udp", IsEndpoint: false, Shareable: true, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "uuid", Credentials: []string{"password"}},
	},

	// —— 其它代理类（无标准 URL scheme）—— //
	"anytls": {
		Key: "anytls", Network: "tcp", IsEndpoint: false, Shareable: false, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "password"},
	},
	"shadowtls": {
		Key: "shadowtls", Network: "tcp", IsEndpoint: false, Shareable: false, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "password"},
	},
	"naive": {
		Key: "naive", Network: "tcp", IsEndpoint: false, Shareable: false, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "username", Credentials: []string{"password"}},
	},

	// —— Endpoint 类隧道协议 —— //
	// wireguard 在 sing-box 1.11+ 属于 endpoint；无传统意义上的 "user"，UserSchema 全为空。
	// endpoint 类协议不走 sing-box sniff 流水线，Sniffable=false。
	"wireguard": {
		Key: "wireguard", Network: "udp", IsEndpoint: true, Shareable: false, Sniffable: false,
		Users: UserSchema{},
	},

	// —— 经典代理（socks/http/mixed）—— //
	// Shareable 虽有 socks://、http:// 事实标准 URL，前端 genLink 会生成；此处标记 true
	// 以驱动"复制分享链接"按钮显示。
	"socks": {
		Key: "socks", Network: "both", IsEndpoint: false, Shareable: true, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "username", Credentials: []string{"password"}},
	},
	"http": {
		Key: "http", Network: "tcp", IsEndpoint: false, Shareable: true, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "username", Credentials: []string{"password"}},
	},
	// mixed 本身复用 socks + http 的认证，前端未实现对应分享链接，Shareable=false。
	"mixed": {
		Key: "mixed", Network: "both", IsEndpoint: false, Shareable: false, Sniffable: true,
		Users: UserSchema{Container: "users", Identifier: "username", Credentials: []string{"password"}},
	},

	// —— 透明转发类 —— //
	// direct 无用户概念，纯端口转发；sing-box direct inbound 不接受 sniff 字段。
	"direct": {
		Key: "direct", Network: "tcp", IsEndpoint: false, Shareable: false, Sniffable: false,
		Users: UserSchema{},
	},
}

// init 确保 order 与 registry 严格一致，防止新增协议时漏改其中一处。
// 此类同步错误应在开发期立即暴露，不允许进入生产二进制。
func init() {
	if len(order) != len(registry) {
		panic(fmt.Sprintf("singbox/spec: order(%d) 与 registry(%d) 长度不一致，请检查注册表", len(order), len(registry)))
	}
	for _, k := range order {
		if _, ok := registry[k]; !ok {
			panic(fmt.Sprintf("singbox/spec: order 中的 %q 未在 registry 注册", k))
		}
	}
}

// Get 按 key 查询协议元数据。ok=false 表示协议未注册。
// 适合运行期从不可信输入（例如数据库历史数据）取值的场景。
func Get(key string) (Spec, bool) {
	s, ok := registry[key]
	return s, ok
}

// MustGet 按 key 查询协议元数据，未注册则 panic。
// 仅用于 x-ui 代码内部已知枚举的场景，不接受外部输入。
func MustGet(key string) Spec {
	s, ok := registry[key]
	if !ok {
		panic(fmt.Sprintf("singbox/spec: 未知协议 %q", key))
	}
	return s
}

// All 按固定顺序返回全部协议元数据，用于 HTTP 接口或 CLI 列举。
// 返回切片为副本，调用方可自由修改而不影响注册表。
func All() []Spec {
	out := make([]Spec, 0, len(order))
	for _, k := range order {
		out = append(out, registry[k])
	}
	return out
}
