package sys

import (
	"os"
	"path/filepath"
)

// HostProc 返回 procfs 的挂载根路径，并可选地拼接子路径。
//
// 历史实现通过 //go:linkname 劫持 gopsutil v3 内部的 common.HostProc；
// 项目升级到 gopsutil v4 后该符号不再存在，链接期直接失败。
// 这里改为自行实现同样的语义：优先读取 $HOST_PROC（gopsutil 的约定环境变量），
// 否则回退到 /proc。
func HostProc(combineWith ...string) string {
	root := os.Getenv("HOST_PROC")
	if root == "" {
		root = "/proc"
	}
	if len(combineWith) == 0 {
		return root
	}
	return filepath.Join(append([]string{root}, combineWith...)...)
}
