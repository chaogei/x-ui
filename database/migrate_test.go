package database

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"x-ui/database/model"
)

// seedLegacyDB 造一个 Xray 时代的 x-ui.db：inbounds 表用旧列名与旧 settings 结构。
func seedLegacyDB(t *testing.T, rows []map[string]interface{}) string {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	raw, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}

	// 旧 schema：有 stream_settings，protocol 存的是 Xray 的协议名。
	err = raw.Exec(`CREATE TABLE inbounds (
		id integer PRIMARY KEY AUTOINCREMENT,
		user_id integer,
		up integer,
		down integer,
		total integer,
		remark text,
		enable numeric,
		expiry_time integer,
		listen text,
		port integer UNIQUE,
		protocol text,
		settings text,
		stream_settings text,
		tag text,
		sniffing text
	)`).Error
	if err != nil {
		t.Fatalf("create legacy inbounds table: %v", err)
	}

	for _, row := range rows {
		if err := raw.Table("inbounds").Create(row).Error; err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}
	if sqlDB, err := raw.DB(); err == nil {
		_ = sqlDB.Close()
	}
	return dbPath
}

func legacyXrayRow() map[string]interface{} {
	return map[string]interface{}{
		"user_id":         1,
		"port":            10000,
		"protocol":        "vmess",
		"remark":          "old",
		"enable":          true,
		"listen":          "",
		"settings":        `{"clients":[{"id":"11111111-1111-1111-1111-111111111111","alterId":0}],"disableInsecureEncryption":false}`,
		"stream_settings": `{"network":"tcp","security":"none"}`,
		"tag":             "inbound-10000",
		"sniffing":        `{"enabled":true,"destOverride":["http","tls"]}`,
	}
}

