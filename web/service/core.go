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

	"go.uber.org/atomic"
)

// coreState 是 sing-box 子进程运行时状态的进程内单例。
//
// 所有对 proc / lastResult 的读写都必须先拿 mu；needRestart 使用原子操作。
// 这样做的目的是消除 Cron goroutine（刷新状态、拉流量）与 HTTP goroutine（重启、
// 安装新版）之间的 data race。
type coreState struct {
	mu          sync.Mutex
	proc        core.Core
	lastResult  string
	needRestart atomic.Bool
}

var state = &coreState{}

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

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.proc != nil && state.proc.IsRunning() {
		if !force && state.proc.GetConfig().Equals(cfg) {
			logger.Debug("sing-box config unchanged, skip restart")
			return nil
		}
		// Stop 内部做 graceful wait 并阻塞直到端口释放，下一步的 Start 才能安全 bind。
		if err := state.proc.Stop(); err != nil {
			logger.Warning("stop old sing-box failed:", err)
		}
	}

	state.proc = singbox.NewProcess(cfg)
	state.lastResult = ""
	return state.proc.Start()
}

// StopCore 终止 sing-box 子进程。
func (s *CoreService) StopCore() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	logger.Debug("stop sing-box")
	if state.proc == nil || !state.proc.IsRunning() {
		return errors.New("sing-box is not running")
	}
	err := state.proc.Stop()
	// 进程已经结束，下一次 GetCoreResult 要能拿到最终输出。
	state.lastResult = state.proc.GetResult()
	return err
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
