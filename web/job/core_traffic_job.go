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
	if err := j.inboundService.AddTraffic(traffics); err != nil {
		logger.Warning("add traffic failed:", err)
	}
}
