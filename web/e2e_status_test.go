package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

// TestE2EServerStatusUnderConcurrentRefresh 把 H1 放到完整的 HTTP 栈上跑。
//
// 这条链路以前测不到：脚手架建了 cron 却不启动，而状态缓存的数据竞争恰恰
// 发生在 cron 的刷新任务与请求 goroutine 之间。这里不等 2 秒的滴答，直接把
// 控制器注册的那个任务拿出来反复执行，同时对着 /server/status 发请求。
// -race 下这就是 H1 的回归用例。
func TestE2EServerStatusUnderConcurrentRefresh(t *testing.T) {
	p := newPanel(t)
	p.login()
	token := p.csrfToken()

	entries := p.server.cron.Entries()
	if len(entries) == 0 {
		t.Fatal("no background job was registered; the status refresher is the one this test needs")
	}

	const (
		refreshers = 3
		readers    = 6
		rounds     = 15
	)

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < refreshers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				for _, entry := range entries {
					entry.Job.Run()
				}
			}
		}()
	}

	statuses := make(chan int, readers*rounds)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				resp := p.postFormWithToken("server/status", nil, token)
				statuses <- resp.StatusCode
				resp.Body.Close()
			}
		}()
	}

	close(start)
	wg.Wait()
	close(statuses)

	for code := range statuses {
		if code != http.StatusOK {
			t.Fatalf("a concurrent status request returned %d, want 200", code)
		}
	}

	// 最后一次读到的必须是一份完整的快照，而不是被撕开的中间态。
	final := p.decode(p.postFormWithToken("server/status", nil, token))
	if !final.Success {
		t.Fatalf("the status endpoint reports failure: %s", final.Msg)
	}
	var snapshot struct {
		Cpu float64 `json:"cpu"`
		Mem struct {
			Total uint64 `json:"total"`
		} `json:"mem"`
	}
	if err := json.Unmarshal(final.Obj, &snapshot); err != nil {
		t.Fatalf("the status payload does not decode: %v\n%s", err, final.Obj)
	}
	if snapshot.Mem.Total == 0 {
		t.Errorf("the published snapshot has no total memory, so it was never filled in: %s", final.Obj)
	}
}
