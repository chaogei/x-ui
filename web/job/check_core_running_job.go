package job

import "x-ui/web/service"

// CheckCoreRunningJob 周期性探测 sing-box 进程是否存活，
// 连续两次检测到挂掉后触发自动重启，避免闪断误报。
//
// 这是一张兜底网，不是主路径：内核退出的第一时间由
// CoreService.watchCoreExit 直接举旗（它等的是子进程的 waitDone 通道），
// 走到这里说明那条快路没能覆盖——例如面板刚启动、进程从来没被本进程
// fork 出来过。慢一点没关系，两次探活是为了不把瞬时抖动当成崩溃。
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
