// Package testutil 提供测试专用的脚手架。
//
// 只被 _test.go 文件 import，因此不会被链接进发布二进制。
//
// 铁律：测试永远不得读写 /etc/x-ui。所有用例都通过 XUI_DB_PATH 把数据库
// 指向 t.TempDir() 下的临时文件。
package testutil

import (
	"bytes"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"x-ui/database"
)

// InitDB 在临时目录里初始化一个全新的 x-ui 数据库并返回句柄。
//
// 同时抑制首启凭证公告的 stderr 输出（内容通过返回的 buffer 提供），
// 避免 go test 的输出被大段横幅淹没。
func InitDB(t *testing.T) (*gorm.DB, *bytes.Buffer) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	t.Setenv("XUI_DB_PATH", dbPath)

	banner := &bytes.Buffer{}
	old := database.SetCredentialsOutput(banner)
	t.Cleanup(func() { database.SetCredentialsOutput(old) })

	if err := database.InitDB(dbPath); err != nil {
		t.Fatalf("init test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.CloseDB(); err != nil {
			t.Logf("close test database: %v", err)
		}
	})
	return database.GetDB(), banner
}

// DBPath 返回当前测试使用的数据库路径（InitDB 之后调用）。
func DBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "x-ui.db")
}
