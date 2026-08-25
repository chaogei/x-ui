package database

import (
	"fmt"
	"io"
	"os"
	"path"

	"x-ui/config"
	"x-ui/database/model"
	xlogger "x-ui/logger"
	"x-ui/util/random"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

// dbDirPerm 是 /etc/x-ui 这类数据目录的权限。
// 历史实现误用 fs.ModeDir（类型位，实际权限 0000），这里改为仅 owner 可访问。
const dbDirPerm os.FileMode = 0o700

// initialPasswordLength 首次启动生成的随机管理员密码长度（字符数）。
const initialPasswordLength = 20

// SettingKeyInitialCredentials 标记「当前管理员口令仍是首次启动自动生成的随机口令」。
// 面板据此显示"请尽快修改账号密码"的提示；用户改密后由 UserService 清除。
const SettingKeyInitialCredentials = "initialCredentials"

// initUser 在空库上创建首个管理员账号。
//
// 安全约定（与历史行为的关键差异）：
//   - 不再写入众所周知的 admin/admin 明文口令；
//   - 密码由 crypto/rand 生成，长度 20，落库前经 bcrypt 哈希；
//   - 明文仅在此刻向 stderr 打印一次，之后无法从面板或数据库取回。
func initUser() error {
	if err := db.AutoMigrate(&model.User{}); err != nil {
		return err
	}
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	password, err := random.Seq(initialPasswordLength)
	if err != nil {
		return err
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := &model.User{
		Username: "admin",
		Password: string(hashed),
	}
	if err := db.Create(user).Error; err != nil {
		return err
	}
	if err := markInitialCredentials(true); err != nil {
		return err
	}
	announceInitialCredentials(user.Username, password)
	return nil
}

// credentialsWriter 是初始凭证公告的输出目标。
// 默认 stderr；测试通过 SetCredentialsOutput 改写以断言"只打印一次"。
var credentialsWriter io.Writer = os.Stderr

// SetCredentialsOutput 替换初始凭证公告的输出目标并返回旧值。
func SetCredentialsOutput(w io.Writer) io.Writer {
	old := credentialsWriter
	credentialsWriter = w
	return old
}

// announceInitialCredentials 把首次启动生成的凭证打印到 stderr（且仅此一次）。
// Docker 部署下用户从容器日志读取；systemd 部署下从 journalctl 读取。
func announceInitialCredentials(username, password string) {
	fmt.Fprintf(credentialsWriter, `
================================================================
 x-ui: 已生成初始管理员账号（此口令只显示这一次，请立即保存）
   username: %s
   password: %s
 登录面板后请立刻在「面板设置 → 用户设置」中修改。
================================================================
`, username, password)
}

// markInitialCredentials 写入/清除「仍在使用初始随机口令」标记。
// 该标记存在 settings 表，避免与用户可见的 AllSetting 字段混在一起。
func markInitialCredentials(active bool) error {
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		return err
	}
	value := "false"
	if active {
		value = "true"
	}
	setting := &model.Setting{}
	err := db.Model(model.Setting{}).Where("key = ?", SettingKeyInitialCredentials).First(setting).Error
	if IsNotFound(err) {
		return db.Create(&model.Setting{Key: SettingKeyInitialCredentials, Value: value}).Error
	} else if err != nil {
		return err
	}
	setting.Value = value
	return db.Save(setting).Error
}

// MarkInitialCredentials 供 service 层在用户改密后清除初始口令标记。
func MarkInitialCredentials(active bool) error {
	return markInitialCredentials(active)
}

func initInbound() error {
	// 旧 Xray schema 与 sing-box schema 完全不兼容，
	// 先做一次性重命名备份（若检测到），再由 AutoMigrate 建新表。
	if err := migrateFromXraySchema(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.Inbound{}); err != nil {
		return err
	}
	// 清理历史遗留列 / 索引（stream_settings、port 的 unique 索引等）。
	return dropLegacyColumns(db)
}

func initSetting() error {
	return db.AutoMigrate(&model.Setting{})
}

func InitDB(dbPath string) error {
	dir := path.Dir(dbPath)
	if err := os.MkdirAll(dir, dbDirPerm); err != nil {
		return err
	}

	var gormLogger logger.Interface

	if config.IsDebug() {
		gormLogger = logger.Default
	} else {
		gormLogger = logger.Discard
	}

	c := &gorm.Config{
		Logger: gormLogger,
	}
	var err error
	db, err = gorm.Open(sqlite.Open(dbPath), c)
	if err != nil {
		return err
	}

	// 数据库文件含 bcrypt 哈希与 session secret，收紧到 owner-only。
	if err := os.Chmod(dbPath, 0o600); err != nil && !os.IsNotExist(err) {
		xlogger.Warning("chmod db file failed:", err)
	}

	if err := initSetting(); err != nil {
		return err
	}
	if err := initUser(); err != nil {
		return err
	}
	if err := initInbound(); err != nil {
		return err
	}

	return nil
}

func GetDB() *gorm.DB {
	return db
}

// CloseDB 关闭底层连接池，测试用例在 t.Cleanup 中调用以释放临时目录。
func CloseDB() error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func IsNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}
