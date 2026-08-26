/**
 * inbound.ts —— 数据库里那一行入站记录的前端视图。
 */
import { Inbound, isShareableProtocol } from './core'

export const ONE_GB = 1024 * 1024 * 1024

/** DBInboundData 与后端 database/model.Inbound 的 JSON 字段一一对应。 */
export interface DBInboundData {
  id: number
  userId: number
  up: number
  down: number
  total: number
  remark: string
  enable: boolean
  expiryTime: number
  listen: string
  port: number
  protocol: string
  settings: string
  tag: string
  sniffing: string
}

export class DBInbound implements DBInboundData {
  id = 0
  userId = 0
  up = 0
  down = 0
  total = 0
  remark = ''
  enable = true
  expiryTime = 0
  listen = ''
  port = 0
  protocol = ''
  settings = ''
  tag = ''
  sniffing = ''

  constructor(data?: Partial<DBInboundData>) {
    if (!data) {
      return
    }
    for (const key of Object.keys(this) as (keyof DBInboundData)[]) {
      if (data[key] !== undefined) {
        // 逐字段赋值而不是 Object.assign：后端多返回一个字段时不会被静默吸进来。
        ;(this as Record<string, unknown>)[key] = data[key]
      }
    }
  }

  get totalGB(): number {
    return Math.round((this.total / ONE_GB) * 100) / 100
  }

  set totalGB(gb: number) {
    this.total = Math.round((Number(gb) || 0) * ONE_GB)
  }

  /** address 是生成分享链接时用的服务器地址。 */
  get address(): string {
    if (this.listen && this.listen !== '0.0.0.0' && this.listen !== '::') {
      return this.listen
    }
    return location.hostname
  }

  get isExpiry(): boolean {
    return this.expiryTime > 0 && this.expiryTime < Date.now()
  }

  toInbound(): Inbound {
    return Inbound.fromJson({
      port: this.port,
      listen: this.listen,
      protocol: this.protocol,
      settings: this.settings ? JSON.parse(this.settings) : {},
      tag: this.tag,
      sniffing: this.sniffing ? JSON.parse(this.sniffing) : {},
    })
  }

  /** hasLink 委托后端 spec 的 Shareable 字段，前端不再维护第二份名单。 */
  hasLink(): boolean {
    return isShareableProtocol(this.protocol)
  }

  genLink(): string {
    return this.toInbound().genLink(this.address, this.remark)
  }
}

/** Client 对应后端 database/model.Client。 */
export interface Client {
  id: number
  inboundId: number
  email: string
  enable: boolean
  up: number
  down: number
  total: number
  expiryTime: number
  uuid: string
  password: string
  username: string
  extra: string
  subToken: string
  lastSeen: number
}

export function emptyClient(inboundId: number): Client {
  return {
    id: 0,
    inboundId,
    email: '',
    enable: true,
    up: 0,
    down: 0,
    total: 0,
    expiryTime: 0,
    uuid: '',
    password: '',
    username: '',
    extra: '',
    subToken: '',
    lastSeen: 0,
  }
}
