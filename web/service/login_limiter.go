package service

import (
	"sort"
	"sync"
	"time"
)

// defaultMaxLoginRecords 是失败记录表的容量上限。
//
// 这不是调优参数而是一道闸门：记录是按来源 IP 建的，而来源 IP 由对面决定。
// 一个手握 /64 IPv6 或者僵尸网络的攻击者，每个地址试一次错密码就能让
// 面板凭空长出任意多条记录——限流器本身变成了内存耗尽的入口。
//
// 一万条记录约合几 MB，对任何真实部署都绰绰有余（正常情况下这张表里
// 只有几个手滑的管理员）。
const defaultMaxLoginRecords = 10000

// loginEvictFraction 是一次淘汰的比例（1/N）。
//
// 满了才淘汰，而且一次腾出一批：否则每来一个新 IP 都要做一次全表扫描，
// 攻击者只要持续换 IP 就能把面板的 CPU 钉在这个循环上。
const loginEvictFraction = 8

// LoginLimiter 是面板登录失败 IP 限流器。
//
// 策略（固定窗口 + 滑动清理）：
//   - 在 WindowDuration 窗口内，同一 IP 累计失败达到 MaxFailures 次 → 锁定
//   - 锁定持续 LockDuration，锁定期内所有请求直接拒绝
//   - 锁定到期或窗口走完，该 IP 的记录作废，下一次失败从头计数
//   - 登录成功立即清除该 IP 的失败计数
//
// 内存存储足够（重启即清空也可接受）。通过内部 Mutex 保证并发安全，
// 回收采用惰性策略（访问时清理 + 满了才批量淘汰）以避免引入后台 goroutine。
type LoginLimiter struct {
	mu      sync.Mutex
	records map[string]*loginFailRecord

	WindowDuration time.Duration
	MaxFailures    int
	LockDuration   time.Duration

	// MaxRecords 是记录表的容量上限，<= 0 时取 defaultMaxLoginRecords。
	MaxRecords int
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
		MaxRecords:     defaultMaxLoginRecords,
	}
}

// expiry 返回这条记录彻底失效的时刻。
// 锁定中的记录活到锁定结束，否则活到当前窗口结束。
func (r *loginFailRecord) expiry(window time.Duration) time.Time {
	if !r.lockedUntil.IsZero() {
		return r.lockedUntil
	}
	return r.firstFailAt.Add(window)
}

func (r *loginFailRecord) stale(now time.Time, window time.Duration) bool {
	return now.After(r.expiry(window))
}

func (l *LoginLimiter) maxRecords() int {
	if l.MaxRecords > 0 {
		return l.MaxRecords
	}
	return defaultMaxLoginRecords
}

// IsLocked 查询 IP 是否仍处于锁定期内。
// 顺带清理已经失效的记录，避免内存长期增长。
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
	if rec.stale(now, l.WindowDuration) {
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
	if ok && rec.stale(now, l.WindowDuration) {
		// 窗口走完或锁定到期：这条记录已经无效，从头开始计数。
		ok = false
	}
	if !ok {
		l.makeRoom(now)
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

// Size 返回当前记录条数，供测试与诊断使用。
func (l *LoginLimiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.records)
}

// makeRoom 在插入新记录前保证表没有超出上限。调用方必须持有 l.mu。
//
// 两步：先扫掉已经失效的记录（正常负载下这一步就够了），仍然满就按
// 失效时刻从早到晚批量淘汰。被牺牲的总是最接近失效的那些——锁定中的
// IP 失效最晚，因此最后才会被丢，攻击者没法用一堆新 IP 把自己的锁洗掉。
func (l *LoginLimiter) makeRoom(now time.Time) {
	limit := l.maxRecords()
	if len(l.records) < limit {
		return
	}

	for ip, rec := range l.records {
		if rec.stale(now, l.WindowDuration) {
			delete(l.records, ip)
		}
	}
	if len(l.records) < limit {
		return
	}

	type entry struct {
		ip      string
		expires time.Time
	}
	entries := make([]entry, 0, len(l.records))
	for ip, rec := range l.records {
		entries = append(entries, entry{ip: ip, expires: rec.expiry(l.WindowDuration)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].expires.Before(entries[j].expires) })

	drop := len(entries) / loginEvictFraction
	if drop < 1 {
		drop = 1
	}
	for _, e := range entries[:drop] {
		delete(l.records, e.ip)
	}
}
