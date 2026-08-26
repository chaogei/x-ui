/**
 * protocols.ts —— sing-box 协议元数据的前端入口。
 *
 * 设计要点：
 *   1. 协议列表的单一来源是后端 core/singbox/spec，经 head.html 注入到
 *      window.__XUI__.protocols（boot.protocols）。这里只消费，不硬编码。
 *   2. 后端给静态元数据（key / network / is_endpoint / shareable / users），
 *      前端补"带随机值、依赖前端类型"的部分 —— 即每种协议的 defaults() 工厂。
 *   3. core/singbox/spec/frontend_parity_test.go 会解析本文件的 _frontendPatch，
 *      逐协议核对 defaults() 是否覆盖了 UserSchema 声明的凭证字段。改动结构前
 *      先看那个测试。
 */
import { boot } from '../boot'
import { randomSeq, randomUUID } from '../random'
import { protocolDefaults, ProtocolSpecs, SSMethods, TlsBlock, TransportBlock } from './core'

/**
 * _frontendPatch 为每种协议提供 defaults() 工厂。
 *
 * 约束：defaults() 必须返回全新对象，避免不同 inbound 共享可变引用。
 */
const _frontendPatch = {
  vmess: {
    defaults() {
      return {
        users: [{ name: '', uuid: randomUUID(), alterId: 0 }],
        tls: new TlsBlock(),
        transport: new TransportBlock(),
      }
    },
  },
  vless: {
    defaults() {
      return {
        users: [{ name: '', uuid: randomUUID(), flow: '' }],
        tls: new TlsBlock(),
        transport: new TransportBlock(),
      }
    },
  },
  trojan: {
    defaults() {
      return {
        users: [{ name: '', password: randomSeq(16) }],
        tls: new TlsBlock(true),
        transport: new TransportBlock(),
      }
    },
  },
  shadowsocks: {
    defaults() {
      return {
        method: SSMethods.AES_256_GCM,
        password: randomSeq(16),
        network: '',
      }
    },
  },
  hysteria2: {
    defaults() {
      return {
        up_mbps: 100,
        down_mbps: 100,
        users: [{ name: '', password: randomSeq(16) }],
        masquerade: '',
        ignore_client_bandwidth: false,
        tls: new TlsBlock(true),
      }
    },
  },
  tuic: {
    defaults() {
      return {
        users: [{ name: '', uuid: randomUUID(), password: randomSeq(16) }],
        congestion_control: 'bbr',
        auth_timeout: '3s',
        zero_rtt_handshake: false,
        heartbeat: '10s',
        tls: new TlsBlock(true),
      }
    },
  },
  anytls: {
    defaults() {
      return {
        users: [{ name: '', password: randomSeq(16) }],
        padding_scheme: [],
        tls: new TlsBlock(true),
      }
    },
  },
  shadowtls: {
    defaults() {
      return {
        version: 3,
        users: [{ name: '', password: randomSeq(16) }],
        handshake: { server: 'www.microsoft.com', server_port: 443 },
        strict_mode: false,
      }
    },
  },
  naive: {
    defaults() {
      return {
        users: [{ username: '', password: randomSeq(16) }],
        tls: new TlsBlock(true),
      }
    },
  },
  wireguard: {
    defaults() {
      return {
        system: false,
        mtu: 1420,
        address: ['10.0.0.1/32'],
        private_key: '',
        peers: [
          {
            address: '',
            port: 51820,
            public_key: '',
            pre_shared_key: '',
            allowed_ips: ['0.0.0.0/0'],
            persistent_keepalive_interval: 0,
          },
        ],
      }
    },
  },
  socks: {
    defaults() {
      return {
        users: [{ username: randomSeq(6), password: randomSeq(10) }],
      }
    },
  },
  http: {
    defaults() {
      return {
        users: [{ username: '', password: randomSeq(10) }],
      }
    },
  },
  mixed: {
    defaults() {
      return {
        users: [{ username: '', password: randomSeq(10) }],
      }
    },
  },
  direct: {
    defaults() {
      return {
        override_address: '',
        override_port: 0,
        network: '',
      }
    },
  },
}

/**
 * 把 defaults 注册回 core.ts。
 *
 * 后端注册了协议但前端没有 defaults 时直接抛错，而不是悄悄用空对象：
 * 后者会让用户在 UI 上建出一个没有任何字段的"伪入站"，保存成功但连不上。
 */
for (const spec of boot.protocols) {
  const patch = (_frontendPatch as Record<string, { defaults(): Record<string, unknown> }>)[spec.key]
  if (!patch || typeof patch.defaults !== 'function') {
    throw new Error(`protocols: 协议 ${spec.key} 缺少前端 defaults 实现，请在 _frontendPatch 中补齐`)
  }
  protocolDefaults[spec.key] = patch.defaults
}

export { ProtocolSpecs }
