/**
 * http.ts —— 面板 API 客户端。
 *
 * 与后端的两条约定不能动：
 *   1. 请求体是 application/x-www-form-urlencoded（gin 的 ShouldBind 按 form tag 绑定）；
 *   2. 非幂等方法必须带 X-CSRF-Token 头，值来自服务端渲染的 <meta name="csrf-token">。
 *      从 meta 读而不是从注入的 boot 对象读，是为了让"CSRF token 由页面下发"这件事
 *      在 HTML 里可见——端到端测试也正是这样抓 token 的。
 */
import { message } from 'ant-design-vue'
import axios from 'axios'

import { boot } from './boot'

/** 后端统一的 {success,msg,obj} 响应体。 */
export interface Msg<T = unknown> {
  success: boolean
  msg: string
  obj: T | null
}

function csrfToken(): string {
  const meta = document.querySelector('meta[name="csrf-token"]')
  return meta?.getAttribute('content') ?? boot.csrfToken
}

/** encodeForm 把一个对象展开成 form-urlencoded 串。 */
function encodeForm(data: unknown): string {
  const params = new URLSearchParams()

  const put = (key: string, value: unknown): void => {
    if (value === undefined || value === null) {
      return
    }
    if (Array.isArray(value)) {
      // arrayFormat=repeat：`a=1&a=2`，与 gin 的切片绑定一致。
      value.forEach((item) => put(key, item))
      return
    }
    if (typeof value === 'object') {
      for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
        put(`${key}[${k}]`, v)
      }
      return
    }
    params.append(key, String(value))
  }

  if (data && typeof data === 'object') {
    for (const [key, value] of Object.entries(data as Record<string, unknown>)) {
      put(key, value)
    }
  }
  return params.toString()
}

const client = axios.create({
  baseURL: boot.basePath,
  headers: {
    'X-Requested-With': 'XMLHttpRequest',
  },
  // 面板可能被塞进很慢的链路（换内核会下载几十 MB），但也不能永远挂着。
  timeout: 120_000,
})

client.interceptors.request.use((config) => {
  const method = (config.method ?? 'get').toLowerCase()
  if (method !== 'get' && method !== 'head' && method !== 'options') {
    config.headers.set('Content-Type', 'application/x-www-form-urlencoded; charset=UTF-8')
    config.headers.set('X-CSRF-Token', csrfToken())
    config.data = encodeForm(config.data)
  }
  return config
})

function toMsg<T>(data: unknown): Msg<T> {
  if (data && typeof data === 'object' && 'success' in (data as Record<string, unknown>)) {
    const d = data as { success: boolean; msg?: string; obj?: T }
    return { success: !!d.success, msg: d.msg ?? '', obj: d.obj ?? null }
  }
  return { success: true, msg: '', obj: (data as T) ?? null }
}

function toast<T>(msg: Msg<T>): void {
  if (!msg.msg) {
    return
  }
  if (msg.success) {
    message.success(msg.msg)
  } else {
    message.error(msg.msg)
  }
}

/**
 * post 发一次面板 API 调用。
 *
 * 网络层异常也折叠成 {success:false}，让调用方只需要看一个字段。
 * 会话过期时后端返回的是 200 + success:false，因此 4xx/5xx 只可能是
 * 真正的传输故障或 CSRF 被拒——两种都值得弹出来。
 */
export async function post<T = unknown>(url: string, data?: unknown, silent = false): Promise<Msg<T>> {
  let msg: Msg<T>
  try {
    const resp = await client.post(url, data)
    msg = toMsg<T>(resp.data)
  } catch (e) {
    msg = { success: false, msg: String(e), obj: null }
  }
  if (!silent) {
    toast(msg)
  }
  return msg
}

export async function get<T = unknown>(url: string, silent = false): Promise<Msg<T>> {
  let msg: Msg<T>
  try {
    const resp = await client.get(url)
    msg = toMsg<T>(resp.data)
  } catch (e) {
    msg = { success: false, msg: String(e), obj: null }
  }
  if (!silent) {
    toast(msg)
  }
  return msg
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
