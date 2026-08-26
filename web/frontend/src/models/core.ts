/**
 * core.ts —— x-ui 前端数据模型层（sing-box 内核）。
 *
 * 设计要点：
 *   1. sing-box 没有独立的 streamSettings/transport 概念，TLS/transport 内嵌在
 *      每种协议自身的字段里，因此这里没有 StreamSettings 抽象。
 *   2. 每种协议的 settings 就是 sing-box inbound 的协议私有字段集（users/tls/
 *      masquerade 之类），由 Go 端与顶层字段（type/tag/listen/listen_port）合并。
 *   3. 分享链接同时存在服务端实现（web/service/sharelink.go，订阅走它）与这里的
 *      客户端实现（面板上的"复制链接/二维码"走它）。两者的取值口径必须一致，
 *      web/service/sharelink_test.go 是那一侧的护栏。
 */
import { boot, type ProtocolSpec } from '../boot'
import { randomIntRange } from '../random'

/* ================= 协议 & 常量 ================= */

/** ProtocolSpecs 是后端注册表的前端镜像，键为协议 key。 */
export const ProtocolSpecs: Record<string, ProtocolSpec> = (() => {
  const out: Record<string, ProtocolSpec> = {}
  for (const spec of boot.protocols) {
    out[spec.key] = spec
  }
  return out
})()

/** allProtocolKeys 返回与后端注册表同序的协议 key 列表。 */
export function allProtocolKeys(): string[] {
  return boot.protocols.map((s) => s.key)
}

/** Protocols 保留 `Protocols.VMESS` 这种写法，值由后端注册表派生。 */
export const Protocols: Record<string, string> = (() => {
  const out: Record<string, string> = {}
  for (const key of allProtocolKeys()) {
    out[key.toUpperCase()] = key
  }
  return Object.freeze(out)
})()

/** sing-box shadowsocks 支持的加密方式（含 2022 系列）。 */
export const SSMethods = Object.freeze({
  NONE: 'none',
  AES_256_GCM: 'aes-256-gcm',
  AES_128_GCM: 'aes-128-gcm',
  CHACHA20_POLY1305: 'chacha20-poly1305',
  XCHACHA20_POLY1305: 'xchacha20-poly1305',
  SS2022_BLAKE3_AES_128: '2022-blake3-aes-128-gcm',
  SS2022_BLAKE3_AES_256: '2022-blake3-aes-256-gcm',
  SS2022_BLAKE3_CHACHA20: '2022-blake3-chacha20-poly1305',
})

/** isSS2022 判断 SS 加密方法是否属于需要 base64 密钥的 2022 系列。 */
export function isSS2022(method: string): boolean {
  return typeof method === 'string' && method.startsWith('2022-')
}

export function isEndpointProtocol(key: string): boolean {
  return !!ProtocolSpecs[key]?.is_endpoint
}

export function isShareableProtocol(key: string): boolean {
  return !!ProtocolSpecs[key]?.shareable
}

export function isSniffableProtocol(key: string): boolean {
  return !!ProtocolSpecs[key]?.sniffable
}

/* ================= base64 ================= */

/** base64 编码任意 UTF-8 字符串（btoa 只吃 latin1）。 */
export function base64(str: string): string {
  const bytes = new TextEncoder().encode(str)
  let binary = ''
  bytes.forEach((b) => {
    binary += String.fromCharCode(b)
  })
  return btoa(binary)
}

