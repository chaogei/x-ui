package singbox

import "sync"

// ringBuffer 是一个固定容量的线程安全环形缓冲，
// 满后 Push 会覆盖最旧元素，用于暂存 sing-box 的 stdout/stderr 行。
type ringBuffer struct {
	mu       sync.Mutex
	items    []string
	capacity int
	head     int
	size     int
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = 1
	}
	return &ringBuffer{
		items:    make([]string, capacity),
		capacity: capacity,
	}
}

// Push 追加一行；若已满，覆盖最旧一行。
func (r *ringBuffer) Push(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := (r.head + r.size) % r.capacity
	r.items[idx] = s
	if r.size < r.capacity {
		r.size++
	} else {
		r.head = (r.head + 1) % r.capacity
	}
}

// Snapshot 返回当前缓冲内容的拷贝（按写入顺序）。
func (r *ringBuffer) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.items[(r.head+i)%r.capacity]
	}
	return out
}

// Reset 清空缓冲，供进程重启时复用实例。
func (r *ringBuffer) Reset() {
	r.mu.Lock()
	r.head = 0
	r.size = 0
	r.mu.Unlock()
}
