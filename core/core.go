// Package core 定义了内核无关的抽象层。
// x-ui 通过该接口对接 sing-box/Xray 等不同的代理内核，
// 保证上层 service、job、controller 不直接依赖具体内核实现。
package core

// Core 表示一个可被面板托管的代理内核实例（例如 sing-box 子进程）。
//
// 所有方法必须是并发安全的：Start/Stop 互斥由实现保证，
// IsRunning/GetVersion/GetTraffic 可在任意时刻被调用。
type Core interface {
	// Start 以当前 Config 启动内核，若已在运行则返回错误。
	Start() error

	// Stop 停止内核，若未运行则返回错误。
	Stop() error

	// IsRunning 返回内核进程是否仍在运行。
	IsRunning() bool

	// GetErr 返回最近一次启动/运行过程中产生的错误。
	GetErr() error

	// GetResult 返回内核进程最后 N 行 stdout/stderr，用于前端展示启动失败原因。
	GetResult() string

	// GetVersion 返回内核版本号（形如 "sing-box 1.10.7"）。
	GetVersion() string

	// GetConfig 返回当前加载的配置，用于与新配置比对决定是否需要重启。
	GetConfig() Config

	// GetTraffic 拉取并重置流量计数器；reset=true 时服务端清零。
	GetTraffic(reset bool) ([]*Traffic, error)
}

// Config 是内核配置的抽象。
// 不同内核有不同 schema，调用方不应直接访问字段，
// 而是通过 Equals 做幂等判断。
type Config interface {
	// Equals 判断两份配置在语义上是否完全等价，
	// 相等则无需重启进程。
	Equals(other Config) bool

	// MarshalJSON 序列化为内核可加载的 JSON 字节流。
	MarshalJSON() ([]byte, error)
}
