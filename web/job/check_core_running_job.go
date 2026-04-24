package job

import "x-ui/web/service"

// CheckCoreRunningJob 周期性探测 sing-box 进程是否存活，
// 连续两次检测到挂掉后触发自动重启，避免闪断误报。
type CheckCoreRunningJob struct {
	coreService service.CoreService

	checkTime int
}

func NewCheckCoreRunningJob() *CheckCoreRunningJob {
	return new(CheckCoreRunningJob)
}

func (j *CheckCoreRunningJob) Run() {
	if j.coreService.IsCoreRunning() {
		j.checkTime = 0
		return
	}
	j.checkTime++
	if j.checkTime < 2 {
		return
	}
	j.coreService.SetToNeedRestart()
}
