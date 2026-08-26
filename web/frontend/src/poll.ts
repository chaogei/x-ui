/**
 * poll.ts —— 前台可见时才跑的轮询。
 *
 * 面板的状态页每两秒问一次 server/status。最朴素的写法是
 * `while (!stopped) { await refresh(); await sleep(2000) }`，它有三个毛病：
 *
 *   1. 标签页切到后台照样问。面板通常是开着不管的那一类页面，一整天下来就是
 *      几万次没人看的请求，浏览器还会因此让整个后台标签页保持唤醒。
 *   2. 面板重启（换内核、改端口）期间每次请求都要等到超时才失败，失败之后
 *      立刻再来一次，等于用固定节奏往一台正在起来的机器上砸请求。
 *   3. 单次请求慢过一个周期时，虽然 await 挡住了并发，但恢复之后会连着补发
 *      好几轮 —— 而这些响应携带的都是过期数据。
 *
 * 这里换成"自排队的一次性定时器"：一轮结束才安排下一轮，因此永远只有一个
 * 请求在飞；失败按指数退避；document.hidden 时干脆不排队，回到前台立刻补一次。
 */
import { onBeforeUnmount, onMounted } from 'vue'

/**
 * PollTick 是一轮轮询要做的事。
 *
 * 抛异常或显式返回 false 都算这一轮失败，会触发退避。返回 undefined 视为成功，
 * 这样只关心"跑一下"的调用方不需要写 `return true`。
 */
export type PollTick = () => Promise<boolean | void>

export interface PollOptions {
  /** 一切正常时两轮之间的间隔，毫秒。 */
  interval: number
  /** 连续失败时退避的上限，毫秒。默认 30 秒。 */
  maxInterval?: number
}

/**
 * usePolling 在组件挂载期间按 options 轮询 tick，卸载时停掉。
 *
 * 时序保证：任意时刻至多一个 tick 在执行，且至多一个定时器在等待。
 */
export function usePolling(tick: PollTick, options: PollOptions): void {
  const { interval, maxInterval = 30_000 } = options

  let timer: ReturnType<typeof setTimeout> | undefined
  /** 一轮正在飞。用它挡住 visibilitychange 触发的插队。 */
  let inFlight = false
  let disposed = false
  let failures = 0

  const paused = (): boolean => disposed || document.hidden

  function cancelTimer(): void {
    if (timer !== undefined) {
      clearTimeout(timer)
      timer = undefined
    }
  }

  function scheduleNext(): void {
    cancelTimer()
    if (paused()) {
      // 后台时不排队。回到前台由 visibilitychange 重新启动，
      // 中间攒下的轮次一次也不补 —— 用户要看的是"现在"，不是这段历史。
      return
    }
    // 2s → 4s → 8s … 直到 maxInterval。面板重启期间连续失败十几次是常态，
    // 退避让它安静下来，恢复后第一次成功就把间隔打回原状。
    const delay = failures === 0 ? interval : Math.min(interval * 2 ** failures, maxInterval)
    timer = setTimeout(() => {
      timer = undefined
      void run()
    }, delay)
  }

  async function run(): Promise<void> {
    if (inFlight || paused()) {
      // 已经有一轮在飞：不叠第二个请求。它结束时会自己排下一轮。
      return
    }
    inFlight = true
    try {
      failures = (await tick()) === false ? failures + 1 : 0
    } catch {
      failures += 1
    } finally {
      inFlight = false
    }
    scheduleNext()
  }

  function onVisibilityChange(): void {
    if (document.hidden) {
      cancelTimer()
      return
    }
    // 切回来时屏幕上还是离开那一刻的数字，等下一个间隔太迟了，立刻刷一次。
    // 顺带清掉退避：后台期间攒的失败次数不该拖慢刚回到前台的第一次刷新。
    failures = 0
    void run()
  }

  onMounted(() => {
    document.addEventListener('visibilitychange', onVisibilityChange)
    void run()
  })

  onBeforeUnmount(() => {
    disposed = true
    cancelTimer()
    document.removeEventListener('visibilitychange', onVisibilityChange)
  })
}
