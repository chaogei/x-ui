package core

// Traffic 描述一个统计维度在一个统计周期内的上下行累计字节数。
//
// 三种维度对应 sing-box v2ray_api 的三类计数器名：
//
//	IsInbound          inbound>>><tag>>>>traffic>>><dir>   Tag = model.Inbound.Tag
//	IsUser             user>>><name>>>>traffic>>><dir>     Tag = model.Client.Email
//	两者皆 false        outbound>>><tag>>>>traffic>>><dir>  Tag = 出站 tag
//
// user 维度只有在 sing-box 配置的 experimental.v2ray_api.stats.users 里
// 列出了该用户名时才会产生，见 service.CoreService.GetCoreConfig。
type Traffic struct {
	IsInbound bool
	IsUser    bool
	Tag       string
	Up        int64
	Down      int64
}
