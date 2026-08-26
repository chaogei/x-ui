/**
 * forms.ts —— 每种协议的表单描述。
 *
 * 旧前端为 14 种协议各写了一个 HTML 模板，字段增删要同时改模板和 defaults()。
 * 这里改成声明式描述，由 ProtocolForm.vue 统一渲染：字段仍然是手写的（协议
 * 之间差异太大，没法从 UserSchema 自动推导），但只剩一处可改。
 *
 * path 是相对 inbound.settings 的点路径，数组下标直接写数字，例如
 * `users.0.uuid`、`peers.0.allowed_ips`。
 */
import { SSMethods } from './core'

export type Field =
  | { kind: 'text'; path: string; label: string; placeholder?: string }
  | { kind: 'password'; path: string; label: string }
  | { kind: 'number'; path: string; label: string }
  | { kind: 'switch'; path: string; label: string }
  | { kind: 'select'; path: string; label: string; options: { value: string | number; label: string }[]; width?: number }
  /** csv：界面上是逗号分隔的一行文本，模型里是字符串数组。 */
  | { kind: 'csv'; path: string; label: string; placeholder?: string; keepEmpty?: boolean }
  | { kind: 'textarea'; path: string; label: string; rows?: number; placeholder?: string }

export interface ProtocolForm {
  fields: Field[]
  /** 协议是否有 TLS 块（settings.tls）。 */
  tls?: boolean
  /** 协议是否有 transport 块（settings.transport）。 */
  transport?: boolean
  /** 协议是否支持 Reality（目前只有 VLESS）。 */
  reality?: boolean
  /** 分割线之后的第二组字段，例如 wireguard 的 peer。 */
  extraTitle?: string
  extra?: Field[]
}

const NETWORK_OPTIONS = [
  { value: '', label: 'tcp+udp' },
  { value: 'tcp', label: 'tcp' },
  { value: 'udp', label: 'udp' },
]

const ssMethodOptions = Object.values(SSMethods).map((m) => ({ value: m, label: m }))