/** safeBase64 是 URL 安全变体，用于 Shadowsocks SIP002 的 userinfo。 */
export function safeBase64(str: string): string {
  return base64(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

type Json = Record<string, any>

/* ================= TLS / Transport 嵌入结构 ================= */

/** sing-box 各协议 inbound 共用的 TLS 字段块。 */
export class TlsBlock {
  enabled: boolean
  server_name: string
  alpn: string[]
  useFile: boolean
  certificate_path: string
  key_path: string
  certificate: string
  key: string
  reality: RealityBlock | null

  constructor(enabled = false) {
    this.enabled = enabled
    this.server_name = ''
    this.alpn = []
    this.useFile = true
    this.certificate_path = ''
    this.key_path = ''
    this.certificate = ''
    this.key = ''
    this.reality = null
  }

  static fromJson(json: Json = {}): TlsBlock {
    const b = new TlsBlock(!!json.enabled)
    b.server_name = json.server_name || ''
    b.alpn = json.alpn || []
    b.useFile = !!(json.certificate_path || json.key_path) || !(json.certificate || json.key)
    b.certificate_path = json.certificate_path || ''
    b.key_path = json.key_path || ''
    b.certificate = Array.isArray(json.certificate) ? json.certificate.join('\n') : json.certificate || ''
    b.key = Array.isArray(json.key) ? json.key.join('\n') : json.key || ''
    b.reality = json.reality ? RealityBlock.fromJson(json.reality) : null
    return b
  }

  toJson(): Json {
    if (!this.enabled) {
      return { enabled: false }
    }
    const out: Json = { enabled: true }
    if (this.server_name) out.server_name = this.server_name
    if (this.alpn && this.alpn.length) out.alpn = this.alpn
    if (this.useFile) {
      if (this.certificate_path) out.certificate_path = this.certificate_path
      if (this.key_path) out.key_path = this.key_path
    } else {
      if (this.certificate) out.certificate = this.certificate.split('\n')
      if (this.key) out.key = this.key.split('\n')
    }
    if (this.reality && this.reality.enabled) out.reality = this.reality.toJson()
    return out
  }
}

/**
 * Reality 子块（仅 VLESS inbound + TLS 时使用）。
 *
 * public_key 不是 sing-box inbound 的字段（服务端只需要 private_key），
 * 但分享链接的 `pbk` 参数必须带上它，客户端才能完成 Reality 握手。
 * 早期版本漏掉了这个字段，genVlessLink 生成的链接里 pbk 恒为空、
 * 所有 Reality 节点都连不上，因此这里必须持久化保存。
 */
export class RealityBlock {
  enabled: boolean
  handshake_server: string
  handshake_port: number
  private_key: string
  public_key: string
  short_id: string[]
  max_time_difference: string

  constructor(enabled = false) {
    this.enabled = enabled
    this.handshake_server = ''
    this.handshake_port = 443
    this.private_key = ''
    this.public_key = ''
    this.short_id = ['']
    this.max_time_difference = ''
  }

  static fromJson(json: Json = {}): RealityBlock {
    const b = new RealityBlock(!!json.enabled)
    b.handshake_server = json.handshake?.server || ''
    b.handshake_port = json.handshake?.server_port || 443
    b.private_key = json.private_key || ''
    b.public_key = json.public_key || ''
    b.short_id = json.short_id || ['']
    b.max_time_difference = json.max_time_difference || ''
    return b
  }

  toJson(): Json {
    const out: Json = {
      enabled: true,
      handshake: { server: this.handshake_server, server_port: Number(this.handshake_port) || 443 },
      private_key: this.private_key,
      public_key: this.public_key,
      short_id: this.short_id && this.short_id.length ? this.short_id : [''],
    }
    if (this.max_time_difference) out.max_time_difference = this.max_time_difference
    return out
  }
}

/** sing-box 传输层字段块：websocket / http / quic / grpc / httpupgrade。 */
export class TransportBlock {
  type: string
  path: string
  host: string[]
  service_name: string
  headers: Record<string, string>

  constructor(type = '') {
    this.type = type
    this.path = '/'
    this.host = []
    this.service_name = ''
    this.headers = {}
  }

  static fromJson(json: Json | null | undefined): TransportBlock {
    if (!json || !json.type) {
      return new TransportBlock('')
    }
    const b = new TransportBlock(json.type)
    b.path = json.path || '/'
    b.host = json.host || []
    b.service_name = json.service_name || ''
    b.headers = json.headers || {}
    return b
  }

  toJson(): Json | undefined {
    if (!this.type) {
      return undefined
    }
    const out: Json = { type: this.type }
    switch (this.type) {
      case 'ws':
      case 'httpupgrade':
        out.path = this.path || '/'
        if (this.headers && Object.keys(this.headers).length) out.headers = this.headers
        break
      case 'http':
        if (this.path) out.path = this.path
        if (this.host && this.host.length) out.host = this.host
        if (this.headers && Object.keys(this.headers).length) out.headers = this.headers
        break
      case 'grpc':
        out.service_name = this.service_name || ''
        break
      case 'quic':
        // QUIC 透传 type 即可。
        break
    }
    return out
  }
}

/** sing-box inbound 顶层 sniff 系列字段块。 */
export class SniffBlock {
  sniff: boolean
  sniff_override_destination: boolean
  sniff_timeout: string
  domain_strategy: string

  constructor() {
    this.sniff = true
    this.sniff_override_destination = true
    this.sniff_timeout = ''
    this.domain_strategy = ''
  }

  static fromJson(json: Json = {}): SniffBlock {
    const b = new SniffBlock()
    b.sniff = json.sniff !== false
    b.sniff_override_destination = json.sniff_override_destination !== false
    b.sniff_timeout = json.sniff_timeout || ''
    b.domain_strategy = json.domain_strategy || ''
    return b
  }

  toJson(): Json {
    const out: Json = {
      sniff: !!this.sniff,
      sniff_override_destination: !!this.sniff_override_destination,
    }
    if (this.sniff_timeout) out.sniff_timeout = this.sniff_timeout
    if (this.domain_strategy) out.domain_strategy = this.domain_strategy
    return out
  }
}

/* ================= 入站 viewmodel ================= */

export class Inbound {
  port: number
  listen: string
  _protocol: string
  settings: Json
  tag: string
  sniff: SniffBlock

  constructor(protocol = 'vmess', settings: Json | null = null) {
    this.port = randomIntRange(10000, 60000)
    this.listen = ''
    this._protocol = protocol
    this.settings = settings ?? Inbound.defaultSettings(protocol)
    this.tag = ''
    this.sniff = new SniffBlock()
  }

  get protocol(): string {
    return this._protocol
  }

  set protocol(p: string) {
    this._protocol = p
    this.settings = Inbound.defaultSettings(p)
  }

  get isEndpoint(): boolean {
    return isEndpointProtocol(this._protocol)
  }

  get canShare(): boolean {
    return isShareableProtocol(this._protocol)
  }

  get canSniff(): boolean {
    return isSniffableProtocol(this._protocol)
  }

  /**
   * _getUserField 按 UserSchema 定位用户字段并取值。
   *
   *   - 字段名必须是该协议 UserSchema 的 identifier 或 credentials 之一，否则返回 ''
   *   - container='users' → 取 settings.users[0][field]
   *   - container=''      → 取 settings[field]（shadowsocks 的顶层 password）
   *
   * 语义边界：AnyTLS/ShadowTLS 的 identifier 是 password、不含 username，
   * 所以 this.username 对它们严格返回 ''；要展示备注名请直接读 settings.users[0].name。
   */
  _getUserField(field: string): string {
    const spec = ProtocolSpecs[this._protocol]
    if (!spec || !spec.users) {
      return ''
    }
    const { container, identifier } = spec.users
    const credentials = spec.users.credentials ?? []
    if (![identifier, ...credentials].filter(Boolean).includes(field)) {
      return ''
    }
    if (container === '') {
      return this.settings?.[field] || ''
    }
    const arr = this.settings?.[container]
    if (!Array.isArray(arr) || arr.length === 0) {
      return ''
    }
    return arr[0][field] || ''
  }

  get uuid(): string {
    return this._getUserField('uuid')
  }

  get password(): string {
    return this._getUserField('password')
  }

  get username(): string {
    return this._getUserField('username')
  }

  /** shadowsocks 独有的加密方法，与"用户"维度无关。 */
  get method(): string {
    return this._protocol === 'shadowsocks' ? this.settings.method || '' : ''
  }

  get tls(): boolean {
    return !!this.settings?.tls?.enabled
  }

  get serverName(): string {
    return this.settings?.tls?.server_name || ''
  }

  get transportType(): string {
    return this.settings?.transport?.type || ''
  }

  /**
   * defaultSettings 委托到 protocols.ts 的注册表。
   *
   * 后端新增协议但前端漏补 defaults 时，protocols.ts 在合并阶段就 fail-fast；
   * 这里只需对未知协议优雅返回空对象，保证绑定不炸。
   */
  static defaultSettings(protocol: string): Json {
    // 延迟解析：protocols.ts 依赖本文件的 TlsBlock/TransportBlock，直接 import 会成环。
    const factory = protocolDefaults[protocol]
    return factory ? factory() : {}
  }

  static fromJson(json: Json = {}): Inbound {
    const proto = json.protocol || 'vmess'
    const settings = json.settings ? InboundSettings.fromJson(json.settings) : Inbound.defaultSettings(proto)
    const inbound = new Inbound(proto, settings)
    inbound.port = json.port ?? inbound.port
    inbound.listen = json.listen ?? ''
    inbound.tag = json.tag || ''
    inbound.sniff = json.sniffing ? SniffBlock.fromJson(json.sniffing) : new SniffBlock()
    return inbound
  }

  toJson(): Json {
    return {
      port: this.port,
      listen: this.listen,
      protocol: this._protocol,
      settings: InboundSettings.toJson(this._protocol, this.settings),
      tag: this.tag,
      sniffing: this.sniff.toJson(),
    }
  }

  /* ============ 分享链接生成 ============ */

  genLink(address = '', remark = ''): string {
    switch (this._protocol) {
      case 'vmess':
        return this.genVmessLink(address, remark)
      case 'vless':
        return this.genVlessLink(address, remark)
      case 'trojan':
        return this.genTrojanLink(address, remark)
      case 'shadowsocks':
        return this.genSsLink(address, remark)
      case 'hysteria2':
        return this.genHysteria2Link(address, remark)
      case 'tuic':
        return this.genTuicLink(address, remark)
      case 'socks':
        return this.genSocksLink(address, remark)
      case 'http':
        return this.genHttpLink(address, remark)
      // anytls / shadowtls / naive / wireguard / mixed / direct 没有标准 URL scheme，
      // canShare 会返回 false，UI 上不显示复制按钮。
      default:
        return ''
    }
  }

  dialAddress(address: string): string {
    return this.tls && this.serverName ? this.serverName : address
  }

  genVmessLink(address = '', remark = ''): string {
    const s = this.settings
    const user = (s.users || [])[0] || {}
    const network = this.transportType || 'tcp'
    const obj = {
      v: '2',
      ps: remark,
      add: this.dialAddress(address),
      port: this.port,
      id: user.uuid || '',
      aid: user.alterId || 0,
      net: network,
      type: 'none',
      host: s.transport?.host?.[0] || '',
      path: s.transport?.path || '',
      tls: this.tls ? 'tls' : '',
    }
    return 'vmess://' + base64(JSON.stringify(obj, null, 2))
  }

  genVlessLink(address = '', remark = ''): string {
    const s = this.settings
    const user = (s.users || [])[0] || {}
    const params = new URLSearchParams()
    params.set('type', this.transportType || 'tcp')
    if (this.tls) {
      const reality = !!s.tls?.reality?.enabled
      params.set('security', reality ? 'reality' : 'tls')
      if (this.serverName) params.set('sni', this.serverName)
      if (s.tls?.alpn?.length) params.set('alpn', s.tls.alpn.join(','))
      if (reality) {
        params.set('pbk', s.tls.reality.public_key || '')
        params.set('sid', (s.tls.reality.short_id || [''])[0] || '')
      }
    } else {
      params.set('security', 'none')
    }
    if (user.flow) params.set('flow', user.flow)
    if (s.transport) {
      if (s.transport.path) params.set('path', s.transport.path)
      if (s.transport.host?.length) params.set('host', s.transport.host.join(','))
      if (s.transport.service_name) params.set('serviceName', s.transport.service_name)
    }
    return `vless://${user.uuid || ''}@${this.dialAddress(address)}:${this.port}?${params.toString()}#${encodeURIComponent(remark)}`
  }

  genTrojanLink(address = '', remark = ''): string {
    const user = (this.settings.users || [])[0] || {}
    const params = new URLSearchParams()
    if (this.serverName) params.set('sni', this.serverName)
    if (this.transportType) params.set('type', this.transportType)
    const query = params.toString()
    return (
      `trojan://${encodeURIComponent(user.password || '')}@${address}:${this.port}` +
      (query ? '?' + query : '') +
      '#' +
      encodeURIComponent(remark)
    )
  }

  genSsLink(address = '', remark = ''): string {
    const s = this.settings
    const raw = `${s.method}:${s.password}`
    // 2022 系列的密钥本身就是 base64，保留标准 padding 以便客户端还原。
    const userInfo = isSS2022(s.method) ? base64(raw) : safeBase64(raw)
    return `ss://${userInfo}@${address}:${this.port}#${encodeURIComponent(remark)}`
  }

  genHysteria2Link(address = '', remark = ''): string {
    const user = (this.settings.users || [])[0] || {}
    const params = new URLSearchParams()
    if (this.serverName) params.set('sni', this.serverName)
    if (this.settings.up_mbps) params.set('up', String(this.settings.up_mbps))
    if (this.settings.down_mbps) params.set('down', String(this.settings.down_mbps))
    const query = params.toString()
    return (
      `hysteria2://${encodeURIComponent(user.password || '')}@${address}:${this.port}` +
      (query ? '?' + query : '') +
      '#' +
      encodeURIComponent(remark)
    )
  }

  genTuicLink(address = '', remark = ''): string {
    const user = (this.settings.users || [])[0] || {}
    const params = new URLSearchParams()
    if (this.settings.congestion_control) params.set('congestion_control', this.settings.congestion_control)
    if (this.serverName) params.set('sni', this.serverName)
    params.set('alpn', 'h3')
    const userInfo = `${user.uuid || ''}:${encodeURIComponent(user.password || '')}`
    return `tuic://${userInfo}@${address}:${this.port}?${params.toString()}#${encodeURIComponent(remark)}`
  }

  genSocksLink(address = '', remark = ''): string {
    return `socks://${this.proxyAuth()}${address}:${this.port}#${encodeURIComponent(remark)}`
  }

  genHttpLink(address = '', remark = ''): string {
    const scheme = this.tls ? 'https' : 'http'
    return `${scheme}://${this.proxyAuth()}${address}:${this.port}#${encodeURIComponent(remark)}`
  }

  /** proxyAuth 构造 socks/http 链接的 `user:pass@` 段；无账号时返回空串。 */
  proxyAuth(): string {
    const user = (this.settings.users || [])[0] || {}
    if (!user.username) {
      return ''
    }
    let auth = encodeURIComponent(user.username)
    if (user.password) {
      auth += ':' + encodeURIComponent(user.password)
    }
    return auth + '@'
  }
}

/* ================= settings 序列化 ================= */

export const InboundSettings = {
  fromJson(json: Json): Json {
    const out = JSON.parse(JSON.stringify(json))
    if (out.tls) out.tls = TlsBlock.fromJson(out.tls)
    if (out.transport) out.transport = TransportBlock.fromJson(out.transport)
    return out
  },

  toJson(protocol: string, settings: Json): Json {
    const out: Json = {}
    for (const k of Object.keys(settings || {})) {
      const v = settings[k]
      if (v === undefined || v === null || v === '') continue
      if (v && typeof v.toJson === 'function') {
        const vv = v.toJson()
        if (vv !== undefined) out[k] = vv
      } else if (Array.isArray(v)) {
        if (v.length) out[k] = v
      } else if (typeof v === 'object') {
        if (Object.keys(v).length) out[k] = v
      } else {
        out[k] = v
      }
    }
    // Hysteria2 masquerade 支持 URL 字符串 | {"type":"file",...} | {"type":"proxy",...}。
    // 表单统一收成字符串，这里试一次 JSON 解析；解析失败就原样交给 sing-box 报错，
    // 而不是悄悄吞掉用户输入。
    if (protocol === 'hysteria2' && typeof out.masquerade === 'string' && out.masquerade) {
      const s = out.masquerade.trim()
      if (s.startsWith('{') && s.endsWith('}')) {
        try {
          out.masquerade = JSON.parse(s)
        } catch {
          out.masquerade = s
        }
      } else {
        out.masquerade = s
      }
    }
    return out
  },
}

/**
 * protocolDefaults 由 protocols.ts 在模块初始化时注册。
 *
 * 这个间接层是为了打破 core.ts ↔ protocols.ts 的循环 import：
 * defaults 工厂要 new TlsBlock()，而 Inbound 要查 defaults。
 */
export const protocolDefaults: Record<string, () => Json> = {}
