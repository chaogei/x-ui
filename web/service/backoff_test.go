package service

import (
	"sync"
	"testing"
	"time"
)

// fakeClock 让退避逻辑无需真实 sleep 即可测试。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestBackoff(base, max time.Duration) (*Backoff, *fakeClock) {
	clock := &fakeClock{t: time.Unix(1700000000, 0)}
	b := NewBackoff(base, max)
	b.SetClock(clock.Now)
	return b, clock
}

func TestBackoffReadyInitially(t *testing.T) {
	b, _ := newTestBackoff(10*time.Second, time.Minute)
	if !b.Ready() {
		t.Fatal("a fresh backoff must allow the first attempt immediately")
	}
}

func TestBackoffDoublesUntilCeiling(t *testing.T) {
	b, clock := newTestBackoff(10*time.Second, 40*time.Second)

	wantDelays := []time.Duration{
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		40 * time.Second, // 封顶后不再增长
		40 * time.Second,
	}
	for i, want := range wantDelays {
		if !b.Ready() {
			t.Fatalf("attempt %d: expected Ready after advancing past the window", i)
		}
		got := b.Fail()
		if got != want {
			t.Errorf("failure %d waited %v, want %v", i, got, want)
		}
		if b.Ready() {
			t.Errorf("failure %d: Ready must be false during the backoff window", i)
		}
		clock.Advance(got)
	}
}

func TestBackoffResetsOnSuccess(t *testing.T) {
	b, clock := newTestBackoff(10*time.Second, time.Minute)
	b.Fail()
	clock.Advance(10 * time.Second)
	b.Fail() // 现在 current 已经涨到 40s 档位
	clock.Advance(time.Hour)

	b.Succeed()
	if !b.Ready() {
		t.Fatal("Succeed must clear the backoff window immediately")
	}
	if got := b.Current(); got != 10*time.Second {
		t.Errorf("after Succeed the next delay is %v, want the base 10s", got)
	}
}

func TestBackoffBlocksInsideWindow(t *testing.T) {
	b, clock := newTestBackoff(10*time.Second, time.Minute)
	b.Fail()

	clock.Advance(9 * time.Second)
	if b.Ready() {
		t.Error("Ready before the window elapsed")
	}
	clock.Advance(time.Second)
	if !b.Ready() {
		t.Error("not Ready once the window elapsed exactly")
	}
}

func TestBackoffNormalisesBadArguments(t *testing.T) {
	b := NewBackoff(0, 0)
	if b.Current() <= 0 {
		t.Errorf("non-positive base should be normalised, got %v", b.Current())
	}
	b2 := NewBackoff(time.Minute, time.Second)
	if got := b2.Fail(); got != time.Minute {
		t.Errorf("max below base should be raised to base; first delay = %v", got)
	}
}

// TestBackoffConcurrent 在 -race 下验证内部状态的互斥。
func TestBackoffConcurrent(t *testing.T) {
	b := NewBackoff(time.Millisecond, 10*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				b.Ready()
				if j%3 == 0 {
					b.Fail()
				} else if j%7 == 0 {
					b.Succeed()
				}
				b.Current()
			}
		}(i)
	}
	wg.Wait()
}
