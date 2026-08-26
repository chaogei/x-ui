package service

import (
	"strconv"
	"testing"

	"gorm.io/gorm"

	"x-ui/core"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/testutil"
)

// seedTrafficFixture 建一条入站和 n 个客户端，返回一批各带增量的计数器。
func seedTrafficFixture(b testing.TB, n int) []*core.Traffic {
	b.Helper()

	testutil.InitDB(b)
	inbound := &model.Inbound{
		UserId: 1, Enable: true, Port: 40000, Protocol: model.VMess,
		Settings: `{"users":[]}`, Tag: "bench-inbound",
	}
	if err := (&InboundService{}).AddInbound(inbound); err != nil {
		b.Fatalf("seed inbound: %v", err)
	}

	cs := &ClientService{}
	traffics := make([]*core.Traffic, 0, n)
	for i := 0; i < n; i++ {
		email := "bench" + strconv.Itoa(i) + "@x"
		if err := cs.AddClient(&model.Client{
			InboundId: inbound.Id, Email: email, Enable: true,
			UUID: "uuid-" + strconv.Itoa(i),
		}); err != nil {
			b.Fatalf("seed client %d: %v", i, err)
		}
		traffics = append(traffics, &core.Traffic{IsUser: true, Tag: email, Up: 1024, Down: 2048})
	}
	return traffics
}

// addTrafficPerKey 复现改动前的写法：每个客户端一条 UPDATE。
// 只存在于基准测试里，作为对照组。
func addTrafficPerKey(traffics []*core.Traffic) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		for _, t := range traffics {
			if !t.IsUser || (t.Up == 0 && t.Down == 0) {
				continue
			}
			err := tx.Model(model.Client{}).
				Where("email = ?", t.Tag).
				Updates(map[string]interface{}{
					"up":        gorm.Expr("up + ?", t.Up),
					"down":      gorm.Expr("down + ?", t.Down),
					"last_seen": 1,
				}).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// benchSizes 覆盖从"家用小面板"到"卖号机器"的客户端规模。
var benchSizes = []int{50, 200, 1000}

// BenchmarkClientAddTrafficBatched / BenchmarkClientAddTrafficPerKey 量的是
// 流量任务每 10 秒付出的那一次代价：一整批客户端流量的入账。
//
// 两者的差距随客户端数量拉开：单次事务的提交开销是固定的，语句数不是。
func BenchmarkClientAddTrafficBatched(b *testing.B) {
	s := &ClientService{}
	for _, size := range benchSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			traffics := seedTrafficFixture(b, size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.AddTraffic(traffics); err != nil {
					b.Fatalf("add traffic: %v", err)
				}
			}
		})
	}
}

func BenchmarkClientAddTrafficPerKey(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			traffics := seedTrafficFixture(b, size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := addTrafficPerKey(traffics); err != nil {
					b.Fatalf("add traffic: %v", err)
				}
			}
		})
	}
}

func BenchmarkFoldTraffic(b *testing.B) {
	traffics := make([]*core.Traffic, 0, 400)
	for i := 0; i < 200; i++ {
		traffics = append(traffics,
			&core.Traffic{IsUser: true, Tag: "u" + strconv.Itoa(i), Up: 1, Down: 1},
			&core.Traffic{IsInbound: true, Tag: "in" + strconv.Itoa(i), Up: 1, Down: 1},
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := foldTraffic(traffics, isUserTraffic); len(got) != 200 {
			b.Fatalf("folded %d entries, want 200", len(got))
		}
	}
}
