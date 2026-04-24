package service

import (
	"sync"
	"time"
)

// LoginLimiter 是面板登录失败 IP 限流器。
//
// 策略（固定窗口 + 滑动清理）：
//   - 在 WindowDuration 窗口内，同一 IP 累计失败达到 MaxFailures 次 → 锁定
//   - 锁定持续 LockDuration，锁定期内所有请求直接拒绝
//   - 登录成功立即清除该 IP 的失败计数
//
// 内存存储足够（面板 IP 空间有限，重启即清空也可接受）。
// 通过内部 Mutex 保证并发安全，GC 采用惰性策略（访问时扫描清理）以避免引入后台 goroutine。
type LoginLimiter struct {
	mu      sync.Mutex
	records map[string]*loginFailRecord

	WindowDuration time.Duration
	MaxFailures    int
	LockDuration   time.Duration
}

type loginFailRecord struct {
	count       int
	firstFailAt time.Time
	lockedUntil time.Time
}

// NewLoginLimiter 构造面板默认策略的限流器：10 分钟窗口 5 次失败 → 锁 15 分钟。
func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{
		records:        make(map[string]*loginFailRecord),
		WindowDuration: 10 * time.Minute,
		MaxFailures:    5,
		LockDuration:   15 * time.Minute,
	}
}

// IsLocked 查询 IP 是否仍处于锁定期内。
// 若已过期会顺便清理记录，避免内存长期增长。
func (l *LoginLimiter) IsLocked(ip string) (locked bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[ip]
	if !ok {
		return false, 0
	}
	now := time.Now()
	if !rec.lockedUntil.IsZero() && now.Before(rec.lockedUntil) {
		return true, rec.lockedUntil.Sub(now)
	}
	if now.Sub(rec.firstFailAt) > l.WindowDuration {
		delete(l.records, ip)
	}
	return false, 0
}

// RecordFail 登记一次失败，若累计达到阈值则进入锁定态。
// 返回本次失败后是否已进入锁定、剩余可重试次数。
func (l *LoginLimiter) RecordFail(ip string) (locked bool, remaining int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	rec, ok := l.records[ip]
	if !ok || now.Sub(rec.firstFailAt) > l.WindowDuration {
		rec = &loginFailRecord{firstFailAt: now}
		l.records[ip] = rec
	}
	rec.count++
	if rec.count >= l.MaxFailures {
		rec.lockedUntil = now.Add(l.LockDuration)
		return true, 0
	}
	return false, l.MaxFailures - rec.count
}

// Reset 清除 IP 的失败记录，登录成功后调用。
func (l *LoginLimiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.records, ip)
}
