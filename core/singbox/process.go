package singbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"x-ui/core"
	"x-ui/logger"
	"x-ui/util/common"
)

// GetBinaryName 返回当前 OS/ARCH 对应的 sing-box 二进制文件名。
// 约定：bin/sing-box-{os}-{arch}（由 install.sh / Dockerfile 在部署时放置）。
func GetBinaryName() string {
	return fmt.Sprintf("sing-box-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// GetBinaryPath 返回 sing-box 二进制的相对路径（相对 x-ui 工作目录）。
func GetBinaryPath() string {
	return "bin/" + GetBinaryName()
}

// GetConfigPath 返回 sing-box 运行所需配置文件的固定路径。
// 每次 Start 时由 x-ui 重新生成写入。
func GetConfigPath() string {
	return "bin/config.json"
}

const (
	// 进程日志缓冲最多保留多少行，超出自动丢弃最旧行。
	logLineCap = 200
	// graceful 停机时给 sing-box 发送 SIGTERM 后最多等待多久。
	gracefulStopTimeout = 5 * time.Second
)

// Process 是 core.Core 的 sing-box 实现。
//
// 并发契约：
//   - 所有对 cmd / exitErr / version / apiPort / waitDone 的读写都受 mu 保护。
//   - logs 内部自带互斥，无需外部加锁。
type Process struct {
	mu       sync.RWMutex
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	waitDone chan struct{}
	exitErr  error
	version  string
	apiPort  int
	config   *Config
	logs     *ringBuffer
	stats    *statsClient
}

// 断言 *Process 满足 core.Core 接口。
var _ core.Core = (*Process)(nil)

// NewProcess 构建一个持有指定配置的 sing-box 进程包装。
// 调用 Start 前不会创建子进程。
// 进程生命周期由调用方通过 Start/Stop 显式管理；x-ui 主进程退出时应保证
// 调用 Stop 以避免 sing-box 残留（main.go 已经接入 signal trap）。
func NewProcess(cfg *Config) *Process {
	return &Process{
		version: "Unknown",
		config:  cfg,
		logs:    newRingBuffer(logLineCap),
	}
}

// IsRunning 判断子进程是否仍在运行。
func (p *Process) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.isRunningLocked()
}

func (p *Process) isRunningLocked() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	if p.waitDone == nil {
		return false
	}
	select {
	case <-p.waitDone:
		return false
	default:
		return true
	}
}

// GetErr 返回最近一次启动失败/进程退出错误。
func (p *Process) GetErr() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.exitErr
}

// GetResult 聚合最近若干行 stdout/stderr，便于前端展示启动失败原因。
func (p *Process) GetResult() string {
	lines := p.logs.Snapshot()
	if len(lines) == 0 {
		p.mu.RLock()
		err := p.exitErr
		p.mu.RUnlock()
		if err != nil {
			return err.Error()
		}
		return ""
	}
	return strings.Join(lines, "\n")
}

// GetVersion 返回 sing-box 版本号。
func (p *Process) GetVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

// GetAPIPort 返回 V2Ray API 所在端口（从配置 experimental.v2ray_api.listen 解析）。
func (p *Process) GetAPIPort() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apiPort
}

// GetConfig 返回当前加载配置。
func (p *Process) GetConfig() core.Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}

// refreshAPIPort 从 experimental.v2ray_api.listen 中解析 gRPC API 端口。
// 调用方必须已持有 p.mu 写锁。
func (p *Process) refreshAPIPortLocked() {
	if len(p.config.Experimental) == 0 {
		return
	}
	var exp struct {
		V2RayAPI struct {
			Listen string `json:"listen"`
		} `json:"v2ray_api"`
	}
	if err := json.Unmarshal(p.config.Experimental, &exp); err != nil {
		return
	}
	idx := strings.LastIndex(exp.V2RayAPI.Listen, ":")
	if idx < 0 || idx >= len(exp.V2RayAPI.Listen)-1 {
		return
	}
	port := exp.V2RayAPI.Listen[idx+1:]
	var n int
	_, _ = fmt.Sscanf(port, "%d", &n)
	p.apiPort = n
}

// ensureAPIPortAvailableLocked 检查 config.experimental.v2ray_api.listen 的端口是否可用；
// 若已被占用则自动从随机端口区间重新选一个并回写 config。
// 调用方必须已持有 p.mu 写锁。
func (p *Process) ensureAPIPortAvailableLocked() error {
	if len(p.config.Experimental) == 0 {
		return nil
	}
	var exp map[string]json.RawMessage
	if err := json.Unmarshal(p.config.Experimental, &exp); err != nil {
		return err
	}
	rawAPI, ok := exp["v2ray_api"]
	if !ok {
		return nil
	}
	var api map[string]json.RawMessage
	if err := json.Unmarshal(rawAPI, &api); err != nil {
		return err
	}
	rawListen, ok := api["listen"]
	if !ok {
		return nil
	}
	var listen string
	if err := json.Unmarshal(rawListen, &listen); err != nil {
		return err
	}
	if listen == "" {
		return nil
	}

	// 尝试 Listen 验证端口是否空闲。
	l, err := net.Listen("tcp", listen)
	if err == nil {
		_ = l.Close()
		return nil
	}

	// 端口被占：由 OS 随机分配一个空闲端口。
	fallback, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := fallback.Addr().(*net.TCPAddr)
	_ = fallback.Close()
	newListen := fmt.Sprintf("127.0.0.1:%d", addr.Port)

	newListenRaw, err := json.Marshal(newListen)
	if err != nil {
		return err
	}
	api["listen"] = newListenRaw
	newAPI, err := json.Marshal(api)
	if err != nil {
		return err
	}
	exp["v2ray_api"] = newAPI
	newExp, err := json.Marshal(exp)
	if err != nil {
		return err
	}
	p.config.Experimental = newExp
	logger.Infof("v2ray_api listen %s is busy, switched to %s", listen, newListen)
	return nil
}

