/**
 * protocol_spec.js - x-ui sing-box 协议元数据前端入口
 *
 * 设计要点：
 *   1. 后端 core/singbox/spec 通过 head.html 的 <script> 注入 window.__PROTOCOL_SPECS__，
 *      此文件直接消费，没有异步 fetch，不存在时序问题。
 *   2. 后端提供静态元数据（key / network / is_endpoint / shareable / users），
 *      前端补齐"带随机/依赖前端类型"的部分（defaults() 工厂方法）。
 *   3. URL 分享链接生成目前仍保留在 core.js 的 Inbound 类上，以避免一次性改动面过大；
 *      后续可迁移，本文件先聚焦元数据 + defaults。
 *   4. 任何消费者（core.js / models.js / 页面 Vue 组件）通过 ProtocolSpecs /
 *      getProtocolSpec / isEndpointProtocol / isShareableProtocol 访问，
 *      禁止再在业务层硬编码协议分类。
 */

/* ========== 后端元数据校验 ========== */

// _backendSpecs 为后端 core/singbox/spec.All() 的副本，顺序与注册表一致。
// 以快速失败策略校验：缺失即抛异常，避免页面静默进入不一致状态。
const _backendSpecs = (function () {
    const raw = window.__PROTOCOL_SPECS__;
    if (!Array.isArray(raw) || raw.length === 0) {
        throw new Error('protocol_spec: window.__PROTOCOL_SPECS__ 缺失或格式错误，请确认 head.html 已注入');
    }
    return raw;
})();

/* ========== 前端 defaults 补丁 ========== */

// _frontendPatch 为每种协议提供 "defaults()" 工厂。
// 约束：defaults() 必须返回全新对象，避免不同 inbound 共享可变引用。
// 函数体内允许延迟引用 core.js 的 TlsBlock / TransportBlock / RealityBlock —— 这些符号
// 在首次 defaults() 被调用时（用户点击"新建入站"）已经就绪。
const _frontendPatch = {
    vmess: {
        defaults() {
            return {
                users: [{ name: '', uuid: RandomUtil.randomUUID(), alterId: 0 }],
                tls: new TlsBlock(),
                transport: new TransportBlock(),
            };
        },
    },
    vless: {
        defaults() {
            return {
                users: [{ name: '', uuid: RandomUtil.randomUUID(), flow: '' }],
                tls: new TlsBlock(),
                transport: new TransportBlock(),
            };
        },
    },
    trojan: {
        defaults() {
            return {
                users: [{ name: '', password: RandomUtil.randomSeq(16) }],
                tls: new TlsBlock(true),
                transport: new TransportBlock(),
            };
        },
    },
    shadowsocks: {
        defaults() {
            return {
                method: SSMethods.AES_256_GCM,
                password: RandomUtil.randomSeq(16),
                network: '',
            };
        },
    },
    hysteria2: {
        defaults() {
            return {
                up_mbps: 100,
                down_mbps: 100,
                users: [{ name: '', password: RandomUtil.randomSeq(16) }],
                masquerade: '',
                ignore_client_bandwidth: false,
                tls: new TlsBlock(true),
            };
        },
    },
    tuic: {
        defaults() {
            return {
                users: [{ name: '', uuid: RandomUtil.randomUUID(), password: RandomUtil.randomSeq(16) }],
                congestion_control: 'bbr',
                auth_timeout: '3s',
                zero_rtt_handshake: false,
                heartbeat: '10s',
                tls: new TlsBlock(true),
            };
        },
    },
    anytls: {
        defaults() {
            return {
                users: [{ name: '', password: RandomUtil.randomSeq(16) }],
                padding_scheme: [],
                tls: new TlsBlock(true),
            };
        },
    },
    shadowtls: {
        defaults() {
            return {
                version: 3,
                users: [{ name: '', password: RandomUtil.randomSeq(16) }],
                handshake: { server: 'www.microsoft.com', server_port: 443 },
                strict_mode: false,
            };
        },
    },
    naive: {
        defaults() {
            return {
                users: [{ username: '', password: RandomUtil.randomSeq(16) }],
                tls: new TlsBlock(true),
            };
        },
    },
    wireguard: {
        defaults() {
            return {
                system: false,
                mtu: 1420,
                address: ['10.0.0.1/32'],
                private_key: '',
                peers: [{
                    address: '',
                    port: 51820,
                    public_key: '',
                    pre_shared_key: '',
                    allowed_ips: ['0.0.0.0/0'],
                    persistent_keepalive_interval: 0,
                }],
            };
        },
    },
    // socks / http / mixed 共享 "users[].{username,password}" 默认结构；
    // 仅 socks 预置一个随机 username，保持原 core.js 行为。
    socks: {
        defaults() {
            return {
                users: [{ username: RandomUtil.randomSeq(6), password: RandomUtil.randomSeq(10) }],
            };
        },
    },
    http: {
        defaults() {
            return {
                users: [{ username: '', password: RandomUtil.randomSeq(10) }],
            };
        },
    },
    mixed: {
        defaults() {
            return {
                users: [{ username: '', password: RandomUtil.randomSeq(10) }],
            };
        },
    },
    direct: {
        defaults() {
            return {
                override_address: '',
                override_port: 0,
                network: '',
            };
        },
    },
};

