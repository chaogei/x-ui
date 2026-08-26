package job

import "x-ui/web/service"

// CoreRestartJob 周期性地消费"需要重启内核"的标志。
//
// 增删入站、客户端到期、探活发现内核不见了——这些事件都只是把标志立起来，
// 真正的重启集中在这里做。好处有两个：短时间内的多次改动只重启一次内核，
// 以及重启失败时有一个统一的位置做指数退避（见 CoreService.RestartCoreIfNeeded）。
type CoreRestartJob struct {
	coreService service.CoreService
}

func NewCoreRestartJob() *CoreRestartJob {
	return new(CoreRestartJob)
}

func (j *CoreRestartJob) Run() {
	j.coreService.RestartCoreIfNeeded()
}
