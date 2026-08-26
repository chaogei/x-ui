package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"x-ui/core"
	"x-ui/core/singbox"
	"x-ui/database/model"
	"x-ui/logger"
	"x-ui/util/json_util"
	"x-ui/web/metrics"

	"go.uber.org/atomic"
)

// 内核重启的退避区间。
//
// 配置永久非法时重启会一直失败：没有退避的话，探活任务每个周期都会
// fork 一次 sing-box、写一行错误日志，直到有人手动修好为止。
const (
	coreRestartBackoffBase = 10 * time.Second
	coreRestartBackoffMax  = 10 * time.Minute
)

// coreState 是 sing-box 子进程运行时状态的进程内单例。
//
// 所有对 proc / lastResult / startPending 的读写都必须先拿 mu；
// needRestart 使用原子操作。这样做的目的是消除 Cron goroutine（刷新状态、
// 拉流量）与 HTTP goroutine（重启、安装新版）之间的 data race。
type coreState struct {
	mu         sync.Mutex
	proc       core.Core
	lastResult string
	// startPending 表示最近一次启动的结果还没结算，见 settleLastStart。
	startPending bool

	// stopRequested 记录当前这个 proc 是被面板主动停掉的。
	// 退出监视器靠它区分"运维按了停止"与"内核自己没了"。
	stopRequested bool

	needRestart atomic.Bool
	// backoff 是失败重启的节流阀，内部自带互斥。
	backoff *Backoff
}

var state = &coreState{
	backoff: NewBackoff(coreRestartBackoffBase, coreRestartBackoffMax),
}

type CoreService struct {
	inboundService InboundService
	clientService  ClientService
	settingService SettingService
}

// IsCoreRunning 返回 sing-box 子进程是否存活。
func (s *CoreService) IsCoreRunning() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.proc != nil && state.proc.IsRunning()
}

// GetCoreErr 返回最近一次启动失败的原始错误。
func (s *CoreService) GetCoreErr() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.proc == nil {
		return nil
	}
	return state.proc.GetErr()
}

// GetCoreResult 返回 sing-box 进程的最近若干行输出（按字符串聚合）。
//
// 显示策略：
//   - 无进程实例：返回空串（首次启动之前的状态）。
//   - 进程运行中：返回空串，避免把旧的退出错误一直挂在 UI 上。
//   - 进程已退出：优先返回缓存的最终输出，缺失时从 proc 实时读取并缓存。
func (s *CoreService) GetCoreResult() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.proc == nil {
		return ""
	}
	if state.proc.IsRunning() {
		return ""
	}
	if state.lastResult == "" {
		state.lastResult = state.proc.GetResult()
	}
	return state.lastResult
}

// GetCoreVersion 返回 sing-box 版本号。
func (s *CoreService) GetCoreVersion() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.proc == nil {
		return "Unknown"
	}
	return state.proc.GetVersion()
}

// GetCoreConfig 根据设置模板 + 当前启用的入站与客户端拼装完整 sing-box 配置。
//
// 多用户展开的规则：
//   - 该入站在 clients 表里有可用客户端 → 用它们覆盖 settings 中的用户字段；
//   - 一个可用客户端都没有 → settings 原样下发。后者既是老数据的迁移路径
//     （凭证只存在于 settings 里），也让"客户端全部到期"退化成入站原有凭证
//     而不是一份没有用户、谁都连不上的配置。
//
// 同时把统计白名单写回 experimental.v2ray_api.stats：sing-box 只为白名单里的
// tag 与用户名建计数器，不写就永远统计不到任何字节。
func (s *CoreService) GetCoreConfig() (*singbox.Config, error) {
	tmpl, err := s.settingService.GetCoreTemplateConfig()
	if err != nil {
		return nil, err
	}
	cfg := &singbox.Config{}
	if err := json.Unmarshal([]byte(tmpl), cfg); err != nil {
		return nil, fmt.Errorf("sing-box template config invalid: %w", err)
	}

	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return nil, err
	}
	clientsByInbound, err := s.clientService.ActiveClientsByInbound(time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}

	statsInbounds := make([]string, 0, len(inbounds))
	statsUsers := make([]string, 0)

	for _, ib := range inbounds {
		if !ib.Enable {
			continue
		}
		clients := clientsByInbound[ib.Id]
		settings, err := model.ApplyClients(ib.Protocol, ib.Settings, clients)
		if err != nil {
			return nil, fmt.Errorf("inbound %q: %w", ib.Tag, err)
		}

		built := ib.BuildSingBoxInbound()
		built.Settings = json_util.RawMessage(settings)
		if ib.Protocol.IsEndpoint() {
			cfg.Endpoints = append(cfg.Endpoints, *built)
		} else {
			cfg.Inbounds = append(cfg.Inbounds, *built)
		}

		if ib.Tag != "" {
			statsInbounds = append(statsInbounds, ib.Tag)
		}
		statsUsers = append(statsUsers, model.StatsUserNames(ib.Protocol, clients)...)
	}

	if err := cfg.SetStatsTargets(statsInbounds, statsUsers); err != nil {
		return nil, fmt.Errorf("cannot enable sing-box traffic stats: %w", err)
	}
	return cfg, nil
}

