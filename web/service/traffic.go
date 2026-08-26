package service

import (
	"sort"
	"strings"

	"gorm.io/gorm"

	"x-ui/core"
)

// trafficDelta 是一个记账键（入站 tag 或客户端 email）在本批次里的增量。
type trafficDelta struct {
	Key  string
	Up   int64
	Down int64
}

// trafficBatchSize 是单条 UPDATE 语句最多折进去的键数。
//
// 每个键要占 5 个绑定变量（两个 CASE 分支各 2 个 + IN 列表 1 个），
// 100 个键 = 500 个变量，离 SQLite 的上限还很远，同时把语句长度
// 控制在解析器不会退化的量级。
const trafficBatchSize = 100

// foldTraffic 把内核返回的扁平计数器折成去重后的增量列表。
//
// 做三件事：
//   - 按 keep 过滤维度（入站 / 用户），不属于本次入账的计数器直接丢掉；
//   - 丢掉零增量：内核在 reset=true 下仍会返回上一周期没有流量的计数器，
//     为它们发 UPDATE 是纯粹的浪费；
//   - 合并同名条目。正常情况下 aggregateTraffic 已经去过重，这里兜底
//     防止同一个键在一条 CASE 里出现两次——SQL 的 CASE 只会命中第一个分支，
//     那样后面的字节会被静默吞掉。
//
// 返回值按 Key 排序，让生成的 SQL 在相同输入下逐字节稳定（便于测试与日志比对）。
func foldTraffic(traffics []*core.Traffic, keep func(*core.Traffic) bool) []trafficDelta {
	if len(traffics) == 0 {
		return nil
	}
	index := make(map[string]*trafficDelta, len(traffics))
	for _, t := range traffics {
		if t == nil || t.Tag == "" || !keep(t) {
			continue
		}
		if t.Up == 0 && t.Down == 0 {
			continue
		}
		d, ok := index[t.Tag]
		if !ok {
			index[t.Tag] = &trafficDelta{Key: t.Tag, Up: t.Up, Down: t.Down}
			continue
		}
		d.Up += t.Up
		d.Down += t.Down
	}
	if len(index) == 0 {
		return nil
	}
	out := make([]trafficDelta, 0, len(index))
	for _, d := range index {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// isInboundTraffic / isUserTraffic 是 foldTraffic 的两个维度谓词。
//
// 两个维度携带的是同一批字节，但落在不同的表：入站行记录"这条入站总共
// 搬了多少"，客户端行记录"这个人用掉了多少配额"。它们各自独立地被
// 面板展示与熔断使用，不存在重复计账。
func isInboundTraffic(t *core.Traffic) bool { return t.IsInbound }
func isUserTraffic(t *core.Traffic) bool    { return t.IsUser }

// applyTrafficDeltas 用尽可能少的语句把增量累加到目标表。
//
// 朴素写法是每个键一条 UPDATE（甚至每个方向一条，也就是每个键两条）。
// 流量任务每 10 秒跑一次，一台有 200 个客户端的机器就是每 10 秒 400 次
// 往返；SQLite 是进程内的，但每条语句仍要过一遍 prepare/bind/step，
// 而且全都压在同一个写事务里，期间别的写者只能等着。
//
// 这里改成按批合成一条语句：
//
//	UPDATE clients
//	   SET up   = up   + CASE email WHEN ? THEN ? ... ELSE 0 END,
//	       down = down + CASE email WHEN ? THEN ? ... ELSE 0 END,
//	       last_seen = ?
//	 WHERE email IN (?, ...)
//
// ELSE 0 与 WHERE 的组合保证只有出现在本批次里的行会被改写。
func applyTrafficDeltas(tx *gorm.DB, entity interface{}, keyColumn string, deltas []trafficDelta, extra map[string]interface{}) error {
	for start := 0; start < len(deltas); start += trafficBatchSize {
		end := start + trafficBatchSize
		if end > len(deltas) {
			end = len(deltas)
		}
		chunk := deltas[start:end]

		keys := make([]interface{}, 0, len(chunk))
		upArgs := make([]interface{}, 0, len(chunk)*2)
		downArgs := make([]interface{}, 0, len(chunk)*2)
		var branches strings.Builder
		for _, d := range chunk {
			branches.WriteString(" WHEN ? THEN ?")
			keys = append(keys, d.Key)
			upArgs = append(upArgs, d.Key, d.Up)
			downArgs = append(downArgs, d.Key, d.Down)
		}
		caseSQL := "CASE " + keyColumn + branches.String() + " ELSE 0 END"

		values := make(map[string]interface{}, len(extra)+2)
		for k, v := range extra {
			values[k] = v
		}
		values["up"] = gorm.Expr("up + "+caseSQL, upArgs...)
		values["down"] = gorm.Expr("down + "+caseSQL, downArgs...)

		err := tx.Model(entity).
			Where(keyColumn+" IN ?", keys).
			Updates(values).Error
		if err != nil {
			return err
		}
	}
	return nil
}
