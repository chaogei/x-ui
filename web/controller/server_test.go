package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"x-ui/web/global"
)

// fakeWebServer 满足 global.WebServer，只为让 ServerController 能把
// 刷新任务挂到一个 cron 上。这里的 cron 建而不启动：用例自己调 Job.Run()，
// 这样"cron 侧"与"HTTP 侧"的交错由用例控制，而不是靠等 2 秒的滴答。
type fakeWebServer struct {
	cron *cron.Cron
}

func (f *fakeWebServer) GetCron() *cron.Cron     { return f.cron }
func (f *fakeWebServer) GetCtx() context.Context { return context.Background() }

// newTestServerController 造一个挂好路由的控制器，并返回它注册的刷新任务。
func newTestServerController(t *testing.T) (*ServerController, cron.Job) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	c := cron.New(cron.WithSeconds())
	global.SetWebServer(&fakeWebServer{cron: c})
	t.Cleanup(func() { global.SetWebServer(nil) })

	engine := gin.New()
	a := NewServerController(engine.Group("/"))

	entries := c.Entries()
	if len(entries) != 1 {
		t.Fatalf("the controller registered %d cron entries, want exactly the status refresh", len(entries))
	}
	return a, entries[0].Job
}

// callStatus 直接调处理函数，绕开 session 中间件：这里要验的是缓存字段的
// 并发访问，不是登录态。
func callStatus(a *ServerController) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/server/status", nil)
	a.status(c)
	return w
}

func callGetCoreVersion(a *ServerController) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/server/getCoreVersion", nil)
	a.getCoreVersion(c)
	return w
}

// TestServerControllerStatusConcurrentWithRefresh 让刷新任务与状态请求真正
// 同时跑。加锁之前，这个用例在 -race 下必然报 lastStatus / lastGetStatusTime
// 上的读写冲突。
func TestServerControllerStatusConcurrentWithRefresh(t *testing.T) {
	a, refresh := newTestServerController(t)

	const (
		refreshers = 4
		readers    = 8
		rounds     = 25
	)

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < refreshers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				refresh.Run()
			}
		}()
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				if code := callStatus(a).Code; code != http.StatusOK {
					t.Errorf("status handler returned %d, want 200", code)
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	// 跑完之后缓存必须是一份可序列化的完整快照，而不是被撕成两半的中间态。
	body := callStatus(a).Body.Bytes()
	var envelope struct {
		Success bool `json:"success"`
		Obj     *struct {
			Cpu float64 `json:"cpu"`
			Mem struct {
				Total uint64 `json:"total"`
			} `json:"mem"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("status response is not the panel envelope: %v\nbody: %s", err, body)
	}
	if !envelope.Success {
		t.Fatalf("status response reports failure: %s", body)
	}
	if envelope.Obj == nil {
		t.Fatalf("status response carries no snapshot after %d refreshes: %s", refreshers*rounds, body)
	}
	if envelope.Obj.Mem.Total == 0 {
		t.Errorf("the published snapshot has no total memory, so it was never filled in: %s", body)
	}
}

// TestServerControllerVersionsConcurrentReads 并发打缓存命中的版本接口。
// 缓存有效期内不会出网，所以这个用例在离线环境下也是确定的。
func TestServerControllerVersionsConcurrentReads(t *testing.T) {
	a, _ := newTestServerController(t)

	want := []string{"v1.9.0", "v1.8.14"}
	a.mu.Lock()
	a.lastVersions = want
	a.lastGetVersionsTime = time.Now()
	a.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := callGetCoreVersion(a).Body.Bytes()
			var envelope struct {
				Success bool     `json:"success"`
				Obj     []string `json:"obj"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Errorf("version response is not the panel envelope: %v\nbody: %s", err, body)
				return
			}
			if !envelope.Success || len(envelope.Obj) != len(want) {
				t.Errorf("version response = %s, want the cached %v", body, want)
			}
		}()
	}
	wg.Wait()
}

// TestServerControllerSkipsRefreshWhenNobodyIsWatching 固定"没人看面板就不采集"
// 这条省电规则：它依赖 lastGetStatusTime，而那个字段现在在锁后面。
func TestServerControllerSkipsRefreshWhenNobodyIsWatching(t *testing.T) {
	a, refresh := newTestServerController(t)

	a.mu.Lock()
	a.lastGetStatusTime = time.Now().Add(-4 * time.Minute)
	a.mu.Unlock()

	refresh.Run()
	a.mu.RLock()
	stale := a.lastStatus
	a.mu.RUnlock()
	if stale != nil {
		t.Fatalf("the refresh job collected a snapshot even though the last status request was 4 minutes ago")
	}

	// 一次请求就把它唤醒。
	callStatus(a)
	refresh.Run()
	a.mu.RLock()
	fresh := a.lastStatus
	a.mu.RUnlock()
	if fresh == nil {
		t.Fatalf("the refresh job stayed idle right after a status request")
	}
}
