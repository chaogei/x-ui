package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"x-ui/core"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/util/random"
)

// subTokenLength 是订阅 token 的字符数。
//
// 62 个可见字符 × 32 位 ≈ 190 bit 熵。订阅接口只靠这个 token 鉴权
// （没有 session、没有密码），所以宁可长一些。
const subTokenLength = 32

// ErrClientNotFound 表示按 id / token 找不到客户端。
var ErrClientNotFound = errors.New("client not found")

// ClientService 管理入站下的终端用户。
type ClientService struct{}

// GetClients 返回某条入站下的全部客户端，按 id 升序。
func (s *ClientService) GetClients(inboundID int) ([]*model.Client, error) {
	db := database.GetDB()
	var clients []*model.Client
	err := db.Model(model.Client{}).
		Where("inbound_id = ?", inboundID).
		Order("id asc").
		Find(&clients).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return clients, nil
}

// GetAllClients 返回全部客户端，供配置生成与订阅查询使用。
func (s *ClientService) GetAllClients() ([]*model.Client, error) {
	db := database.GetDB()
	var clients []*model.Client
	err := db.Model(model.Client{}).Order("id asc").Find(&clients).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return clients, nil
}

// GetClient 按主键读取。
func (s *ClientService) GetClient(id int) (*model.Client, error) {
	db := database.GetDB()
	client := &model.Client{}
	if err := db.Model(model.Client{}).First(client, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrClientNotFound
		}
		return nil, err
	}
	return client, nil
}

// GetClientBySubToken 按订阅 token 查找客户端。
//
// 空 token 直接判定为未找到，避免"数据库里恰好有一条空 token 记录"
// 就让匿名请求拿到别人的订阅。
func (s *ClientService) GetClientBySubToken(token string) (*model.Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, ErrClientNotFound
	}
	db := database.GetDB()
	client := &model.Client{}
	err := db.Model(model.Client{}).Where("sub_token = ?", token).First(client).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrClientNotFound
	}
	if err != nil {
		return nil, err
	}
	return client, nil
}

// AddClient 校验并写入一个新客户端。
//
// SubToken 由服务端生成，忽略调用方传入的值：让前端能指定订阅 token
// 等于把一个可枚举的口令交给用户去挑。
func (s *ClientService) AddClient(client *model.Client) error {
	inbound, err := (&InboundService{}).GetInbound(client.InboundId)
	if err != nil {
		return err
	}
	if err := model.ValidateClientForProtocol(inbound.Protocol, client); err != nil {
		return err
	}
	if err := s.checkSingleCredentialLimit(inbound, client.InboundId, 0); err != nil {
		return err
	}

	token, err := random.Seq(subTokenLength)
	if err != nil {
		return err
	}
	client.Id = 0
	client.SubToken = token
	client.Up = 0
	client.Down = 0
	client.LastSeen = 0

	db := database.GetDB()
	if err := db.Create(client).Error; err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("a client with email %q already exists", client.Email)
		}
		return err
	}
	return nil
}

// UpdateClient 更新可编辑字段。
//
// 刻意不接受来自请求的 Up / Down / SubToken：流量由内核统计写入，
// token 由 RotateSubToken 单独轮换。允许前端改这些字段等于允许它伪造用量。
func (s *ClientService) UpdateClient(client *model.Client) error {
	existing, err := s.GetClient(client.Id)
	if err != nil {
		return err
	}
	inbound, err := (&InboundService{}).GetInbound(existing.InboundId)
	if err != nil {
		return err
	}
	client.InboundId = existing.InboundId
	if err := model.ValidateClientForProtocol(inbound.Protocol, client); err != nil {
		return err
	}

	existing.Email = client.Email
	existing.Enable = client.Enable
	existing.Total = client.Total
	existing.ExpiryTime = client.ExpiryTime
	existing.UUID = client.UUID
	existing.Password = client.Password
	existing.Username = client.Username
	existing.Extra = client.Extra

	// up / down / last_seen 同样要 Omit：读出整行再写回去，等于把这中间
	// 流量任务记下的字节抹掉。忽略请求里的值只挡住了伪造，挡不住这个时序。
	db := database.GetDB()
	if err := db.Omit("up", "down", "last_seen").Save(existing).Error; err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("a client with email %q already exists", client.Email)
		}
		return err
	}
	return nil
}