export const protocolForms: Record<string, ProtocolForm> = {
  vmess: {
    fields: [
      { kind: 'text', path: 'users.0.uuid', label: 'UUID' },
      { kind: 'number', path: 'users.0.alterId', label: 'alterId' },
      { kind: 'text', path: 'users.0.name', label: 'proto_name_opt' },
    ],
    tls: true,
    transport: true,
  },
  vless: {
    fields: [
      { kind: 'text', path: 'users.0.uuid', label: 'UUID' },
      {
        kind: 'select',
        path: 'users.0.flow',
        label: 'flow',
        width: 200,
        options: [
          { value: '', label: 'transport_none' },
          { value: 'xtls-rprx-vision', label: 'xtls-rprx-vision' },
        ],
      },
      { kind: 'text', path: 'users.0.name', label: 'proto_name_opt' },
    ],
    tls: true,
    reality: true,
    transport: true,
  },
  trojan: {
    fields: [
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
      { kind: 'text', path: 'users.0.name', label: 'proto_name_opt' },
    ],
    tls: true,
    transport: true,
  },
  shadowsocks: {
    fields: [
      { kind: 'select', path: 'method', label: 'proto_method', width: 260, options: ssMethodOptions },
      { kind: 'text', path: 'password', label: 'proto_password' },
      { kind: 'select', path: 'network', label: 'proto_network', width: 120, options: NETWORK_OPTIONS },
    ],
  },
  hysteria2: {
    fields: [
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
      { kind: 'number', path: 'up_mbps', label: 'proto_up_mbps' },
      { kind: 'number', path: 'down_mbps', label: 'proto_down_mbps' },
      { kind: 'switch', path: 'ignore_client_bandwidth', label: 'proto_ignore_bw' },
      // sing-box 的 masquerade 有三种合法形态（URL 字符串 / {"type":"file"} /
      // {"type":"proxy"}），这里收原样文本，序列化时再试着解析成对象。
      { kind: 'textarea', path: 'masquerade', label: 'masquerade', rows: 3, placeholder: 'proto_masquerade_ph' },
    ],
    tls: true,
  },
  tuic: {
    fields: [
      { kind: 'text', path: 'users.0.uuid', label: 'UUID' },
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
      {
        kind: 'select',
        path: 'congestion_control',
        label: 'proto_congestion',
        width: 120,
        options: [
          { value: 'cubic', label: 'cubic' },
          { value: 'new_reno', label: 'new_reno' },
          { value: 'bbr', label: 'bbr' },
        ],
      },
      { kind: 'text', path: 'auth_timeout', label: 'auth_timeout', placeholder: 'proto_auth_timeout_ph' },
      { kind: 'switch', path: 'zero_rtt_handshake', label: '0-RTT' },
      { kind: 'text', path: 'heartbeat', label: 'heartbeat', placeholder: 'proto_heartbeat_ph' },
    ],
    tls: true,
  },
  anytls: {
    fields: [
      { kind: 'text', path: 'users.0.name', label: 'proto_username' },
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
      { kind: 'csv', path: 'padding_scheme', label: 'proto_padding', placeholder: 'proto_padding_ph' },
    ],
    tls: true,
  },
  shadowtls: {
    fields: [
      {
        kind: 'select',
        path: 'version',
        label: 'proto_version',
        width: 100,
        options: [
          { value: 1, label: '1' },
          { value: 2, label: '2' },
          { value: 3, label: '3' },
        ],
      },
      { kind: 'text', path: 'users.0.name', label: 'proto_username_v' },
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
      { kind: 'text', path: 'handshake.server', label: 'proto_handshake_server' },
      { kind: 'number', path: 'handshake.server_port', label: 'proto_handshake_port' },
      { kind: 'switch', path: 'strict_mode', label: 'strict_mode' },
    ],
  },
  naive: {
    fields: [
      { kind: 'text', path: 'users.0.username', label: 'proto_username' },
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
    ],
    tls: true,
  },
  wireguard: {
    fields: [
      { kind: 'switch', path: 'system', label: 'proto_system_iface' },
      { kind: 'number', path: 'mtu', label: 'MTU' },
      { kind: 'csv', path: 'address', label: 'proto_local_address', placeholder: 'proto_local_address_ph' },
      { kind: 'text', path: 'private_key', label: 'private_key' },
    ],
    extraTitle: 'proto_peer',
    extra: [
      { kind: 'text', path: 'peers.0.address', label: 'proto_peer_address' },
      { kind: 'number', path: 'peers.0.port', label: 'proto_peer_port' },
      { kind: 'text', path: 'peers.0.public_key', label: 'public_key' },
      { kind: 'text', path: 'peers.0.pre_shared_key', label: 'pre_shared_key' },
      { kind: 'csv', path: 'peers.0.allowed_ips', label: 'proto_allowed_ips' },
      { kind: 'number', path: 'peers.0.persistent_keepalive_interval', label: 'persistent_keepalive' },
    ],
  },
  socks: {
    fields: [
      { kind: 'text', path: 'users.0.username', label: 'proto_username' },
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
    ],
  },
  http: {
    fields: [
      { kind: 'text', path: 'users.0.username', label: 'proto_username' },
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
    ],
    tls: true,
  },
  mixed: {
    fields: [
      { kind: 'text', path: 'users.0.username', label: 'proto_username' },
      { kind: 'text', path: 'users.0.password', label: 'proto_password' },
    ],
  },
  direct: {
    fields: [
      { kind: 'text', path: 'override_address', label: 'override_address' },
      { kind: 'number', path: 'override_port', label: 'override_port' },
      { kind: 'select', path: 'network', label: 'network', width: 120, options: NETWORK_OPTIONS },
    ],
  },
}

/** getPath 读取点路径上的值。 */
export function getPath(root: Record<string, any>, path: string): any {
  return path.split('.').reduce<any>((acc, key) => (acc == null ? acc : acc[key]), root)
}

/** setPath 写入点路径上的值，沿途缺失的节点按下一段是否为数字下标补出来。 */
export function setPath(root: Record<string, any>, path: string, value: unknown): void {
  const parts = path.split('.')
  let node: any = root
  for (let i = 0; i < parts.length - 1; i++) {
    const key = parts[i]
    if (node[key] == null) {
      node[key] = /^\d+$/.test(parts[i + 1]) ? [] : {}
    }
    node = node[key]
  }
  node[parts[parts.length - 1]] = value
}
