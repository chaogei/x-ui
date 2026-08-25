package service

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLoginLimiterLocksAfterMaxFailures(t *testing.T) {
	l := NewLoginLimiter()
	const ip = "203.0.113.7"

	for i := 1; i < l.MaxFailures; i++ {
		locked, remaining := l.RecordFail(ip)
		if locked {
			t.Fatalf("locked after only %d failures, MaxFailures is %d", i, l.MaxFailures)
		}
		if want := l.MaxFailures - i; remaining != want {
			t.Errorf("after %d failures remaining = %d, want %d", i, remaining, want)
		}
		if isLocked, _ := l.IsLocked(ip); isLocked {
			t.Fatalf("IsLocked true after only %d failures", i)
		}
	}

	locked, remaining := l.RecordFail(ip)
	if !locked {
		t.Fatalf("not locked after reaching MaxFailures (%d)", l.MaxFailures)
	}
	if remaining != 0 {
		t.Errorf("remaining = %d after lock, want 0", remaining)
	}

	isLocked, retryAfter := l.IsLocked(ip)
	if !isLocked {
		t.Fatal("IsLocked should report the lock")
	}
	if retryAfter <= 0 || retryAfter > l.LockDuration {
		t.Errorf("retryAfter = %v, want a value in (0, %v]", retryAfter, l.LockDuration)
	}
}

func TestLoginLimiterIsolatesIPs(t *testing.T) {
	l := NewLoginLimiter()
	for i := 0; i < l.MaxFailures; i++ {
		l.RecordFail("198.51.100.1")
	}
	if locked, _ := l.IsLocked("198.51.100.1"); !locked {
		t.Fatal("the failing IP should be locked")
	}
	if locked, _ := l.IsLocked("198.51.100.2"); locked {
		t.Error("a different IP must not inherit the lock")
	}
}

func TestLoginLimiterResetOnSuccess(t *testing.T) {
	l := NewLoginLimiter()
	const ip = "192.0.2.5"
	l.RecordFail(ip)
	l.RecordFail(ip)
	l.Reset(ip)

	locked, remaining := l.RecordFail(ip)
	if locked {
		t.Fatal("a single failure after Reset should not lock")
	}
	if want := l.MaxFailures - 1; remaining != want {
		t.Errorf("remaining = %d after Reset+1 failure, want %d", remaining, want)
	}
}

// TestLoginLimiterWindowExpiry 验证窗口过期后失败计数重新开始。
func TestLoginLimiterWindowExpiry(t *testing.T) {
	l := &LoginLimiter{
		records:        map[string]*loginFailRecord{},
		WindowDuration: 30 * time.Millisecond,
		MaxFailures:    3,
		LockDuration:   time.Minute,
	}
	const ip = "192.0.2.9"
	l.RecordFail(ip)
	l.RecordFail(ip)

	time.Sleep(50 * time.Millisecond)

	// 窗口已过，这一次算作新窗口的第一次失败，不应触发锁定。
	locked, remaining := l.RecordFail(ip)
	if locked {
		t.Fatal("failures from an expired window must not count toward the lock")
	}
	if remaining != 2 {
		t.Errorf("remaining = %d, want 2 (a fresh window of 3)", remaining)
	}
}

// TestLoginLimiterUnlocksAfterLockDuration 验证锁定到期后自动放行。
func TestLoginLimiterUnlocksAfterLockDuration(t *testing.T) {
	l := &LoginLimiter{
		records:        map[string]*loginFailRecord{},
		WindowDuration: time.Minute,
		MaxFailures:    2,
		LockDuration:   30 * time.Millisecond,
	}
	const ip = "192.0.2.10"
	l.RecordFail(ip)
	l.RecordFail(ip)
	if locked, _ := l.IsLocked(ip); !locked {
		t.Fatal("should be locked")
	}
	time.Sleep(50 * time.Millisecond)
	if locked, _ := l.IsLocked(ip); locked {
		t.Error("lock should have expired")
	}
}

// TestLoginLimiterConcurrent 在 -race 下验证内部 map 的互斥。
func TestLoginLimiterConcurrent(t *testing.T) {
	l := NewLoginLimiter()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ip := fmt.Sprintf("10.0.0.%d", i%4)
			for j := 0; j < 100; j++ {
				l.RecordFail(ip)
				l.IsLocked(ip)
				if j%10 == 0 {
					l.Reset(ip)
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestLoginLimiterConcurrentSameIPLocksExactlyOnce 保证并发失败下阈值判定不漂移：
// 恰好有一次 RecordFail 返回 locked=true。
func TestLoginLimiterConcurrentSameIPReachesLock(t *testing.T) {
	l := NewLoginLimiter()
	const ip = "198.51.100.42"

	var wg sync.WaitGroup
	lockedCount := 0
	var mu sync.Mutex
	for i := 0; i < l.MaxFailures; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if locked, _ := l.RecordFail(ip); locked {
				mu.Lock()
				lockedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if lockedCount != 1 {
		t.Errorf("%d concurrent failures produced %d lock transitions, want exactly 1", l.MaxFailures, lockedCount)
	}
	if locked, _ := l.IsLocked(ip); !locked {
		t.Error("IP should be locked after MaxFailures concurrent failures")
	}
}
