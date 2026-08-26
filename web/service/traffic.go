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
// 上界来自 SQLite：拼进 FROM 的是一串 UNION ALL，而复合 SELECT 的分支数
// 硬上限是 500，超了直接报 "too many terms in compound SELECT"。
//
// 下界来自实测：100 和 50 打平（1000 个客户端各约 5.2ms），200 起开始变慢。
// 取 100，离硬上限有五倍余量。
const trafficBatchSize = 100

// foldTraffic 把内核返回的扁平计数器折成去重后的增量列表。
//
// 做三件事：
//   - 按 keep 过滤维度（入站 / 用户），不属于本次入账的计数器直接丢掉；
//   - 丢掉零增量：内核在 reset=true 下仍会返回上一周期没有流量的计数器，
//     为它们发 UPDATE 是纯粹的浪费；
//   - 合并同名条目。正常情况下 aggregateTraffic 已经去过重，这里兜底：
//     同一个键在批次里出现两次，连接更新会把那一行改写两遍，
//     结果取决于执行顺序——最好的情况也只是少记一笔。
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
// 朴素写法是每个键一条 UPDATE（历史实现在入站侧甚至是每个方向一条，
// 也就是每个键两条）。流量任务每 10 秒跑一次，一台有几百个客户端的机器
// 就是每 10 秒几百上千条语句，而且全压在同一个写事务里——期间任何别的
// 写者都只能排队。
//
// 这里按批把增量拼成一张临时关系再做连接更新：
//
//	UPDATE clients
//	   SET up = clients.up + v.up_delta,
//	       down = clients.down + v.down_delta,
//	       last_seen = ?
//	  FROM (SELECT ? AS k, ? AS up_delta, ? AS down_delta
//	        UNION ALL SELECT ?, ?, ? ...) AS v
//	 WHERE clients.email = v.k
//
// 为什么不是 `SET up = up + CASE email WHEN ? THEN ? ... END`：那种写法
// 会对匹配到的每一行重新走一遍整条 CASE，代价是批大小的平方。实测 1000 个
// 客户端一批，CASE 版要 16ms，比"每个键一条 UPDATE"的 11.6ms 还慢；
// 连接版是 5.2ms。把批切小能压住平方项，但那等于把语句数又加回来。
//
// 连接的方向保证只有出现在本批次里的行会被改写，其余行一个字节都不动。
//
// UPDATE ... FROM 需要 SQLite 3.33+（本仓库用的 modernc 驱动是 3.41）。
// 面板只支持 SQLite，不为其它方言留退路。
func applyTrafficDeltas(tx *gorm.DB, entity interface{}, keyColumn string, deltas []trafficDelta, extra map[string]interface{}) error {
	table, err := tableName(tx, entity)
	if err != nil {
		return err
	}
	// map 的遍历顺序是随机的，而占位符是按出现顺序绑定的：必须定序。
	extraColumns := make([]string, 0, len(extra))
	for column := range extra {
		extraColumns = append(extraColumns, column)
	}
	sort.Strings(extraColumns)

	for start := 0; start < len(deltas); start += trafficBatchSize {
		end := start + trafficBatchSize
		if end > len(deltas) {
			end = len(deltas)
		}
		chunk := deltas[start:end]

		var sb strings.Builder
		args := make([]interface{}, 0, len(chunk)*3+len(extraColumns))

		sb.WriteString("UPDATE ")
		sb.WriteString(table)
		sb.WriteString(" SET up = ")
		sb.WriteString(table)
		sb.WriteString(".up + v.up_delta, down = ")
		sb.WriteString(table)
		sb.WriteString(".down + v.down_delta")
		for _, column := range extraColumns {
			sb.WriteString(", ")
			sb.WriteString(column)
			sb.WriteString(" = ?")
			args = append(args, extra[column])
		}

		sb.WriteString(" FROM (")
		for i, d := range chunk {
			if i == 0 {
				sb.WriteString("SELECT ? AS k, ? AS up_delta, ? AS down_delta")
			} else {
				sb.WriteString(" UNION ALL SELECT ?, ?, ?")
			}
			args = append(args, d.Key, d.Up, d.Down)
		}
		sb.WriteString(") AS v WHERE ")
		sb.WriteString(table)
		sb.WriteString(".")
		sb.WriteString(keyColumn)
		sb.WriteString(" = v.k")

		if err := tx.Exec(sb.String(), args...).Error; err != nil {
			return err
		}
	}
	return nil
}

// tableName 通过 gorm 的 schema 解析拿到实体对应的表名，
// 而不是在 SQL 里写死字符串——命名策略变了要立刻跟着变。
func tableName(tx *gorm.DB, entity interface{}) (string, error) {
	stmt := &gorm.Statement{DB: tx}
	if err := stmt.Parse(entity); err != nil {
		return "", err
	}
	return stmt.Schema.Table, nil
}