/* ========== 合并产出 ProtocolSpecs ========== */

// ProtocolSpecs 是 sing-box 协议元数据的前端权威映射。
// 形态：{ [key]: { key, network, is_endpoint, shareable, users, defaults() } }
// 本对象顶层冻结，但各协议条目保留 defaults 方法可调用性。
const ProtocolSpecs = (function () {
    const merged = {};
    _backendSpecs.forEach(meta => {
        const patch = _frontendPatch[meta.key];
        if (!patch || typeof patch.defaults !== 'function') {
            // 后端注册了新协议但前端未提供 defaults：抛错而非悄悄使用空对象，
            // 避免用户在 UI 上创建出无字段的"伪入站"。
            throw new Error(
                `protocol_spec: 协议 ${meta.key} 缺少前端 defaults 实现，` +
                `请在 _frontendPatch 中补齐`
            );
        }
        merged[meta.key] = Object.assign({}, meta, { defaults: patch.defaults });
    });
    return Object.freeze(merged);
})();

/* ========== 查询 helpers ========== */

/**
 * 按 key 查询协议 spec；未知协议返回 null。
 * 调用方应在业务层对 null 做优雅降级，不应 panic。
 */
function getProtocolSpec(key) {
    return ProtocolSpecs[key] || null;
}

/** 协议是否挂在 sing-box endpoints 列表（sing-box 1.11+ 约定）。 */
function isEndpointProtocol(key) {
    const s = ProtocolSpecs[key];
    return !!(s && s.is_endpoint);
}

/** 协议是否支持标准分享 URL（用于 UI 复制/二维码按钮显示）。 */
function isShareableProtocol(key) {
    const s = ProtocolSpecs[key];
    return !!(s && s.shareable);
}

/**
 * 协议是否支持 sing-box sniff 流水线配置。
 * endpoint 类（wireguard）与透明转发（direct）返回 false，
 * 其余 TCP/UDP 代理协议返回 true。
 */
function isSniffableProtocol(key) {
    const s = ProtocolSpecs[key];
    return !!(s && s.sniffable);
}

/** 协议传输层网络类型（"tcp" / "udp" / "both"）；未知协议返回 ""。 */
function getProtocolNetwork(key) {
    const s = ProtocolSpecs[key];
    return s ? s.network : '';
}

/** 所有协议 key 的有序列表，顺序与后端注册表 order 保持一致。 */
function allProtocolKeys() {
    return _backendSpecs.map(s => s.key);
}