// GetCoreTraffic 通过 V2Ray API 拉取并重置所有 inbound 的累计流量。
func (s *CoreService) GetCoreTraffic() ([]*core.Traffic, error) {
	state.mu.Lock()
	proc := state.proc
	state.mu.Unlock()
	if proc == nil || !proc.IsRunning() {
		return nil, errors.New("sing-box is not running")
	}
	return proc.GetTraffic(true)
}

// RestartCore 在必要时停止旧进程并基于最新配置启动 sing-box。
// force=true 强制重启；否则在配置等价时跳过。
func (s *CoreService) RestartCore(force bool) error {
	logger.Debug("restart sing-box, force:", force)

	cfg, err := s.GetCoreConfig()
	if err != nil {
		return err
	}

	// 杀掉进程之前先把内核侧的计数器收走，否则这一段流量随进程一起蒸发。
	// 这一步在锁外做，理由见 drainTraffic。
	outgoing, skip := s.pickDrainTarget(force, cfg)
	if skip {
		logger.Debug("sing-box config unchanged, skip restart")
		return nil
	}
	s.drainTraffic(outgoing)

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.proc != nil {
		// 重新判定一次：刚才那段无锁的收流量期间，别人可能已经替我们
		// 换过进程了。上面那次判定只是为了省掉一次没必要的 gRPC 往返，
		// 这一次才作数。
		if !force && state.proc.IsRunning() && state.proc.GetConfig().Equals(cfg) {
			logger.Debug("sing-box config unchanged, skip restart")
			return nil
		}
		// 不管进程是否还活着都要 Close：崩溃退出的实例仍握着一条到
		// v2ray_api 的 gRPC 连接，它会在后台永远重连一个没人监听的端口。
		// Close 在进程仍在运行时内部走 graceful stop，阻塞到端口释放，
		// 下一步的 Start 才能安全 bind。
		if err := state.proc.Close(); err != nil {
			logger.Warning("release old sing-box instance failed:", err)
		}
		state.proc = nil
	}

	proc := singbox.NewProcess(cfg)
	state.proc = proc
	state.lastResult = ""
	state.stopRequested = false
	// 每次真正拉起进程都记一次。这个计数持续增长通常意味着配置有问题，
	// 内核起来就崩——从面板 UI 上很难看出来，从曲线上一眼就能看出来。
	metrics.RecordCoreRestart()
	if err := proc.Start(); err != nil {
		return err
	}
	// fork 成功不等于内核活下来了，结果留给下一个周期结算。
	state.startPending = true
	go s.watchCoreExit(proc, proc.Done())
	return nil
}

// watchCoreExit 在内核进程退出的那一刻举起重启标志。
//
// 没有它的话，"内核崩了"要靠 CheckCoreRunningJob 连续两次探活才能发现：
// 30 秒一轮、连中两轮才算数，再加上重启任务自己的 10 秒周期，最坏情况下
// 用户要断线一分多钟。等 Done 通道把这段延迟压成零。
//
// 三个不该重启的情形在这里被挡掉：
//   - 面板主动停机（StopCore）——stopRequested 标记；
//   - RestartCore 换掉了这个实例——state.proc 已经不是它了；
//   - 面板还在退避窗口里——标志由 RestartCoreIfNeeded 消费，退避照旧生效，
//     所以这条快路只是让第一次重试来得更早，不会让崩溃循环转得更快。
func (s *CoreService) watchCoreExit(proc core.Core, done <-chan struct{}) {
	<-done

	state.mu.Lock()
	current := state.proc == proc
	deliberate := state.stopRequested
	if current && !deliberate {
		// 进程已经没了，把最终输出定格下来供 UI 展示。
		state.lastResult = proc.GetResult()
	}
	state.mu.Unlock()

	if !current || deliberate {
		return
	}
	logger.Warning("sing-box exited on its own, scheduling a restart")
	s.SetToNeedRestart()
}

// pickDrainTarget 在锁外挑出待收流量的进程，并顺带回答"这次重启能不能跳过"。
//
// 返回的 skip 只是一个省事的预判：真正作数的是 RestartCore 拿到锁之后
// 再做的那一次。
func (s *CoreService) pickDrainTarget(force bool, cfg core.Config) (proc core.Core, skip bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.proc == nil {
		return nil, false
	}
	if !force && state.proc.IsRunning() && state.proc.GetConfig().Equals(cfg) {
		return nil, true
	}
	return state.proc, false
}

