//go:build !windows

package singbox

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCore 在临时工作目录里放一个假的 sing-box 二进制。
//
// GetBinaryPath() 返回的是相对路径 bin/<name>，所以只能靠切目录来隔离；
// 这也是 web/service 那边验证内核校验和时用的办法。
func fakeCore(t *testing.T, script string) {
	t.Helper()

	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	if err := os.MkdirAll(filepath.Dir(GetBinaryPath()), 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	if err := os.WriteFile(GetBinaryPath(), []byte(script), 0o700); err != nil {
		t.Fatalf("write the fake core: %v", err)
	}
}

// waitForExit 等子进程退出，返回是否等到了。
func waitForExit(t *testing.T, p *Process, within time.Duration) bool {
	t.Helper()

	p.mu.RLock()
	done := p.waitDone
	p.mu.RUnlock()
	if done == nil {
		t.Fatal("the process was never started")
	}
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

// TestStartCapturesEveryLineBeforeTheProcessIsReaped 是 pumpLogs/Wait 顺序的
// 回归用例。
//
// cmd.Wait 会关掉 StdoutPipe/StderrPipe。以前 Wait 与两个读 goroutine 同时跑，
// 内核退出前最后打的那几行——恰恰是崩溃原因——会在竞态里被截断。现在 Wait
// 排在读完之后，所以 waitDone 一关闭，缓冲里就必须是完整的输出。
func TestStartCapturesEveryLineBeforeTheProcessIsReaped(t *testing.T) {
	const lines = 60
	fakeCore(t, `#!/bin/sh
if [ "$1" = "version" ]; then echo "sing-box version 9.9.9 linux/amd64"; exit 0; fi
i=0
while [ $i -lt 60 ]; do echo "stdout line $i"; i=$((i+1)); done
echo "FATAL: the configuration is invalid" 1>&2
exit 1
`)

	p := NewProcess(&Config{})
	if err := p.Start(); err != nil {
		t.Fatalf("start the fake core: %v", err)
	}
	if !waitForExit(t, p, 10*time.Second) {
		t.Fatal("the fake core never exited")
	}

	result := p.GetResult()
	for _, want := range []string{
		"stdout line 0",
		"stdout line " + strconv.Itoa(lines-1),
		"FATAL: the configuration is invalid",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("the captured output is missing %q:\n%s", want, result)
		}
	}
	if got := strings.Count(result, "stdout line "); got != lines {
		t.Errorf("captured %d stdout lines, want all %d", got, lines)
	}
	if p.GetErr() == nil {
		t.Error("a core that exited 1 on its own reported no error")
	}
	if p.IsRunning() {
		t.Error("IsRunning is still true after the process exited")
	}
	if v := p.GetVersion(); v != "9.9.9" {
		t.Errorf("version = %q, want the one the binary printed", v)
	}
}

// TestStopDrainsTheLogsOfALongRunningCore 覆盖另一半：进程还活着时 Stop
// 必须能停下来，并且停机前打的日志一条都不少。
func TestStopDrainsTheLogsOfALongRunningCore(t *testing.T) {
	fakeCore(t, `#!/bin/sh
if [ "$1" = "version" ]; then echo "sing-box version 9.9.9 linux/amd64"; exit 0; fi
echo "started"
echo "warning: something looked odd" 1>&2
while true; do sleep 0.2; done
`)

	p := NewProcess(&Config{})
	if err := p.Start(); err != nil {
		t.Fatalf("start the fake core: %v", err)
	}
	// 给它一点时间把那两行写出来。
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(p.GetResult(), "warning:") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.IsRunning() {
		t.Fatal("the fake core exited immediately")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- p.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Stop never returned; waiting for the log pumps deadlocked it")
	}

	if p.IsRunning() {
		t.Error("the process is still running after Stop")
	}
	result := p.GetResult()
	for _, want := range []string{"started", "warning: something looked odd"} {
		if !strings.Contains(result, want) {
			t.Errorf("the captured output is missing %q:\n%s", want, result)
		}
	}
	// Stop 之后可以安全地再 Close，不许 panic 或报错。
	if err := p.Close(); err != nil {
		t.Errorf("close after stop: %v", err)
	}
}

// TestPumpLogsSurvivesAPanickingSink 固定住 recover 的位置。
//
// 原先是 `defer func(){ common.Recover("") }()`：recover 隔了一层调用，
// 返回 nil，panic 会一路穿出去把整个面板带走。
func TestPumpLogsSurvivesAPanickingSink(t *testing.T) {
	p := NewProcess(&Config{})
	// 容量为 0 的环形缓冲仍然可用；这里换成一个必然 panic 的 reader，
	// 让 panic 从 pumpLogs 内部产生。
	var wg sync.WaitGroup
	wg.Add(1)
	go p.pumpLogs(&wg, panicReader{})

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pumpLogs never signalled the WaitGroup after panicking")
	}
}

// panicReader 在第一次读取时 panic。
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("pipe exploded") }
