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
	"regexp"
	"testing"

	"gorm.io/gorm"

	"x-ui/database"
)

// InitDB 在临时目录里初始化一个全新的 x-ui 数据库并返回句柄。
//
// 同时抑制首启凭证公告的 stderr 输出（内容通过返回的 buffer 提供），
// 避免 go test 的输出被大段横幅淹没。
//
// 收 testing.TB 而不是 *testing.T：基准测试也需要一个真实的库，
// 而它们要衡量的恰恰是每 10 秒一次的那批写。
func InitDB(t testing.TB) (*gorm.DB, *bytes.Buffer) {
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

var (
	bannerUsername = regexp.MustCompile(`username:\s*(\S+)`)
	bannerPassword = regexp.MustCompile(`password:\s*(\S+)`)
)

// ParseInitialCredentials 从首启公告横幅里解析出用户名与随机口令。
//
// 这是测试能登录新面板的唯一途径，也正是真实运维的途径
// （docker logs / journalctl）：口令只以 bcrypt 形式落库，事后无从取回。
func ParseInitialCredentials(t *testing.T, banner *bytes.Buffer) (username, password string) {
	t.Helper()

	text := banner.String()
	u := bannerUsername.FindStringSubmatch(text)
	p := bannerPassword.FindStringSubmatch(text)
	if u == nil || p == nil {
		t.Fatalf("first-boot banner did not announce credentials, got:\n%s", text)
	}
	return u[1], p[1]
}