func backupTables(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var names []string
	if err := db.Raw(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'inbounds_xray_backup_%'`).Scan(&names).Error; err != nil {
		t.Fatalf("list backup tables: %v", err)
	}
	return names
}

// TestMigrateBacksUpXraySchema 覆盖从 Xray 版 x-ui 升级上来的用户：
// 旧数据不能被静默丢弃，也不能让 AutoMigrate 把两套 schema 混在一张表里。
func TestMigrateBacksUpXraySchema(t *testing.T) {
	dbPath := seedLegacyDB(t, []map[string]interface{}{legacyXrayRow()})

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init database over a legacy fixture: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	backups := backupTables(t, db)
	if len(backups) != 1 {
		t.Fatalf("backup tables = %v, want exactly one", backups)
	}

	var backedUp int64
	if err := db.Table(backups[0]).Count(&backedUp).Error; err != nil {
		t.Fatalf("count rows in backup: %v", err)
	}
	if backedUp != 1 {
		t.Errorf("backup holds %d rows, want the 1 legacy inbound", backedUp)
	}

	// 新 inbounds 表按 sing-box schema 重建，且是空的。
	var current int64
	if err := db.Model(&model.Inbound{}).Count(&current).Error; err != nil {
		t.Fatalf("count current inbounds: %v", err)
	}
	if current != 0 {
		t.Errorf("current inbounds = %d, want a clean table for sing-box", current)
	}
	if db.Migrator().HasColumn(&model.Inbound{}, "stream_settings") {
		t.Error("stream_settings is an Xray-only column and must be dropped")
	}
}

// TestMigrateDetectsLegacyProtocolNames 覆盖第二种识别信号：
// Dokodemo-door / mtproto 只可能出自 Xray 时代。
func TestMigrateDetectsLegacyProtocolNames(t *testing.T) {
	for _, protocol := range []string{"Dokodemo-door", "mtproto"} {
		t.Run(protocol, func(t *testing.T) {
			row := legacyXrayRow()
			row["protocol"] = protocol
			row["settings"] = `{"address":"1.1.1.1","port":53,"network":"udp"}`
			dbPath := seedLegacyDB(t, []map[string]interface{}{row})

			if err := InitDB(dbPath); err != nil {
				t.Fatalf("init database: %v", err)
			}
			t.Cleanup(func() { _ = CloseDB() })

			if got := backupTables(t, db); len(got) != 1 {
				t.Errorf("backup tables = %v, want one for a %s inbound", got, protocol)
			}
		})
	}
}

// TestMigrateLeavesCleanSingBoxDBAlone 是反向保证：
// 已经是 sing-box schema 的库不能被误判，否则用户的入站会凭空消失。
func TestMigrateLeavesCleanSingBoxDBAlone(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init database: %v", err)
	}

	inbound := &model.Inbound{
		UserId:   1,
		Port:     10000,
		Protocol: model.VMess,
		Remark:   "keep me",
		Enable:   true,
		Settings: `{"users":[{"uuid":"11111111-1111-1111-1111-111111111111"}]}`,
		Tag:      "inbound-10000",
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	if err := CloseDB(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	// 模拟面板重启。
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("re-init database: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	if got := backupTables(t, db); len(got) != 0 {
		t.Errorf("backup tables = %v, want none for an already-migrated database", got)
	}

	var kept model.Inbound
	if err := db.Model(&model.Inbound{}).First(&kept).Error; err != nil {
		t.Fatalf("the existing inbound was lost: %v", err)
	}
	if kept.Remark != "keep me" {
		t.Errorf("remark = %q, want the untouched row", kept.Remark)
	}
}

// TestMigrateIgnoresEmptyLegacyTable 空表没有可备份的数据，直接交给 AutoMigrate。
func TestMigrateIgnoresEmptyLegacyTable(t *testing.T) {
	dbPath := seedLegacyDB(t, nil)

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init database: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	if got := backupTables(t, db); len(got) != 0 {
		t.Errorf("backup tables = %v, want none for an empty legacy table", got)
	}
}

// TestMigrateRunsOnlyOnce 迁移过一次之后，重启不应再产生新的备份表。
func TestMigrateRunsOnlyOnce(t *testing.T) {
	dbPath := seedLegacyDB(t, []map[string]interface{}{legacyXrayRow()})

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("first init: %v", err)
	}
	first := backupTables(t, db)
	if err := CloseDB(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("second init: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	second := backupTables(t, db)
	if len(second) != len(first) {
		t.Errorf("backup tables grew from %v to %v across restarts", first, second)
	}
}

// TestDropLegacyPortUniqueIndex 覆盖 Port 去掉 unique 之后的索引清理：
// 旧库残留的 UNIQUE 索引会让"同端口 tcp + udp 两条入站"这种合法配置写不进去。
func TestDropLegacyPortUniqueIndex(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init database: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_inbounds_port ON inbounds(port)`).Error; err != nil {
		t.Fatalf("recreate legacy index: %v", err)
	}
	if err := dropLegacyColumns(db); err != nil {
		t.Fatalf("drop legacy columns: %v", err)
	}
	if db.Migrator().HasIndex(&model.Inbound{}, "idx_inbounds_port") {
		t.Error("the legacy unique index on inbounds.port must be dropped")
	}
}

func TestMigrateFromXraySchemaSkipsMissingTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	raw, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := raw.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if err := migrateFromXraySchema(raw); err != nil {
		t.Errorf("a database without an inbounds table must be a no-op, got %v", err)
	}
	if err := dropLegacyColumns(raw); err != nil {
		t.Errorf("dropLegacyColumns on a fresh database must be a no-op, got %v", err)
	}
}

// TestBackupTableNameIsTimestamped 备份表名必须带时间戳，
// 这样多次迁移不会互相覆盖，运维也能看出是什么时候备的。
func TestBackupTableNameIsTimestamped(t *testing.T) {
	dbPath := seedLegacyDB(t, []map[string]interface{}{legacyXrayRow()})
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init database: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	names := backupTables(t, db)
	if len(names) != 1 {
		t.Fatalf("backup tables = %v, want one", names)
	}
	suffix := strings.TrimPrefix(names[0], "inbounds_xray_backup_")
	if len(suffix) != len("20060102_150405") {
		t.Errorf("backup table %q has no yyyymmdd_hhmmss suffix", names[0])
	}
}
