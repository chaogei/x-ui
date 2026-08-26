package database

import (
	"bytes"
	"io"
	"net/url"
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

<<<<<<< HEAD
// TestConnectionPragmas 锁住连接级 pragma。
//
// 这些不是调优旋钮而是正确性前提：没有 WAL，每 10 秒一次的流量写事务
// 会把面板的所有读请求挡在门外；没有 busy_timeout，同一时刻的第二个写者
// 直接拿到 SQLITE_BUSY 而不是排队。
//
// pragma 是 per-connection 的，所以要在池里的每条连接上都验一遍。
func TestConnectionPragmas(t *testing.T) {
	initTempDB(t)

	want := map[string]string{
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"synchronous":  "1", // NORMAL
		"foreign_keys": "1",
	}
	// 并行拿多条连接，确认 pragma 不是只在第一条上生效。
	for i := 0; i < dbMaxOpenConns; i++ {
		tx := db.Session(&gorm.Session{})
		for pragma, expected := range want {
			var got string
			if err := tx.Raw("pragma " + pragma).Scan(&got).Error; err != nil {
				t.Fatalf("read pragma %s: %v", pragma, err)
			}
			if !strings.EqualFold(got, expected) {
				t.Errorf("pragma %s = %q, want %q", pragma, got, expected)
			}
		}
	}
}

// TestSidecarFilePermissions 覆盖 WAL 引入的两个新文件。
// -wal 里躺着还没 checkpoint 的页面，和主库一样含哈希与 secret。
func TestSidecarFilePermissions(t *testing.T) {
	dbPath, _ := initTempDB(t)

	// 触发一次写，确保 -wal 已经落地。
	if err := db.Exec("create table if not exists perm_probe(id integer)").Error; err != nil {
		t.Fatalf("write probe: %v", err)
	}

	for _, p := range sidecarPaths(dbPath) {
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if got := info.Mode().Perm(); got&0o077 != 0 {
			t.Errorf("%s mode = %#o, want no group/other bits", p, got)
		}
	}
}

// TestSQLiteDSNIsParseable 防止有人手拼查询串时漏掉转义。
func TestSQLiteDSNIsParseable(t *testing.T) {
	dsn := sqliteDSN("/etc/x-ui/x-ui.db")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn %q: %v", dsn, err)
	}
	if u.Path != "/etc/x-ui/x-ui.db" {
		t.Errorf("dsn path = %q, want the database path verbatim", u.Path)
	}
	pragmas := u.Query()["_pragma"]
	if len(pragmas) < 4 {
		t.Errorf("dsn carries %d pragmas, want the full set: %v", len(pragmas), pragmas)
	}
}

// An absent path is a startup configuration error. It must not silently select
// SQLite's special empty DSN or create a database in the process directory.
func TestInitDBRejectsMissingPath(t *testing.T) {
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("enter temporary directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	old := SetCredentialsOutput(io.Discard)
	t.Cleanup(func() { SetCredentialsOutput(old) })
	t.Cleanup(func() { _ = CloseDB() })

	if err := InitDB(""); err == nil {
		t.Fatal("InitDB accepted an empty database path")
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read working directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("an empty database path created files: %v", entries)
	}
}
