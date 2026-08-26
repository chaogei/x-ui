package web

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingJob 在被调用时阻塞，直到用例放行。
type blockingJob struct {
	started chan struct{}
	release chan struct{}
	runs    atomic.Int32
}

func (j *blockingJob) Run() {
	j.runs.Add(1)
	j.started <- struct{}{}
	<-j.release
}

// TestBackgroundJobChainSkipsOverlappingRuns 固定住"上一轮没跑完就跳过"。
//
// 没有这层护栏时，一次跑了 40 秒的内核重启会在 @every 10s 上叠出四个副本，
// 它们互相抢同一把锁，越堆越慢。
func TestBackgroundJobChainSkipsOverlappingRuns(t *testing.T) {
	j := &blockingJob{started: make(chan struct{}, 1), release: make(chan struct{})}
	wrapped := backgroundJobChain().Then(j)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		wrapped.Run()
	}()

	select {
	case <-j.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first run never started")
	}

	// 第一轮还卡着，后面这几次必须直接返回。
	for i := 0; i < 3; i++ {
		done := make(chan struct{})
		go func() {
			wrapped.Run()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("an overlapping tick blocked instead of being skipped")
		}
	}
	if got := j.runs.Load(); got != 1 {
		t.Errorf("the job body ran %d times while one run was in flight, want 1", got)
	}

	close(j.release)
	wg.Wait()

	// 前一轮结束之后，下一次滴答照常执行。
	j.release = make(chan struct{})
	close(j.release)
	wrapped.Run()
	if got := j.runs.Load(); got != 2 {
		t.Errorf("the job ran %d times in total, want the skipped ticks not to block later ones", got)
	}
}

type panickingJob struct{ runs atomic.Int32 }

func (j *panickingJob) Run() {
	j.runs.Add(1)
	panic("the database went away")
}

// TestBackgroundJobChainContainsPanics 一个后台任务里的 panic 只能损失这一轮。
// cron.New 默认没有这层，一次 panic 会把整个面板进程带走。
//
// 连跑三轮还有第二层用意：cron 的 SkipIfStillRunning 归还令牌的那行没有
// 放进 defer，所以 panic 一旦穿过它，这个任务就永远停在"跳过"上。
// Recover 必须包在里层，这个用例就是那道闩。
func TestBackgroundJobChainContainsPanics(t *testing.T) {
	j := &panickingJob{}
	wrapped := backgroundJobChain().Then(j)

	for i := 0; i < 3; i++ {
		wrapped.Run()
	}

	if got := j.runs.Load(); got != 3 {
		t.Errorf("the job ran %d times, want 3 — a panic must not wedge the skip guard shut", got)
	}
}
