package job

import (
	"sort"
	"sync/atomic"

	"x-ui/core"
	"x-ui/logger"
	"x-ui/web/service"
)

// maxPendingTraffic 是失败重投缓冲的上限，按记账键计。
//
// 缓冲只在写库失败时增长，而写库失败通常意味着磁盘满或库被锁死——
// 那种状态下无限攒增量只会让面板跟着一起 OOM。超出上限时保留字节数最大的
// 那些键：配额判定对大流量用户敏感，对几 KB 的零头不敏感。
const maxPendingTraffic = 4096

// CoreTrafficJob 周期性地从代理内核（当前为 sing-box）拉取流量，
// 并累加到对应 inbound / client 记录，用于面板展示与限额熔断。
//
// 内核侧的计数器是 reset-on-read 的：QueryStats(reset=true) 一返回，
// 那批字节就只存在于本进程的内存里。因此写库失败不能一 warning 了事，
// 必须把增量留到下一轮重投，否则用户的配额会凭空多出一截。
type CoreTrafficJob struct {
	coreService    service.CoreService
	inboundService service.InboundService
	clientService  service.ClientService

	// running 挡住重叠执行。cron 不会因为上一轮没跑完就跳过这一轮，
	// 而两个 goroutine 同时 reset 计数器只会把一批流量切成两半，
	// 徒增写事务的锁竞争。
	running atomic.Bool

	// pendingInbound / pendingUser 是上一轮没能落库的增量。
	// 只在持有 running 标志期间访问，无需额外加锁。
	pendingInbound []*core.Traffic
	pendingUser    []*core.Traffic
}

func NewCoreTrafficJob() *CoreTrafficJob {
	return new(CoreTrafficJob)
}

func (j *CoreTrafficJob) Run() {
	if !j.running.CompareAndSwap(false, true) {
		logger.Debug("core traffic job is still running, skipping this tick")
		return
	}
	defer j.running.Store(false)

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
	inbound := carryOver(j.pendingInbound, traffics, func(t *core.Traffic) bool { return t.IsInbound })
	if err := j.inboundService.AddTraffic(inbound); err != nil {
		logger.Warning("add inbound traffic failed, retrying on the next tick:", err)
		j.pendingInbound = boundPending(inbound, "inbound")
	} else {
		j.pendingInbound = nil
	}

	user := carryOver(j.pendingUser, traffics, func(t *core.Traffic) bool { return t.IsUser })
	if err := j.clientService.AddTraffic(user); err != nil {
		logger.Warning("add client traffic failed, retrying on the next tick:", err)
		j.pendingUser = boundPending(user, "client")
	} else {
		j.pendingUser = nil
	}
}

// carryOver 把上一轮没写成功的增量与本轮拉到的计数器合并成一批。
//
// 只保留 keep 命中的维度，并丢掉零增量：内核在 reset 模式下依然会返回
// 上一周期没有流量的计数器，把它们带进 SQL 只是白白加长语句。
func carryOver(pending, fresh []*core.Traffic, keep func(*core.Traffic) bool) []*core.Traffic {
	merged := make(map[string]*core.Traffic, len(pending)+len(fresh))
	order := make([]string, 0, len(pending)+len(fresh))

	add := func(list []*core.Traffic) {
		for _, t := range list {
			if t == nil || t.Tag == "" || !keep(t) {
				continue
			}
			if t.Up == 0 && t.Down == 0 {
				continue
			}
			if existing, ok := merged[t.Tag]; ok {
				existing.Up += t.Up
				existing.Down += t.Down
				continue
			}
			// 复制一份：pending 会跨轮存活，而 fresh 里的指针属于本轮的响应。
			clone := *t
			merged[t.Tag] = &clone
			order = append(order, t.Tag)
		}
	}
	add(pending)
	add(fresh)

	if len(order) == 0 {
		return nil
	}
	out := make([]*core.Traffic, 0, len(order))
	for _, tag := range order {
		out = append(out, merged[tag])
	}
	return out
}

// boundPending 给重投缓冲封顶，超出部分按字节数从小到大丢弃。
func boundPending(pending []*core.Traffic, dimension string) []*core.Traffic {
	if len(pending) <= maxPendingTraffic {
		return pending
	}
	sort.SliceStable(pending, func(i, j int) bool {
		return pending[i].Up+pending[i].Down > pending[j].Up+pending[j].Down
	})
	logger.Warningf("dropping %d %s traffic counters: the retry buffer is full at %d entries",
		len(pending)-maxPendingTraffic, dimension, maxPendingTraffic)
	return pending[:maxPendingTraffic]
}
