package singbox

import (
	"encoding/json"

	"x-ui/util/json_util"
)

// SetStatsTargets 把要统计的入站 tag 与用户名写进
// experimental.v2ray_api.stats。
//
// 为什么必须动态写：sing-box 只为白名单里的 tag / 用户名创建计数器
// （见 experimental/v2rayapi/stats.go 的 countInbound / countUser 判断）。
// 默认模板里 stats.inbounds 是空数组，也没有 stats.users —— 也就是说
// 一个计数器都不会建，面板上的流量列永远是 0，配额永远不会触发。
// 每次生成配置时按当前启用的入站与客户端重写这两个列表，才能让统计真的转起来。
//
// 行为约定：
//   - Experimental 为空或没有 v2ray_api 时原样返回：运维显式关掉 API 的
//     配置不该被面板偷偷加回来。
//   - stats 缺失时补一个 {"enabled":true}，因为存在 v2ray_api 却没有 stats
//     只可能是模板写漏了。
//   - 其余字段（listen、stats.outbounds 等）逐字保留。
func (c *Config) SetStatsTargets(inbounds, users []string) error {
	if len(c.Experimental) == 0 {
		return nil
	}
	experimental := map[string]json.RawMessage{}
	if err := json.Unmarshal(c.Experimental, &experimental); err != nil {
		return err
	}
	rawAPI, ok := experimental["v2ray_api"]
	if !ok {
		return nil
	}
	api := map[string]json.RawMessage{}
	if err := json.Unmarshal(rawAPI, &api); err != nil {
		return err
	}

	stats := map[string]json.RawMessage{}
	if rawStats, ok := api["stats"]; ok {
		if err := json.Unmarshal(rawStats, &stats); err != nil {
			return err
		}
	}
	if _, ok := stats["enabled"]; !ok {
		stats["enabled"] = json.RawMessage("true")
	}

	encodedInbounds, err := json.Marshal(nonNil(inbounds))
	if err != nil {
		return err
	}
	stats["inbounds"] = encodedInbounds

	encodedUsers, err := json.Marshal(nonNil(users))
	if err != nil {
		return err
	}
	stats["users"] = encodedUsers

	encodedStats, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	api["stats"] = encodedStats

	encodedAPI, err := json.Marshal(api)
	if err != nil {
		return err
	}
	experimental["v2ray_api"] = encodedAPI

	encoded, err := json.Marshal(experimental)
	if err != nil {
		return err
	}
	c.Experimental = json_util.RawMessage(encoded)
	return nil
}

// nonNil 保证序列化成 [] 而不是 null。
// sing-box 对 null 的数组字段容忍度不一，给个空数组最稳妥。
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