// DelClient 删除一个客户端。
func (s *ClientService) DelClient(id int) error {
	db := database.GetDB()
	result := db.Delete(model.Client{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrClientNotFound
	}
	return nil
}

// DelClientsByInbound 在删除入站时清理其下的客户端，避免孤儿记录
// 继续占用全局唯一的 email 与 sub_token。
func (s *ClientService) DelClientsByInbound(inboundID int) error {
	db := database.GetDB()
	return db.Where("inbound_id = ?", inboundID).Delete(model.Client{}).Error
}

// RotateSubToken 重新生成订阅 token，使旧订阅链接立即失效。
func (s *ClientService) RotateSubToken(id int) (string, error) {
	client, err := s.GetClient(id)
	if err != nil {
		return "", err
	}
	token, err := random.Seq(subTokenLength)
	if err != nil {
		return "", err
	}
	db := database.GetDB()
	if err := db.Model(model.Client{}).Where("id = ?", id).Update("sub_token", token).Error; err != nil {
		return "", err
	}
	client.SubToken = token
	return token, nil
}

// ResetClientTraffic 清零某个客户端的累计流量。
func (s *ClientService) ResetClientTraffic(id int) error {
	db := database.GetDB()
	return db.Model(model.Client{}).Where("id = ?", id).
		Updates(map[string]interface{}{"up": 0, "down": 0}).Error
}

// ActiveClientsByInbound 返回按入站分组的、当前应下发的客户端。
//
// 过滤规则见 model.Client.IsActive：禁用、超配额、已过期的用户不会进配置。
func (s *ClientService) ActiveClientsByInbound(now int64) (map[int][]*model.Client, error) {
	clients, err := s.GetAllClients()
	if err != nil {
		return nil, err
	}
	out := make(map[int][]*model.Client)
	for _, c := range clients {
		if !c.IsActive(now) {
			continue
		}
		out[c.InboundId] = append(out[c.InboundId], c)
	}
	return out, nil
}

// AddTraffic 把 user 维度的流量累加到对应客户端。
//
// 说明流量归属的真实边界（不粉饰）：
//
//   - 有 name 字段的协议（vmess/vless/trojan/hysteria2/tuic/anytls/
//     shadowtls/naive/socks/http/mixed）能拿到真正的按用户计数，
//     计数器由 sing-box 在 experimental.v2ray_api.stats.users 白名单命中时创建。
//   - shadowsocks 的凭证挂在 settings 顶层，没有用户名可言，
//     它的流量只出现在入站维度上，客户端行的 up/down 会一直是 0。
//     配额与到期对这类客户端依然生效，但生效依据是入站总量与时间，不是个人用量。
//
// 匹配不到 email 的计数器会被忽略：那通常是刚被删掉的用户留下的最后一批字节。
func (s *ClientService) AddTraffic(traffics []*core.Traffic) error {
	deltas := foldTraffic(traffics, isUserTraffic)
	if len(deltas) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	// 整批一个事务：计数器已经在内核侧清零，半批落库等于凭空丢字节。
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		return applyTrafficDeltas(tx, model.Client{}, "email", deltas,
			map[string]interface{}{"last_seen": now})
	})
}

// DisableInvalidClients 关停超配额或已到期的客户端，返回受影响行数。
//
// 与 DisableInvalidInbounds 对称：定时任务发现有变更时触发一次内核重启，
// 让这些用户真的从运行中的配置里消失，而不只是在面板上变灰。
func (s *ClientService) DisableInvalidClients() (int64, error) {
	db := database.GetDB()
	now := time.Now().UnixMilli()
	result := db.Model(model.Client{}).
		Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ?", now, true).
		Update("enable", false)
	return result.RowsAffected, result.Error
}

// checkSingleCredentialLimit 拦住"给顶层凭证协议加第二个客户端"。
//
// shadowsocks 的 UserSchema.Container 为空，settings 顶层只放得下一份
// password。若放任新增，ApplyClients 会在生成配置时报错，届时整个内核起不来，
// 而用户看到的只是"添加成功"。
func (s *ClientService) checkSingleCredentialLimit(inbound *model.Inbound, inboundID, excludeID int) error {
	names := model.StatsUserNames(inbound.Protocol, []*model.Client{{Email: "probe"}})
	if len(names) > 0 {
		// Container 非空的协议支持任意多用户。
		return nil
	}
	existing, err := s.GetClients(inboundID)
	if err != nil {
		return err
	}
	for _, c := range existing {
		if c.Id != excludeID {
			return fmt.Errorf("protocol %s supports a single client; edit or remove %q first",
				inbound.Protocol, c.Email)
		}
	}
	return nil
}

// isUniqueViolation 判断错误是否来自唯一索引冲突。
//
// 只做字符串匹配：glebarez/sqlite 不导出错误码常量，为一个提示语引一个
// 驱动内部包不值得。判断失败最坏情况是把冲突当成普通错误回显，不影响正确性。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint failed")
}
