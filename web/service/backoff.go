package service

import (
	"sync"
	"time"
)

// Backoff 是一个并发安全的指数退避闸门。
//
// 用法：
//
//	if !b.Ready() { return }        // 仍在退避窗口内，跳过本次尝试
//	if err := do(); err != nil {
//	    b.Fail()                    // 失败 → 间隔翻倍（不超过 max）
//	    return
//	}
//	b.Succeed()                     // 成功 → 立刻复位到 base
//
// 时间源可注入，测试无需 sleep。
type Backoff struct {
	mu       sync.Mutex
	base     time.Duration
	max      time.Duration
	current  time.Duration
	notUntil time.Time
	now      func() time.Time
}

// NewBackoff 构造退避器：首次失败等待 base，随后每次翻倍，封顶 max。
func NewBackoff(base, max time.Duration) *Backoff {
	if base <= 0 {
		base = time.Second
	}
	if max < base {
		max = base
	}
	return &Backoff{
		base:    base,
		max:     max,
		current: base,
		now:     time.Now,
	}
}

// SetClock 替换时间源，仅供测试使用。
func (b *Backoff) SetClock(now func() time.Time) {
	b.mu.Lock()
	b.now = now
	b.mu.Unlock()
}

// Ready 判断当前是否允许再次尝试。
func (b *Backoff) Ready() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.now().Before(b.notUntil)
}

// Fail 记录一次失败，返回下一次允许尝试前需要等待的时长。
func (b *Backoff) Fail() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	delay := b.current
	b.notUntil = b.now().Add(delay)
	if next := b.current * 2; next <= b.max {
		b.current = next
	} else {
		b.current = b.max
	}
	return delay
}

// Succeed 复位退避状态。
func (b *Backoff) Succeed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.current = b.base
	b.notUntil = time.Time{}
}

// Current 返回下一次失败将采用的等待时长，供测试与日志使用。
func (b *Backoff) Current() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.current
}
