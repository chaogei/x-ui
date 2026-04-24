//go:build windows

package singbox

import "os"

// syscallSIGTERM：Windows 下 Process.Signal(SIGTERM) 不被支持。
// 返回 os.Interrupt 让调用链走 fallback（cancel→Kill），保持跨平台可编译。
func syscallSIGTERM() os.Signal {
	return os.Interrupt
}
