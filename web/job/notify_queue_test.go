package job

import (
	"sync"
	"testing"
	"time"
)

// TestNotifyQueueDropsInsteadOfGrowing 是这次改动要挡住的形态。
//
// 登录失败会触发通知，而登录失败是对面可以随意制造的事件。旧实现每条通知
// 起一个 goroutine，每个 goroutine 都要读一次设置再往 Telegram 发一个最长
// 10 秒的请求；Telegram 不通时它们全都堆着。这里改成有界队列，满了就丢。
//
// 断言的是"总会丢"而不是"第几条丢"：工作 goroutine 随时可能取走一条，
// 具体在哪一次投递上撞满取决于调度。
func TestNotifyQueueDropsInsteadOfGrowing(t *testing.T) {
	const capacity = 4
	q := newNotifyQueue(capacity)
	noop := func() {}

	for i := 0; i < capacity; i++ {
		if !q.submit(noop) {
			t.Fatalf("submission %d was dropped while the buffer still had room", i)
		}
	}
	// 工作 goroutine 可能已经取走了一两条，所以这里不断投递直到看到丢弃，
	// 上限远大于容量以免误判。
	sawDrop := false
	for i := 0; i < capacity*100; i++ {
		if !q.submit(noop) {
			sawDrop = true
			break
		}
	}
	if !sawDrop {
		t.Fatal("the queue accepted an unbounded number of notifications")
	}
	if q.droppedCount() == 0 {
		t.Error("dropped notifications were not counted")
	}
	if got := len(q.tasks); got > capacity {
		t.Errorf("the buffer holds %d tasks, want at most %d", got, capacity)
	}
}

// TestNotifyQueueDeliversInOrder 保证队列真的会把活干完，
// 而且是串行的——通知之间没有并发需求，串行让"一条卡住"不会放大成一片。
func TestNotifyQueueDeliversInOrder(t *testing.T) {
	q := newNotifyQueue(16)

	var mu sync.Mutex
	var seen []int
	var wg sync.WaitGroup

	const n = 10
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		if !q.submit(func() {
			mu.Lock()
			seen = append(seen, i)
			mu.Unlock()
			wg.Done()
		}) {
			t.Fatalf("task %d was dropped by an empty queue", i)
		}
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never drained the queue")
	}

	mu.Lock()
	defer mu.Unlock()
	for i, got := range seen {
		if got != i {
			t.Fatalf("task %d ran at position %d; the queue is not serial", got, i)
		}
	}
}

// TestNotifyQueueSurvivesAPanickingTask 保证一条坏通知不会带走工作 goroutine，
// 否则之后所有的通知都会静默消失。
func TestNotifyQueueSurvivesAPanickingTask(t *testing.T) {
	q := newNotifyQueue(4)

	q.submit(func() { panic("telegram exploded") })

	ran := make(chan struct{})
	if !q.submit(func() { close(ran) }) {
		t.Fatal("the follow-up task was dropped")
	}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker died with the panicking task")
	}
}
