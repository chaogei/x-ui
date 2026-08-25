package database

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"x-ui/database/model"
)

// initTempDB 在临时目录里跑一次完整的首启流程，返回公告横幅内容。
func initTempDB(t *testing.T) (string, *bytes.Buffer) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sub", "x-ui.db")
	banner := &bytes.Buffer{}
	old := SetCredentialsOutput(banner)
	t.Cleanup(func() { SetCredentialsOutput(old) })

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init database: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	return dbPath, banner
}

var passwordLine = regexp.MustCompile(`password:\s*(\S+)`)

// TestFirstBootDoesNotSeedAdminAdmin 是 H-4 的核心回归：
// 历史实现把 admin/admin 明文写进库，任何扫到面板的人都能直接登录。
func TestFirstBootDoesNotSeedAdminAdmin(t *testing.T) {
	_, banner := initTempDB(t)

	user := &model.User{}
	if err := db.Model(model.User{}).First(user).Error; err != nil {
		t.Fatalf("first boot must create an admin user: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("username = %q, want admin", user.Username)
	}

	for _, weak := range []string{"admin", "password", "123456", "x-ui"} {
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(weak)); err == nil {
			t.Fatalf("the seeded password must not be the well-known value %q", weak)
		}
	}

	// 唯一能拿到明文的地方是那份只打印一次的公告。
	m := passwordLine.FindStringSubmatch(banner.String())
	if m == nil {
		t.Fatalf("first boot must announce the generated password, banner was:\n%s", banner.String())
	}
	announced := m[1]
	if len(announced) < 16 {
		t.Errorf("announced password %q is %d chars, want at least 16", announced, len(announced))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(announced)); err != nil {
		t.Errorf("the announced password does not match the stored hash: %v", err)
	}
}

// TestFirstBootStoresBcryptNotPlaintext 确认落库的是哈希。
func TestFirstBootStoresBcryptNotPlaintext(t *testing.T) {
	_, banner := initTempDB(t)

	user := &model.User{}
	if err := db.Model(model.User{}).First(user).Error; err != nil {
		t.Fatalf("read seeded user: %v", err)
	}
	if !strings.HasPrefix(user.Password, "$2") {
		t.Errorf("stored password %q is not a bcrypt hash", user.Password)
	}

	announced := passwordLine.FindStringSubmatch(banner.String())[1]
	if user.Password == announced {
		t.Error("the plaintext password was stored verbatim")
	}
}

// TestGeneratedPasswordsDifferPerInstall 防止有人把随机源换回固定种子。
func TestGeneratedPasswordsDifferPerInstall(t *testing.T) {
	seen := make(map[string]bool, 4)
	for i := 0; i < 4; i++ {
		_, banner := initTempDB(t)
		m := passwordLine.FindStringSubmatch(banner.String())
		if m == nil {
			t.Fatalf("run %d announced no password", i)
		}
		if seen[m[1]] {
			t.Fatalf("password %q was generated twice — the RNG is not seeded from crypto/rand", m[1])
		}
		seen[m[1]] = true
	}
}

// TestSecondBootIsQuiet 确认公告只出现一次：重启面板不会再刷一遍口令，
// 也不会把已有账号覆盖掉。
func TestSecondBootIsQuiet(t *testing.T) {
	dbPath, banner := initTempDB(t)
	if banner.Len() == 0 {
		t.Fatal("first boot must announce credentials")
	}
	firstHash := currentPasswordHash(t)

	banner.Reset()
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if banner.Len() != 0 {
		t.Errorf("second boot re-announced credentials:\n%s", banner.String())
	}
	if got := currentPasswordHash(t); got != firstHash {
		t.Error("second boot rewrote the admin password")
	}
}

func currentPasswordHash(t *testing.T) string {
	t.Helper()
	user := &model.User{}
	if err := db.Model(model.User{}).First(user).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	return user.Password
}

// TestInitialCredentialsFlag 驱动面板顶部那条"请尽快改密"的提示。
func TestInitialCredentialsFlag(t *testing.T) {
	initTempDB(t)

	if !initialCredentialsActive(t) {
		t.Fatal("the initial-credentials flag must be set on first boot")
	}
	if err := MarkInitialCredentials(false); err != nil {
		t.Fatalf("clear flag: %v", err)
	}
	if initialCredentialsActive(t) {
		t.Error("the flag must be cleared once the operator changes the password")
	}
}

func initialCredentialsActive(t *testing.T) bool {
	t.Helper()
	setting := &model.Setting{}
	err := db.Model(model.Setting{}).Where("key = ?", SettingKeyInitialCredentials).First(setting).Error
	if err == gorm.ErrRecordNotFound {
		return false
	}
	if err != nil {
		t.Fatalf("read initial-credentials flag: %v", err)
	}
	return setting.Value == "true"
}

// TestDataDirectoryPermissions 是 C-2 的第三处：
// 历史实现传的是 fs.ModeDir，那是类型位不是权限位，实际建出 0000 的目录。
func TestDataDirectoryPermissions(t *testing.T) {
	dbPath, _ := initTempDB(t)

	dirInfo, err := os.Stat(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("stat data directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != dbDirPerm {
		t.Errorf("data directory mode = %#o, want %#o", got, dbDirPerm)
	}

	fileInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat database file: %v", err)
	}
	// 库里装着 bcrypt 哈希和 session secret，group/other 一律不可读。
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("database file mode = %#o, want 0600", got)
	}
}

func TestInitDBIsIdempotent(t *testing.T) {
	dbPath, _ := initTempDB(t)
	for i := 0; i < 3; i++ {
		if err := InitDB(dbPath); err != nil {
			t.Fatalf("re-init %d: %v", i, err)
		}
	}
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Errorf("user count = %d, want exactly 1 after repeated inits", count)
	}
}

func TestSchemaIsCreated(t *testing.T) {
	initTempDB(t)
	for _, table := range []string{"users", "settings", "inbounds"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %q was not created", table)
		}
	}
}
