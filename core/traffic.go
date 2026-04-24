package core

// Traffic 描述一个 inbound/outbound 在一个统计周期内的上下行累计字节数。
// Tag 与 model.Inbound.Tag 一一对应（形如 "inbound-443"）。
type Traffic struct {
	IsInbound bool
	Tag       string
	Up        int64
	Down      int64
}