// refreshVersionLocked 通过 `sing-box version` 命令取版本号。
// 调用方必须已持有 p.mu 写锁。
func (p *Process) refreshVersionLocked() {
	cmd := exec.Command(GetBinaryPath(), "version")
	data, err := cmd.Output()
	if err != nil {
		p.version = "Unknown"
		return
	}
	line := bytes.SplitN(data, []byte("\n"), 2)[0]
	parts := bytes.Fields(line)
	if len(parts) >= 3 {
		p.version = string(parts[2])
	} else if len(parts) >= 1 {
		p.version = string(parts[len(parts)-1])
	} else {
		p.version = "Unknown"
	}
}

// Start 将配置写入磁盘并拉起 sing-box 子进程。
//
// 成功返回表示子进程已 fork，但 sing-box 可能仍在自检阶段；
// 调用方应查询 IsRunning/GetErr 判断最终状态。
func (p *Process) Start() (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isRunningLocked() {
		return errors.New("sing-box is already running")
	}

	defer func() {
		if err != nil {
			p.exitErr = err
		}
	}()

	// 保证 v2ray_api 监听口未被占用；同机多实例 / 侧载其他服务时避免盲启失败。
	if err = p.ensureAPIPortAvailableLocked(); err != nil {
		return common.NewErrorf("v2ray_api 端口预检失败: %v", err)
	}

	data, err := json.MarshalIndent(p.config, "", "  ")
	if err != nil {
		return common.NewErrorf("生成 sing-box 配置文件失败: %v", err)
	}
	configPath := GetConfigPath()
	if err = os.WriteFile(configPath, data, fs.ModePerm); err != nil {
		return common.NewErrorf("写入配置文件失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, GetBinaryPath(), "run", "-c", configPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return err
	}
	if err = cmd.Start(); err != nil {
		cancel()
		return err
	}

	p.cmd = cmd
	p.cancel = cancel
	p.waitDone = make(chan struct{})
	p.exitErr = nil

	go p.pumpLogs(stdout)
	go p.pumpLogs(stderr)

	// 独立 goroutine 执行 Wait 以回收僵尸并捕获退出错误。
	waitDone := p.waitDone
	go func() {
		waitErr := cmd.Wait()
		p.mu.Lock()
		if waitErr != nil {
			// 区分外部主动 Kill/Terminate（p.cancel 触发的 context done）和异常退出。
			if ctx.Err() == nil {
				p.exitErr = waitErr
			}
		}
		p.mu.Unlock()
		close(waitDone)
	}()

	p.refreshVersionLocked()
	p.refreshAPIPortLocked()

	// 建立 V2Ray API 长连接（懒拨号）；若端口未配置则跳过，GetTraffic 时会返回明确错误。
	if p.apiPort > 0 {
		if sc, errSc := newStatsClient(p.apiPort); errSc != nil {
			logger.Warning("init sing-box stats client failed:", errSc)
		} else {
			p.stats = sc
		}
	}
	return nil
}

// pumpLogs 持续读取管道并写入环形缓冲，EOF 或读错误时自然退出。
func (p *Process) pumpLogs(reader io.ReadCloser) {
	defer func() {
		common.Recover("")
		_ = reader.Close()
	}()
	br := bufio.NewReaderSize(reader, 8192)
	for {
		line, _, err := br.ReadLine()
		if err != nil {
			return
		}
		p.logs.Push(string(line))
	}
}

// Stop 优雅停止 sing-box。
//
// 顺序：
//  1. 若平台支持，先发送 SIGTERM 让 sing-box 执行优雅关闭（断开连接、flush 统计）。
//  2. 最多等待 gracefulStopTimeout；超时后通过 ctx 的 cancel 触发 Kill。
//  3. 等待 Wait goroutine 结束，确保端口真正释放后才返回。
func (p *Process) Stop() error {
	p.mu.Lock()
	if !p.isRunningLocked() {
		p.mu.Unlock()
		return errors.New("sing-box is not running")
	}
	cmd := p.cmd
	cancel := p.cancel
	waitDone := p.waitDone
	p.mu.Unlock()

	// Windows 下 cmd.Process.Signal(SIGTERM) 不被支持，跳到强制路径。
	if runtime.GOOS != "windows" {
		_ = cmd.Process.Signal(syscallSIGTERM())
	} else {
		cancel()
	}

	select {
	case <-waitDone:
	case <-time.After(gracefulStopTimeout):
		// 强制终止：cancel ctx 会给进程发 SIGKILL。
		cancel()
		<-waitDone
	}

	// 进程已退出，回收 gRPC 连接；再次 Start 会重新创建。
	p.mu.Lock()
	stats := p.stats
	p.stats = nil
	p.mu.Unlock()
	if stats != nil {
		_ = stats.Close()
	}
	return nil
}
