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

// TestLoginLimiterBoundsItsRecordTable 是内存耗尽面的护栏。
//
// 记录是按来源 IP 建的，而来源 IP 由对面决定：一个手握 /64 IPv6 的攻击者
// 每个地址试一次错密码，就能让这张表无限长大。历史实现只在"恰好查到那个
// IP 且它已过期"时才删记录，也就是说攻击者的地址永远不会被回收。
func TestLoginLimiterBoundsItsRecordTable(t *testing.T) {
	l := &LoginLimiter{
		records:        map[string]*loginFailRecord{},
		WindowDuration: time.Hour,
		MaxFailures:    5,
		LockDuration:   time.Hour,
		MaxRecords:     64,
	}

	for i := 0; i < 5000; i++ {
		l.RecordFail(fmt.Sprintf("2001:db8::%x", i))
	}

	if got := l.Size(); got > l.MaxRecords {
		t.Errorf("the table grew to %d records, want at most %d", got, l.MaxRecords)
	}
	if l.Size() == 0 {
		t.Error("the table was emptied entirely; the limiter would stop working")
	}
}

// TestLoginLimiterEvictionKeepsLockedIPs 保证淘汰不会变成"洗掉自己的锁"的手段。
func TestLoginLimiterEvictionKeepsLockedIPs(t *testing.T) {
	l := &LoginLimiter{
		records:        map[string]*loginFailRecord{},
		WindowDuration: time.Minute,
		MaxFailures:    2,
		LockDuration:   time.Hour, // 锁比窗口长得多，失效时刻最晚
		MaxRecords:     32,
	}

	const attacker = "203.0.113.99"
	l.RecordFail(attacker)
	l.RecordFail(attacker)
	if locked, _ := l.IsLocked(attacker); !locked {
		t.Fatal("the IP should be locked after reaching MaxFailures")
	}

	// 用一大批一次性 IP 把表撑爆。
	for i := 0; i < 2000; i++ {
		l.RecordFail(fmt.Sprintf("198.51.100.%d:%d", i%256, i))
	}

	if locked, _ := l.IsLocked(attacker); !locked {
		t.Error("flooding the limiter with fresh IPs washed away an existing lock")
	}
}

// TestLoginLimiterExpiredRecordsAreReclaimed 覆盖惰性回收：
// 表满时先扫掉失效记录，够用就不必淘汰任何活跃条目。
func TestLoginLimiterExpiredRecordsAreReclaimed(t *testing.T) {
	l := &LoginLimiter{
		records:        map[string]*loginFailRecord{},
		WindowDuration: 20 * time.Millisecond,
		MaxFailures:    5,
		LockDuration:   20 * time.Millisecond,
		MaxRecords:     16,
	}
	for i := 0; i < l.MaxRecords; i++ {
		l.RecordFail(fmt.Sprintf("192.0.2.%d", i))
	}
	if got := l.Size(); got != l.MaxRecords {
		t.Fatalf("seeded %d records, want %d", got, l.MaxRecords)
	}

	time.Sleep(40 * time.Millisecond)
	l.RecordFail("192.0.2.200")

	// 全部过期，所以扫描之后应该只剩刚刚那一条。
	if got := l.Size(); got != 1 {
		t.Errorf("%d records survived the sweep, want only the fresh one", got)
	}
}

// TestLoginLimiterStartsAFreshWindowAfterTheLockExpires 固化解锁语义。
//
// 锁一到期，之前那些失败就不该再算数了。否则在 LockDuration 短于
// WindowDuration 的配置下，解锁后的第一次手滑会立刻把人又锁回去。
func TestLoginLimiterStartsAFreshWindowAfterTheLockExpires(t *testing.T) {
	l := &LoginLimiter{
		records:        map[string]*loginFailRecord{},
		WindowDuration: time.Minute,
		MaxFailures:    2,
		LockDuration:   20 * time.Millisecond,
	}
	const ip = "192.0.2.77"
	l.RecordFail(ip)
	l.RecordFail(ip)
	time.Sleep(40 * time.Millisecond)

	locked, remaining := l.RecordFail(ip)
	if locked {
		t.Fatal("the first failure after the lock expired locked the IP again")
	}
	if remaining != 1 {
		t.Errorf("remaining = %d, want a fresh budget of %d", remaining, l.MaxFailures-1)
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
