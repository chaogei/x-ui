/**
 * boot.ts —— 服务端注入数据的唯一读取点。
 *
 * 后端在每个页面的 <head> 里写一行 `window.__XUI__ = {...}`（见 web/html/app.html
 * 与 web/controller/util.go 的 html()）。
 * 之所以注入而不是让前端再发几个 XHR：
 *   - CSRF token 必须在第一个写请求之前就位，异步拉取会有时序窗口；
 *   - i18n 词典的单一来源是后端的 translation/*.toml，前端再维护一份必然漂移；
 *   - 协议元数据来自 core/singbox/spec 注册表，同理。
 */

/** 后端 core/singbox/spec.Spec 的前端镜像。 */
export interface UserSchema {
  container: string
  identifier: string
  credentials?: string[] | null
}

export interface ProtocolSpec {
  key: string
  network: string
  is_endpoint: boolean
  shareable: boolean
  sniffable: boolean
  users: UserSchema
}

export interface LangOption {
  Code: string
  Name: string
  Tag: string
}

export type PageName = 'login' | 'status' | 'inbounds' | 'setting'

export interface BootData {
  basePath: string
  page: PageName
  csrfToken: string
  /** 生效的词典标签（zh-Hans / zh-Hant / en-US）。 */
  lang: string
  /** 用户显式选择的语言 code；跟随浏览器时为空串。 */
  langCode: string
  languages: LangOption[]
  i18n: Record<string, string>
  protocols: ProtocolSpec[]
  version: string
  requestUri: string
  /** 管理员仍在用首次启动生成的随机口令。 */
  initialCredentials: boolean
}

declare global {
  interface Window {
    __XUI__?: Partial<BootData>
  }
}

const raw = window.__XUI__ ?? {}

export const boot: BootData = {
  basePath: raw.basePath ?? '/',
  page: (raw.page as PageName) ?? 'login',
  csrfToken: raw.csrfToken ?? '',
  lang: raw.lang ?? 'zh-Hans',
  langCode: raw.langCode ?? '',
  languages: raw.languages ?? [],
  i18n: raw.i18n ?? {},
  protocols: raw.protocols ?? [],
  version: raw.version ?? '',
  requestUri: raw.requestUri ?? '/',
  initialCredentials: raw.initialCredentials ?? false,
}

/**
 * t 翻译一个 message id。
 *
 * 漏配的 key 原样回显而不是渲染成空白：空白会让人以为是布局出了问题，
 * 而裸露的 key 一眼就能定位到该补哪个 TOML 条目。
 */
export function t(key: string): string {
  const v = boot.i18n[key]
  return v === undefined || v === '' ? key : v
}

/** 把面板内相对路径拼成带 basePath 的绝对路径。 */
export function panelUrl(path: string): string {
  return boot.basePath + path.replace(/^\/+/, '')
}

/**
 * setLang 写入/清除 `lang` cookie 然后刷新。
 *
 * 空值表示"跟随浏览器"：删掉 cookie 让后端回落到 Accept-Language。
 */
export function setLang(code: string): void {
  if (code) {
    const oneYear = 365 * 24 * 60 * 60
    document.cookie = `lang=${encodeURIComponent(code)};path=${boot.basePath};max-age=${oneYear};samesite=lax`
  } else {
    document.cookie = `lang=;path=${boot.basePath};max-age=0;samesite=lax`
  }
  location.reload()
}