// drainTraffic 在停掉 proc 之前把内核侧的计数器落库。
//
// sing-box 的计数器是 reset-on-read 的，且只活在进程内存里：进程一死，
// 上一次 CoreTrafficJob 之后攒下的那批字节就永远消失了。流量任务 10 秒一轮，
// 而增删一个入站、改一个客户端都会重启内核——用户每改一次配置就白嫖掉
// 最多一整轮的配额，改得越勤漏得越多。
//
// 必须在不持有 state.mu 的情况下调用：这里有一次 gRPC 往返（最长
// statsQueryTimeout）外加一次写事务，而 /healthz、状态接口和 Prometheus
// 抓取都要靠 IsCoreRunning 拿同一把锁。收流量收到一半把面板的可观测性
// 一起锁住，代价比它救回来的那点字节大得多。
//
// 重复调用是安全的：计数器已经被上一次读清零了，第二次拿到的是零增量。
//
// 失败只记日志：停机流程不该因为库写不进去而卡住，而这批字节到这一步
// 已经没有别的去处了。
func (s *CoreService) drainTraffic(proc core.Core) {
	if proc == nil || !proc.IsRunning() {
		return
	}
	traffics, err := proc.GetTraffic(true)
	if err != nil {
		logger.Warning("draining sing-box traffic before stopping it failed:", err)
		return
	}
	if len(traffics) == 0 {
		return
	}
	// 同一批计数器同时承载 inbound 与 user 两个维度，两边各自入账，
	// 与 CoreTrafficJob 的常规轮次保持一致。
	if err := s.inboundService.AddTraffic(traffics); err != nil {
		logger.Error("persisting inbound traffic before stopping sing-box failed, those bytes are lost:", err)
	}
	if err := s.clientService.AddTraffic(traffics); err != nil {
		logger.Error("persisting client traffic before stopping sing-box failed, those bytes are lost:", err)
	}
}

// StopCore 终止 sing-box 子进程。
func (s *CoreService) StopCore() error {
	logger.Debug("stop sing-box")

	// 先把这一轮的流量收进库，再让进程走；收的时候不占 state.mu，
	// 理由见 drainTraffic。
	state.mu.Lock()
	proc := state.proc
	state.mu.Unlock()
	s.drainTraffic(proc)

	state.mu.Lock()
	defer state.mu.Unlock()
	// 这是一次有意的停止，不该被 settleLastStart 当成"启动后崩溃"，
	// 也不该被 watchCoreExit 当成需要自动拉起的意外退出。
	state.startPending = false
	state.stopRequested = true
	if state.proc == nil || !state.proc.IsRunning() {
		return errors.New("sing-box is not running")
	}
	err := state.proc.Stop()
	// 进程已经结束，下一次 GetCoreResult 要能拿到最终输出。
	state.lastResult = state.proc.GetResult()
	return err
}

// RestartCoreIfNeeded 是重启标志的唯一消费者，由 cron 周期调用。
//
// 每个周期做两件事：结算上一次启动的结果，然后在退避允许时消费重启标志。
func (s *CoreService) RestartCoreIfNeeded() {
	s.settleLastStart()

	if !s.IsNeedRestartAndSetFalse() {
		return
	}
	if !state.backoff.Ready() {
		// 仍在退避窗口内：把标志放回去，等下一个周期再试。
		s.SetToNeedRestart()
		return
	}
	if err := s.RestartCore(false); err != nil {
		delay := state.backoff.Fail()
		logger.Errorf("restart sing-box failed, next attempt in %v: %v", delay, err)
		s.SetToNeedRestart()
	}
}

// settleLastStart 结算上一次启动的结果，并据此驱动退避。
//
// 为什么不能拿 RestartCore 的返回值当判据：那时进程刚 fork 出来，什么都还没做。
// 配置非法是最常见的失败形态，而 sing-box 对非法配置的反应是正常启动、
// 校验、然后在几十毫秒内自己退出——exec 成功，RestartCore 返回 nil。
// 只看返回值的话退避每次都会被复位，于是"配置坏掉 → 探活发现内核没了 →
// 再 fork 一次"可以按探活周期无限持续，这正是退避本该挡住的那件事。
//
// 改为隔一个周期回头看：进程还在就是真的起来了，不在就是起来即崩。
func (s *CoreService) settleLastStart() {
	state.mu.Lock()
	if !state.startPending {
		state.mu.Unlock()
		return
	}
	state.startPending = false
	running := state.proc != nil && state.proc.IsRunning()
	state.mu.Unlock()

	if running {
		state.backoff.Succeed()
		return
	}
	delay := state.backoff.Fail()
	logger.Errorf("sing-box exited right after start, next restart attempt in %v", delay)
	s.SetToNeedRestart()
}

// SetToNeedRestart 设置重启标志，由 cron 周期触发实际重启，
// 避免短时间内多次增删入站导致 sing-box 频繁重启。
func (s *CoreService) SetToNeedRestart() {
	state.needRestart.Store(true)
}

// IsNeedRestartAndSetFalse 原子地读取并清空重启标志。
func (s *CoreService) IsNeedRestartAndSetFalse() bool {
	return state.needRestart.CompareAndSwap(true, false)
}
