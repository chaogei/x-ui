package job

import (
	"sync"
	"sync/atomic"

	"x-ui/logger"
	"x-ui/util/common"
)

// notifyQueueCapacity 是待发通知的缓冲深度。
//
// 通知是尽力而为的旁路信息，积压几十条已经说明 Telegram 那边不通了；
// 再攒下去只是把内存换成一堆过时的消息。
const notifyQueueCapacity = 64

// notifyQueue 是通知投递的有界队列：一条固定的工作 goroutine + 定长缓冲。
//
// 为什么不能一条通知起一个 goroutine：登录失败会触发通知，而登录失败是
// 对面可以随意制造的事件。每个 goroutine 都要先读一次设置（走数据库），
// 再往 api.telegram.org 发一个最长 10 秒的请求——Telegram 不可达时它们
// 全都卡在那里。几千次并发失败登录就是几千个 goroutine 加几千次数据库读，
// 而这些通知说的还是同一件事。
//
// 队列满时丢弃并计数：丢通知远好过让面板陪着 Telegram 一起卡死。
type notifyQueue struct {
	tasks   chan func()
	start   sync.Once
	dropped atomic.Int64
}

func newNotifyQueue(capacity int) *notifyQueue {
	return &notifyQueue{tasks: make(chan func(), capacity)}
}

// notifier 是进程级的默认队列。工作 goroutine 在第一次投递时启动，
// 之后随进程存活——它只在 channel 上等待，不占用任何资源。
var notifier = newNotifyQueue(notifyQueueCapacity)

// submit 把任务放进队列，返回是否入队成功。
// 队列满时立即返回 false，绝不阻塞调用方（通常是 HTTP 处理协程）。
func (q *notifyQueue) submit(task func()) bool {
	q.start.Do(func() { go q.run() })
	select {
	case q.tasks <- task:
		return true
	default:
		total := q.dropped.Add(1)
		// 只在第一条和之后每 100 条时出声：队列满通常意味着刷屏，
		// 日志本身不该跟着一起刷。
		if total == 1 || total%100 == 0 {
			logger.Warningf("telegram notify queue is full, dropped %d notifications so far", total)
		}
		return false
	}
}

// run 是唯一的工作 goroutine。
func (q *notifyQueue) run() {
	for task := range q.tasks {
		func() {
			defer common.Recover("tgbot notify")
			task()
		}()
	}
}

// droppedCount 返回累计丢弃数，供测试与诊断使用。
func (q *notifyQueue) droppedCount() int64 {
	return q.dropped.Load()
}
