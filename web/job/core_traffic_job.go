package job

import (
	"x-ui/logger"
	"x-ui/web/service"
)

// CoreTrafficJob 周期性地从代理内核（当前为 sing-box）拉取流量，
// 并累加到对应 inbound 记录，用于面板展示与限额熔断。
type CoreTrafficJob struct {
	coreService    service.CoreService
	inboundService service.InboundService
	clientService  service.ClientService
}

func NewCoreTrafficJob() *CoreTrafficJob {
	return new(CoreTrafficJob)
}

func (j *CoreTrafficJob) Run() {
	if !j.coreService.IsCoreRunning() {
		return
	}
	traffics, err := j.coreService.GetCoreTraffic()
	if err != nil {
		logger.Warning("get sing-box traffic failed:", err)
		return
	}
	// 同一批计数器同时携带 inbound 与 user 两个维度，各自入账各自的表。
	// 两者会重复计同一批字节，这是有意的：入站看总量，客户端看配额。
	if err := j.inboundService.AddTraffic(traffics); err != nil {
		logger.Warning("add inbound traffic failed:", err)
	}
	if err := j.clientService.AddTraffic(traffics); err != nil {
		logger.Warning("add client traffic failed:", err)
	}
}
