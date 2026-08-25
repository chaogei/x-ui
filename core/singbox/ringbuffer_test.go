package singbox

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRingBufferBelowCapacity(t *testing.T) {
	r := newRingBuffer(5)
	r.Push("a")
	r.Push("b")
	got := strings.Join(r.Snapshot(), ",")
	if got != "a,b" {
		t.Errorf("snapshot = %q, want %q", got, "a,b")
	}
}

func TestRingBufferWrapAround(t *testing.T) {
	r := newRingBuffer(3)
	for i := 1; i <= 7; i++ {
		r.Push(fmt.Sprint(i))
	}
	// 容量 3，写入 7 条，只应保留最后 3 条且顺序不乱。
	got := strings.Join(r.Snapshot(), ",")
	if got != "5,6,7" {
		t.Errorf("after wrap-around snapshot = %q, want %q", got, "5,6,7")
	}
	if len(r.Snapshot()) != 3 {
		t.Errorf("size = %d, want 3", len(r.Snapshot()))
	}
}

func TestRingBufferExactlyCapacity(t *testing.T) {
	r := newRingBuffer(3)
	r.Push("a")
	r.Push("b")
	r.Push("c")
	if got := strings.Join(r.Snapshot(), ","); got != "a,b,c" {
		t.Errorf("snapshot = %q, want a,b,c", got)
	}
	r.Push("d")
	if got := strings.Join(r.Snapshot(), ","); got != "b,c,d" {
		t.Errorf("after one overflow snapshot = %q, want b,c,d", got)
	}
}

func TestRingBufferNonPositiveCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		r := newRingBuffer(capacity)
		r.Push("x")
		r.Push("y")
		if got := strings.Join(r.Snapshot(), ","); got != "y" {
			t.Errorf("capacity %d: snapshot = %q, want %q", capacity, got, "y")
		}
	}
}

func TestRingBufferReset(t *testing.T) {
	r := newRingBuffer(3)
	r.Push("a")
	r.Reset()
	if len(r.Snapshot()) != 0 {
		t.Errorf("snapshot after reset = %v, want empty", r.Snapshot())
	}
	r.Push("b")
	if got := strings.Join(r.Snapshot(), ","); got != "b" {
		t.Errorf("snapshot after reset+push = %q, want b", got)
	}
}

// TestRingBufferConcurrent 在 -race 下验证 Push/Snapshot 的互斥。
func TestRingBufferConcurrent(t *testing.T) {
	r := newRingBuffer(64)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.Push(fmt.Sprintf("w%d-%d", i, j))
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = r.Snapshot()
			}
		}()
	}
	wg.Wait()
	if got := len(r.Snapshot()); got != 64 {
		t.Errorf("final size = %d, want 64", got)
	}
}
