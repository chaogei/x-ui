// Package spec 定义 sing-box 协议元数据的单一来源（Single Source of Truth）。
//
// 设计目标：
//  1. 把散落在前端（core.js / models.js）与后端（model.Protocol 方法）的协议
//     元数据集中到一个权威位置，让"新增协议 / 调整分类"只需改此包。
//  2. 保持零外部依赖，仅输出数据结构；model 层与 web 层按需消费。
//  3. 字段命名与 sing-box 配置保持一致，便于直接序列化为 JSON 供前端消费。
//
// 依赖约束：此包位于依赖图底层，任何上层包（model / service / controller）
// 均可 import，但本包自身不得 import x-ui 内的业务层，以避免循环依赖。
package spec

// Spec 描述一种 sing-box 协议在 x-ui 面板内的关键元数据。
//
// 字段说明：
//   - Key        ：协议标识，等于 sing-box inbound/endpoint 配置中的 "type" 字段
//   - Network    ：传输层网络类型，取值 "tcp" / "udp" / "both"，用于端口冲突校验
//   - IsEndpoint ：是否挂在 sing-box 的 endpoints 列表（例如 WireGuard）
//   - Shareable  ：是否能生成标准分享 URL（前端用于决定是否显示复制/二维码按钮）
//   - Sniffable  ：是否支持 sniff 配置（TCP 流量协议识别；endpoint 与透明转发类不支持）
//   - Users      ：协议对应的用户字段形态，multi-user 场景下需要定位用户数组
//
// 本结构体会被序列化为 JSON 暴露给前端，字段命名使用 snake_case 与 sing-box 惯例对齐。
type Spec struct {
	Key        string     `json:"key"`
	Network    string     `json:"network"`
	IsEndpoint bool       `json:"is_endpoint"`
	Shareable  bool       `json:"shareable"`
	Sniffable  bool       `json:"sniffable"`
	Users      UserSchema `json:"users"`
}

// UserSchema 描述协议私有 settings 中用户凭证的位置形态。
//
// 设计此结构是为 multi-user 阶段服务：后端需要基于 Spec 把一组用户展开到
// 具体协议的 settings JSON 中（或从中读回）。三元组 (Container, Identifier,
// Credentials) 足以表达当前 14 个协议的全部形态：
//
//	Container   非空 → settings.<Container>[] 是用户数组（vmess/vless/trojan/...）
//	Container   为空 → 用户凭证直接挂在 settings 顶层（例如 shadowsocks 的 password）
//	Identifier  为空 → 协议无用户概念（例如 direct / wireguard）
//	Credentials 额外凭证字段（TUIC 的 password、NAIVE 的 password 等）
type UserSchema struct {
	Container   string   `json:"container,omitempty"`
	Identifier  string   `json:"identifier,omitempty"`
	Credentials []string `json:"credentials,omitempty"`
}

// HasUsers 判断协议是否具有可管理的用户维度。
// direct / wireguard 这类协议返回 false，上层在展开 multi-user 时直接跳过。
func (s Spec) HasUsers() bool {
	return s.Users.Identifier != ""
}
