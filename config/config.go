package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed version
var version string

//go:embed name
var name string

type LogLevel string

const (
	Debug LogLevel = "debug"
	Info  LogLevel = "info"
	Warn  LogLevel = "warn"
	Error LogLevel = "error"
)

func GetVersion() string {
	return strings.TrimSpace(version)
}

func GetName() string {
	return strings.TrimSpace(name)
}

func GetLogLevel() LogLevel {
	if IsDebug() {
		return Debug
	}
	logLevel := os.Getenv("XUI_LOG_LEVEL")
	if logLevel == "" {
		return Info
	}
	return LogLevel(logLevel)
}

func IsDebug() bool {
	return os.Getenv("XUI_DEBUG") == "true"
}

// GetDBFolderPath 返回数据库所在目录。
// 可通过 XUI_DB_FOLDER 覆盖，便于容器化部署与测试（默认 /etc/x-ui）。
func GetDBFolderPath() string {
	if folder := os.Getenv("XUI_DB_FOLDER"); folder != "" {
		return folder
	}
	return fmt.Sprintf("/etc/%s", GetName())
}

// GetDBPath 返回 SQLite 数据库文件的完整路径。
//
// 覆盖优先级：XUI_DB_PATH > XUI_DB_FOLDER/<name>.db > /etc/<name>/<name>.db。
// 测试与容器场景必须使用环境变量而不是硬编码的 /etc 路径。
func GetDBPath() string {
	if p := os.Getenv("XUI_DB_PATH"); p != "" {
		return p
	}
	return filepath.Join(GetDBFolderPath(), GetName()+".db")
}
