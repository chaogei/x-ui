package service

import (
	"errors"
	"fmt"
	"time"
	"x-ui/core"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/util/common"

	"gorm.io/gorm"
)

// ErrInboundNotFound 表示按 id 找不到入站。
var ErrInboundNotFound = errors.New("inbound not found")

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
	if err := ValidateInbound(inbound); err != nil {
		return err
	}
	conflict, err := s.checkPortConflict(inbound.Port, inbound.Protocol, 0)
	if err != nil {
		return err
	}
	if conflict != "" {
		return common.NewErrorf("port %d is already used by protocol %s (conflicting network type with %s)", inbound.Port, conflict, inbound.Protocol)
	}
	db := database.GetDB()
	return db.Save(inbound).Error
}

func (s *InboundService) AddInbounds(inbounds []*model.Inbound) error {
	for _, inbound := range inbounds {
		if err := ValidateInbound(inbound); err != nil {
			return err
		}
		conflict, err := s.checkPortConflict(inbound.Port, inbound.Protocol, 0)
		if err != nil {
			return err
		}
		if conflict != "" {
			return common.NewErrorf("port %d is already used by protocol %s (conflicting network type with %s)", inbound.Port, conflict, inbound.Protocol)
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

// DelInbound 删除入站，并连带清理其下的客户端。
//
// 不清理的话，孤儿客户端会继续占着全局唯一的 email 与 sub_token：
// 那些订阅链接仍然可用，却指向一条不存在的入站。
func (s *InboundService) DelInbound(id int) error {
	db := database.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("inbound_id = ?", id).Delete(model.Client{}).Error; err != nil {
			return err
		}
		return tx.Delete(model.Inbound{}, id).Error
	})
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

// UpdateInbound 更新入站的可编辑字段。
//
// 刻意不接受请求里的 Up / Down —— 与 ClientService.UpdateClient 同一条规矩。
// 表单里的那两个数是页面加载那一刻的快照，而流量任务每 10 秒就往同一行里
// 加一次字节：把快照写回去，等于把这期间统计到的流量抹掉。改个备注、调个
// 端口，用户的用量就凭空少一截，配额和 Telegram 日报跟着一起错。
//
// 计数器只有两条合法的写入路径：流量任务的累加，以及 ResetTraffic 的清零。
func (s *InboundService) UpdateInbound(inbound *model.Inbound) error {
	if err := ValidateInbound(inbound); err != nil {
		return err
	}
	conflict, err := s.checkPortConflict(inbound.Port, inbound.Protocol, inbound.Id)
	if err != nil {
		return err
	}
	if conflict != "" {
		return common.NewErrorf("port %d is already used by protocol %s (conflicting network type with %s)", inbound.Port, conflict, inbound.Protocol)
	}

	oldInbound, err := s.GetInbound(inbound.Id)
	if err != nil {
		return err
	}
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

	// Omit 那两列，否则 Save 会把 GetInbound 那一刻读到的计数器整行写回去：
	// 不看表单也一样丢字节——丢的是"读出来到写回去"之间入账的那批。
	db := database.GetDB()
	return db.Omit("up", "down").Save(oldInbound).Error
}

// ResetTraffic 把某条入站的累计流量清零。
//
// 单独一条路径而不是"更新时顺便写 up/down"：清零是一个明确的意图，
// 表单里那两个陈旧的数字不是。
//
// 不触发内核重启：生成配置只看 enable，与计数器无关。客户端那边之所以要重启，
// 是因为 ActiveClientsByInbound 会按 up+down 是否超配额过滤用户，清零可能让
// 一个被停用的用户重新进入配置——入站没有这层过滤。
func (s *InboundService) ResetTraffic(id int) error {
	result := database.GetDB().Model(model.Inbound{}).Where("id = ?", id).
		Updates(map[string]interface{}{"up": 0, "down": 0})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInboundNotFound
	}
	return nil
}

// AddTraffic 把 inbound 维度的流量累加到对应入站行。
//
// 与 ClientService.AddTraffic 消费的是同一批计数器，但两者取的是不同维度：
// 这里累加 inbound>>><tag>>>>traffic>>><dir>，那边累加
// user>>><email>>>>traffic>>><dir>。同一批字节在两张表各记一次是有意的
// （入站看总量、客户端看配额），不是重复计账。
//
// 整批走一个事务并合成尽量少的语句：计数器是 reset-on-read 的，
// 内核那边已经清零，这里要么整批落库，要么整批回滚由调用方重投，
// 绝不能出现"前一半写进去了，后一半丢了"。
func (s *InboundService) AddTraffic(traffics []*core.Traffic) error {
	deltas := foldTraffic(traffics, isInboundTraffic)
	if len(deltas) == 0 {
		return nil
	}
	// db.Transaction 会把 Commit 的错误一并返回。
	// 历史实现用 tx.Begin() + defer tx.Commit()，提交失败（磁盘满、锁超时）
	// 时 AddTraffic 照样返回 nil，那一批字节就这么无声无息地没了。
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		return applyTrafficDeltas(tx, model.Inbound{}, "tag", deltas, nil)
	})
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
