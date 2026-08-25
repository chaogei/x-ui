package job

import (
	"x-ui/logger"
	"x-ui/web/service"
)

// CheckInboundJob 周期性地熄灭超配额或已到期的入站与客户端。
type CheckInboundJob struct {
	coreService    service.CoreService
	inboundService service.InboundService
	clientService  service.ClientService
}

func NewCheckInboundJob() *CheckInboundJob {
	return new(CheckInboundJob)
}

func (j *CheckInboundJob) Run() {
	needRestart := false

	count, err := j.inboundService.DisableInvalidInbounds()
	if err != nil {
		logger.Warning("disable invalid inbounds err:", err)
	} else if count > 0 {
		logger.Debugf("disabled %v inbounds", count)
		needRestart = true
	}

	// 客户端也要一起熄灭：只在面板上标灰而不重新生成配置，
	// 过期用户会继续正常连接直到下一次因别的原因重启内核。
	clients, err := j.clientService.DisableInvalidClients()
	if err != nil {
		logger.Warning("disable invalid clients err:", err)
	} else if clients > 0 {
		logger.Debugf("disabled %v clients", clients)
		needRestart = true
	}

	if needRestart {
		j.coreService.SetToNeedRestart()
	}
}
