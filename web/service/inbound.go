package service

import (
	"fmt"
	"time"
	"x-ui/core"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/util/common"

	"gorm.io/gorm"
)

type InboundService struct {
}

func (s *InboundService) GetInbounds(userId int) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Where("user_id = ?", userId).Find(&inbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return inbounds, nil
}

func (s *InboundService) GetAllInbounds() ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Find(&inbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return inbounds, nil
}

// checkPortConflict 按协议 network 类型校验同端口冲突。
// 返回的协议是已占用且冲突的其中一个，空字符串表示无冲突。
func (s *InboundService) checkPortConflict(port int, protocol model.Protocol, ignoreId int) (conflict model.Protocol, err error) {
	db := database.GetDB()
	var existing []*model.Inbound
	q := db.Model(model.Inbound{}).Where("port = ?", port)
	if ignoreId > 0 {
		q = q.Where("id != ?", ignoreId)
	}
	if err = q.Find(&existing).Error; err != nil {
		return "", err
	}
	for _, ib := range existing {
		if protocol.ConflictsWith(ib.Protocol) {
			return ib.Protocol, nil
		}
	}
	return "", nil
}

func (s *InboundService) AddInbound(inbound *model.Inbound) error {
	conflict, err := s.checkPortConflict(inbound.Port, inbound.Protocol, 0)
	if err != nil {
		return err
	}
	if conflict != "" {
		return common.NewErrorf("端口 %d 已被协议 %s 占用（与 %s 网络类型冲突）", inbound.Port, conflict, inbound.Protocol)
	}
	db := database.GetDB()
	return db.Save(inbound).Error
}

func (s *InboundService) AddInbounds(inbounds []*model.Inbound) error {
	for _, inbound := range inbounds {
		conflict, err := s.checkPortConflict(inbound.Port, inbound.Protocol, 0)
		if err != nil {
			return err
		}
		if conflict != "" {
			return common.NewErrorf("端口 %d 已被协议 %s 占用（与 %s 网络类型冲突）", inbound.Port, conflict, inbound.Protocol)
		}
	}

	db := database.GetDB()
	tx := db.Begin()
	var err error
	defer func() {
		if err == nil {
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}()

	for _, inbound := range inbounds {
		err = tx.Save(inbound).Error
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *InboundService) DelInbound(id int) error {
	db := database.GetDB()
	return db.Delete(model.Inbound{}, id).Error
}

func (s *InboundService) GetInbound(id int) (*model.Inbound, error) {
	db := database.GetDB()
	inbound := &model.Inbound{}
	err := db.Model(model.Inbound{}).First(inbound, id).Error
	if err != nil {
		return nil, err
	}
	return inbound, nil
}

func (s *InboundService) UpdateInbound(inbound *model.Inbound) error {
	conflict, err := s.checkPortConflict(inbound.Port, inbound.Protocol, inbound.Id)
	if err != nil {
		return err
	}
	if conflict != "" {
		return common.NewErrorf("端口 %d 已被协议 %s 占用（与 %s 网络类型冲突）", inbound.Port, conflict, inbound.Protocol)
	}

	oldInbound, err := s.GetInbound(inbound.Id)
	if err != nil {
		return err
	}
	oldInbound.Up = inbound.Up
	oldInbound.Down = inbound.Down
	oldInbound.Total = inbound.Total
	oldInbound.Remark = inbound.Remark
	oldInbound.Enable = inbound.Enable
	oldInbound.ExpiryTime = inbound.ExpiryTime
	oldInbound.Listen = inbound.Listen
	oldInbound.Port = inbound.Port
	oldInbound.Protocol = inbound.Protocol
	oldInbound.Settings = inbound.Settings
	oldInbound.Sniffing = inbound.Sniffing
	// Tag 需要协议参与唯一性构成：同端口的 TCP/UDP 协议共存时需要各自起名。
	oldInbound.Tag = fmt.Sprintf("inbound-%v-%s", inbound.Port, inbound.Protocol)

	db := database.GetDB()
	return db.Save(oldInbound).Error
}

func (s *InboundService) AddTraffic(traffics []*core.Traffic) (err error) {
	if len(traffics) == 0 {
		return nil
	}
	db := database.GetDB()
	db = db.Model(model.Inbound{})
	tx := db.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()
	for _, traffic := range traffics {
		if traffic.IsInbound {
			err = tx.Where("tag = ?", traffic.Tag).
				UpdateColumn("up", gorm.Expr("up + ?", traffic.Up)).
				UpdateColumn("down", gorm.Expr("down + ?", traffic.Down)).
				Error
			if err != nil {
				return
			}
		}
	}
	return
}

func (s *InboundService) DisableInvalidInbounds() (int64, error) {
	db := database.GetDB()
	now := time.Now().Unix() * 1000
	result := db.Model(model.Inbound{}).
		Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ?", now, true).
		Update("enable", false)
	err := result.Error
	count := result.RowsAffected
	return count, err
}
