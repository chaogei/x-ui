package database

import (
	"strings"
	"time"
	"x-ui/database/model"
	"x-ui/logger"

	"gorm.io/gorm"
)

// dropLegacyColumns 清除历史版本遗留但新 schema 已不再使用的列。
//
// GORM 的 AutoMigrate 只会新增列/索引，不会删除，因此需显式调用。
// 当前列表：
//   - stream_settings：旧 Xray 时代字段，sing-box 无此概念；此前作为占位保留。
func dropLegacyColumns(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasTable(&model.Inbound{}) {
		return nil
	}
	legacyColumns := []string{"stream_settings"}
	for _, col := range legacyColumns {
		if !migrator.HasColumn(&model.Inbound{}, col) {
			continue
		}
		if err := migrator.DropColumn(&model.Inbound{}, col); err != nil {
			return err
		}
		logger.Infof("dropped legacy column inbounds.%s", col)
	}
	// Port 字段去掉了 gorm:"unique"；旧数据库上可能遗留 UNIQUE 索引，需主动清理。
	legacyIndexes := []string{"idx_inbounds_port"}
	for _, idx := range legacyIndexes {
		if !migrator.HasIndex(&model.Inbound{}, idx) {
			continue
		}
		if err := migrator.DropIndex(&model.Inbound{}, idx); err != nil {
			logger.Warningf("drop legacy index %s failed: %v", idx, err)
		}
	}
	return nil
}

// migrateFromXraySchema 检测数据库是否为旧 Xray 时代的 inbounds schema，
// 若是则将其重命名为带时间戳的备份表，腾出 inbounds 表名给新的 sing-box schema。
//
// 判断策略：
//  1. 存在 inbounds 表；
//  2. 表中至少存在一行，且 protocol 列出现过仅 Xray 特有的 "Dokodemo-door"（旧常量原文），
//     或 settings 列包含 Xray 经典的 "clients":[{"id":...}] 片段。
//
// 仅做一次（备份表已存在说明已迁过）。
func migrateFromXraySchema(db *gorm.DB) error {
	if !db.Migrator().HasTable("inbounds") {
		return nil
	}

	var (
		total       int64
		hasLegacy   int64
		backupTable = "inbounds_xray_backup_" + time.Now().Format("20060102_150405")
	)
	if err := db.Table("inbounds").Count(&total).Error; err != nil {
		return err
	}
	if total == 0 {
		// 空表直接让 AutoMigrate 按新结构建表；不产生备份。
		return nil
	}

	// 采样一行判断是不是旧 schema。
	var protocol, settings string
	row := db.Table("inbounds").Select("protocol, settings").Row()
	if row == nil {
		return nil
	}
	if err := row.Scan(&protocol, &settings); err != nil {
		return err
	}
	legacyProtocols := map[string]struct{}{
		"Dokodemo-door": {},
		"mtproto":       {},
	}
	if _, ok := legacyProtocols[protocol]; ok {
		hasLegacy++
	}
	if strings.Contains(settings, "\"clients\"") && strings.Contains(settings, "\"alterId\"") {
		hasLegacy++
	}
	if strings.Contains(settings, "\"disableInsecureEncryption\"") {
		hasLegacy++
	}

	if hasLegacy == 0 {
		// 已经是 sing-box schema，或无法判断，保持现状。
		return nil
	}

	logger.Warningf("detected legacy Xray inbounds table, renaming to %s and starting fresh for sing-box", backupTable)
	if err := db.Migrator().RenameTable("inbounds", backupTable); err != nil {
		return err
	}
	return nil
}
