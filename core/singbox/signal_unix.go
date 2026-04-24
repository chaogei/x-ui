//go:build !windows

package singbox

import (
	"os"
	"syscall"
)

// syscallSIGTERM 返回平台相关的 SIGTERM 信号。
// Linux/macOS 上原生支持；Windows 走另一个实现。
func syscallSIGTERM() os.Signal {
	return syscall.SIGTERM
}
