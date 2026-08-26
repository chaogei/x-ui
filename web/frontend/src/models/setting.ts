/**
 * setting.ts —— 面板设置对象，字段与后端 web/entity.AllSetting 对齐。
 */
export interface AllSetting {
  webListen: string
  webPort: number
  webCertFile: string
  webKeyFile: string
  webBasePath: string
  /** 前置反向代理的 CIDR 白名单，逗号分隔。留空 = 不信任任何代理。 */
  webTrustedProxies: string
  /** 订阅链接里使用的对外地址；留空则回落到请求的 Host 头。 */
  subAddress: string
  /** /metrics 的 bearer token；留空则只允许已登录会话抓取。 */
  metricsToken: string
  tgBotEnable: boolean
  tgBotToken: string
  tgBotChatId: number
  tgRunTime: string
  coreTemplateConfig: string
  timeLocation: string
}

export function defaultAllSetting(): AllSetting {
  return {
    webListen: '',
    webPort: 54321,
    webCertFile: '',
    webKeyFile: '',
    webBasePath: '/',
    webTrustedProxies: '',
    subAddress: '',
    metricsToken: '',
    tgBotEnable: false,
    tgBotToken: '',
    tgBotChatId: 0,
    tgRunTime: '',
    coreTemplateConfig: '',
    timeLocation: 'Asia/Shanghai',
  }
}

/** fromServer 只吸收已知字段，避免后端多返回的东西被原样回写。 */
export function fromServer(data: Partial<AllSetting> | null): AllSetting {
  const out = defaultAllSetting()
  if (!data) {
    return out
  }
  for (const key of Object.keys(out) as (keyof AllSetting)[]) {
    if (data[key] !== undefined) {
      ;(out as unknown as Record<string, unknown>)[key] = data[key]
    }
  }
  return out
}

export function settingsEqual(a: AllSetting, b: AllSetting): boolean {
  return (Object.keys(a) as (keyof AllSetting)[]).every((key) => a[key] === b[key])
}
