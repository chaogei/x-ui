/**
 * core.js - x-ui 前端数据模型层（sing-box 内核）
 *
 * 设计要点：
 *   1. 相比旧 Xray 模型，sing-box 没有独立的 streamSettings/transport 概念，
 *      TLS/transport 均内嵌在每种协议自身字段中，因此这里摒弃 StreamSettings 抽象。
 *   2. 每种协议对应一个 Settings 构造器，产出的 JSON 即为 sing-box inbound 的
 *      协议私有字段集（如 users/tls/masquerade），由 Go 端与顶层字段（type/tag/
 *      listen/listen_port）合并后作为一个 inbound 条目。
 *   3. 对不那么常见的协议，Settings 使用"JSON 片段 + 关键字段"的混合表单，
 *      用户既能填关键入口，又能通过 raw 字段覆盖任意 sing-box 合法字段。
 */

/* ================= 协议 & 常量 ================= */

const Protocols = {
    VMESS: 'vmess',
    VLESS: 'vless',
    TROJAN: 'trojan',
    SHADOWSOCKS: 'shadowsocks',
    HYSTERIA2: 'hysteria2',
    TUIC: 'tuic',
    ANYTLS: 'anytls',
    SHADOWTLS: 'shadowtls',
    NAIVE: 'naive',
    WIREGUARD: 'wireguard',
    SOCKS: 'socks',
    HTTP: 'http',
    MIXED: 'mixed',
    DIRECT: 'direct',
};
Object.freeze(Protocols);

// endpoints 类协议：挂到 sing-box 的 "endpoints" 列表而非 "inbounds"。
const EndpointProtocols = new Set([Protocols.WIREGUARD]);
function isEndpointProtocol(p) { return EndpointProtocols.has(p); }

// 链接生成支持的协议集合。
const SHAREABLE_PROTOCOLS = new Set([
    Protocols.VMESS, Protocols.VLESS, Protocols.TROJAN,
    Protocols.SHADOWSOCKS, Protocols.HYSTERIA2, Protocols.TUIC,
]);

// sing-box shadowsocks 支持的加密方式（含 2022 系列）。
const SSMethods = {
    NONE: 'none',
    AES_256_GCM: 'aes-256-gcm',
    AES_128_GCM: 'aes-128-gcm',
    CHACHA20_POLY1305: 'chacha20-poly1305',
    XCHACHA20_POLY1305: 'xchacha20-poly1305',
    SS2022_BLAKE3_AES_128: '2022-blake3-aes-128-gcm',
    SS2022_BLAKE3_AES_256: '2022-blake3-aes-256-gcm',
    SS2022_BLAKE3_CHACHA20: '2022-blake3-chacha20-poly1305',
};
Object.freeze(SSMethods);

// 判断 SS 加密方法是否为 2022 系列（需要 base64 密钥）。
function isSS2022(method) { return typeof method === 'string' && method.startsWith('2022-'); }

/* ================= 工具基类 ================= */

class CoreCommonClass {
    static toJsonArray(arr) { return arr.map(x => x.toJson()); }
    static fromJson() { return new CoreCommonClass(); }
    toJson() { return this; }
    toString(format=true) {
        return format ? JSON.stringify(this.toJson(), null, 2) : JSON.stringify(this.toJson());
    }
}

/* ================= TLS / Transport 嵌入结构 ================= */

/**
 * sing-box 各协议 inbound 共用的 TLS 字段块。
 * 使用时按需置入到协议 Settings 的 tls 字段。
 */
class TlsBlock extends CoreCommonClass {
    constructor(enabled=false, serverName='', alpn=[],
                certPath='', keyPath='', certContent='', keyContent='', useFile=true,
                reality=null) {
        super();
        this.enabled = enabled;
        this.server_name = serverName;
        this.alpn = alpn;
        this.useFile = useFile;
        this.certificate_path = certPath;
        this.key_path = keyPath;
        this.certificate = certContent;
        this.key = keyContent;
        this.reality = reality; // RealityBlock | null
    }

    static fromJson(json={}) {
        let useFile = !!(json.certificate_path || json.key_path);
        return new TlsBlock(
            !!json.enabled,
            json.server_name || '',
            json.alpn || [],
            json.certificate_path || '',
            json.key_path || '',
            Array.isArray(json.certificate) ? json.certificate.join('\n') : (json.certificate || ''),
            Array.isArray(json.key) ? json.key.join('\n') : (json.key || ''),
            useFile,
            json.reality ? RealityBlock.fromJson(json.reality) : null,
        );
    }

    toJson() {
        if (!this.enabled) return { enabled: false };
        const out = { enabled: true };
        if (this.server_name) out.server_name = this.server_name;
        if (this.alpn && this.alpn.length) out.alpn = this.alpn;
        if (this.useFile) {
            if (this.certificate_path) out.certificate_path = this.certificate_path;
            if (this.key_path) out.key_path = this.key_path;
        } else {
            if (this.certificate) out.certificate = this.certificate.split('\n');
            if (this.key) out.key = this.key.split('\n');
        }
        if (this.reality && this.reality.enabled) out.reality = this.reality.toJson();
        return out;
    }
}

/** Reality 子块（仅 VLESS inbound + TLS 时使用）。 */
class RealityBlock extends CoreCommonClass {
    constructor(enabled=false, handshakeServer='', handshakePort=443,
                privateKey='', shortIds=[''], maxTimeDifference='') {
        super();
        this.enabled = enabled;
        this.handshake_server = handshakeServer;
        this.handshake_port = handshakePort;
        this.private_key = privateKey;
        this.short_id = shortIds;
        this.max_time_difference = maxTimeDifference;
    }
    static fromJson(json={}) {
        return new RealityBlock(
            !!json.enabled,
            (json.handshake && json.handshake.server) || '',
            (json.handshake && json.handshake.server_port) || 443,
            json.private_key || '',
            json.short_id || [''],
            json.max_time_difference || '',
        );
    }
    toJson() {
        const out = {
            enabled: true,
            handshake: { server: this.handshake_server, server_port: Number(this.handshake_port) || 443 },
            private_key: this.private_key,
            short_id: this.short_id && this.short_id.length ? this.short_id : [''],
        };
        if (this.max_time_difference) out.max_time_difference = this.max_time_difference;
        return out;
    }
}

/**
 * sing-box 传输层（transport）字段块。
 * 支持 websocket / http / quic / grpc / httpupgrade 五种。
 */
class TransportBlock extends CoreCommonClass {
    constructor(type='',      // '' | 'ws' | 'http' | 'quic' | 'grpc' | 'httpupgrade'
                path='/',
                host=[],
                serviceName='',
                headers={}) {
        super();
        this.type = type;
        this.path = path;
        this.host = host;
        this.service_name = serviceName;
        this.headers = headers;
    }
    static fromJson(json) {
        if (!json || !json.type) return new TransportBlock('');
        return new TransportBlock(
            json.type,
            json.path || '/',
            json.host || [],
            json.service_name || '',
            json.headers || {},
        );
    }
    toJson() {
        if (!this.type) return undefined;
        const out = { type: this.type };
        switch (this.type) {
            case 'ws':
            case 'httpupgrade':
                out.path = this.path || '/';
                if (this.headers && Object.keys(this.headers).length) out.headers = this.headers;
                break;
            case 'http':
                if (this.path) out.path = this.path;
                if (this.host && this.host.length) out.host = this.host;
                if (this.headers && Object.keys(this.headers).length) out.headers = this.headers;
                break;
            case 'grpc':
                out.service_name = this.service_name || '';
                break;
            case 'quic':
                // QUIC 透传 type 即可
                break;
        }
        return out;
    }
}

/**
 * sing-box inbound 顶层 sniff 系列字段块（vmess/vless/trojan/ss/tuic/hy2 等 TCP 类协议通用）。
 */
class SniffBlock extends CoreCommonClass {
    constructor(sniff=true, sniffOverrideDestination=true,
                sniffTimeout='', domainStrategy='') {
        super();
        this.sniff = sniff;
        this.sniff_override_destination = sniffOverrideDestination;
        this.sniff_timeout = sniffTimeout;
        this.domain_strategy = domainStrategy;
    }
    static fromJson(json={}) {
        return new SniffBlock(
            json.sniff !== false,
            json.sniff_override_destination !== false,
            json.sniff_timeout || '',
            json.domain_strategy || '',
        );
    }
    toJson() {
        const out = {
            sniff: !!this.sniff,
            sniff_override_destination: !!this.sniff_override_destination,
        };
        if (this.sniff_timeout) out.sniff_timeout = this.sniff_timeout;
        if (this.domain_strategy) out.domain_strategy = this.domain_strategy;
        return out;
    }
}

/* ================= 每种协议的 Settings 构造器 ================= */

class Inbound extends CoreCommonClass {
    /**
     * Inbound 是前端侧的统一入站 viewmodel。
     * 字段约定：
     *   - settings 为协议私有字段的 JS 对象（调用方可以直接 v-model 绑定）
     *   - 序列化后的 settings 与 sing-box inbound 顶层字段（除 type/tag/listen/
     *     listen_port 外）完全对应
     *   - sniff 为 SniffBlock，提交时合并到 dbInbound.sniffing 字段
     */
    constructor(port=RandomUtil.randomIntRange(10000, 60000),
                listen='',
                protocol=Protocols.VMESS,
                settings=null,
                tag='',
                sniff=new SniffBlock()) {
        super();
        this.port = port;
        this.listen = listen;
        this._protocol = protocol;
        this.settings = settings || Inbound.defaultSettings(protocol);
        this.tag = tag;
        this.sniff = sniff;
    }

    get protocol() { return this._protocol; }
    set protocol(p) {
        this._protocol = p;
        this.settings = Inbound.defaultSettings(p);
    }

    get isEndpoint() { return isEndpointProtocol(this._protocol); }
    get canShare() { return SHAREABLE_PROTOCOLS.has(this._protocol); }
    get canSniff() {
        switch (this._protocol) {
            case Protocols.VMESS: case Protocols.VLESS: case Protocols.TROJAN:
            case Protocols.SHADOWSOCKS: case Protocols.HYSTERIA2: case Protocols.TUIC:
            case Protocols.ANYTLS: case Protocols.SHADOWTLS: case Protocols.NAIVE:
            case Protocols.SOCKS: case Protocols.HTTP: case Protocols.MIXED:
                return true;
            default:
                return false;
        }
    }

    // —— 面板/链接生成常用读取器 —— //
    get uuid() {
        if (this._protocol === Protocols.VMESS || this._protocol === Protocols.VLESS) {
            const u = (this.settings.users || [])[0];
            return u ? (u.uuid || '') : '';
        }
        if (this._protocol === Protocols.TUIC) {
            const u = (this.settings.users || [])[0];
            return u ? (u.uuid || '') : '';
        }
        return '';
    }
    get password() {
        switch (this._protocol) {
            case Protocols.TROJAN: case Protocols.ANYTLS:
            case Protocols.SHADOWTLS:
            case Protocols.NAIVE: {
                const u = (this.settings.users || [])[0];
                return u ? (u.password || '') : '';
            }
            case Protocols.SHADOWSOCKS:
                return this.settings.password || '';
            case Protocols.HYSTERIA2: {
                const u = (this.settings.users || [])[0];
                return u ? (u.password || '') : '';
            }
            case Protocols.TUIC: {
                const u = (this.settings.users || [])[0];
                return u ? (u.password || '') : '';
            }
            case Protocols.SOCKS: case Protocols.HTTP: case Protocols.MIXED:
            case Protocols.NAIVE: {
                const u = (this.settings.users || [])[0];
                return u ? (u.password || '') : '';
            }
            default: return '';
        }
    }
    get username() {
        switch (this._protocol) {
            case Protocols.SOCKS: case Protocols.HTTP: case Protocols.MIXED:
            case Protocols.NAIVE: case Protocols.ANYTLS: case Protocols.SHADOWTLS: {
                const u = (this.settings.users || [])[0];
                return u ? (u.username || u.name || '') : '';
            }
            default: return '';
        }
    }
    get method() { return this._protocol === Protocols.SHADOWSOCKS ? (this.settings.method || '') : ''; }

    get tls() { return !!(this.settings && this.settings.tls && this.settings.tls.enabled); }
    get serverName() {
        return (this.settings && this.settings.tls && this.settings.tls.server_name) || '';
    }
    get transportType() {
        return (this.settings && this.settings.transport && this.settings.transport.type) || '';
    }

    // —— 默认 Settings —— //
    static defaultSettings(protocol) {
        switch (protocol) {
            case Protocols.VMESS: return InboundSettings.vmess();
            case Protocols.VLESS: return InboundSettings.vless();
            case Protocols.TROJAN: return InboundSettings.trojan();
            case Protocols.SHADOWSOCKS: return InboundSettings.shadowsocks();
            case Protocols.HYSTERIA2: return InboundSettings.hysteria2();
            case Protocols.TUIC: return InboundSettings.tuic();
            case Protocols.ANYTLS: return InboundSettings.anytls();
            case Protocols.SHADOWTLS: return InboundSettings.shadowtls();
            case Protocols.NAIVE: return InboundSettings.naive();
            case Protocols.WIREGUARD: return InboundSettings.wireguard();
            case Protocols.SOCKS: return InboundSettings.socksLike(Protocols.SOCKS);
            case Protocols.HTTP: return InboundSettings.socksLike(Protocols.HTTP);
            case Protocols.MIXED: return InboundSettings.socksLike(Protocols.MIXED);
            case Protocols.DIRECT: return InboundSettings.direct();
            default: return {};
        }
    }

    static fromJson(json={}) {
        const proto = json.protocol || Protocols.VMESS;
        const settings = json.settings ? InboundSettings.fromJson(proto, json.settings) : Inbound.defaultSettings(proto);
        return new Inbound(
            json.port,
            json.listen,
            proto,
            settings,
            json.tag || '',
            json.sniffing ? SniffBlock.fromJson(json.sniffing) : new SniffBlock(),
        );
    }

    toJson() {
        return {
            port: this.port,
            listen: this.listen,
            protocol: this._protocol,
            settings: InboundSettings.toJson(this._protocol, this.settings),
            tag: this.tag,
            sniffing: this.sniff.toJson(),
        };
    }

    /* ============ 订阅链接生成 ============ */

    genLink(address='', remark='') {
        switch (this._protocol) {
            case Protocols.VMESS:       return this.genVmessLink(address, remark);
            case Protocols.VLESS:       return this.genVlessLink(address, remark);
            case Protocols.TROJAN:      return this.genTrojanLink(address, remark);
            case Protocols.SHADOWSOCKS: return this.genSsLink(address, remark);
            case Protocols.HYSTERIA2:   return this.genHysteria2Link(address, remark);
            case Protocols.TUIC:        return this.genTuicLink(address, remark);
            case Protocols.SOCKS:       return this.genSocksLink(address, remark);
            case Protocols.HTTP:        return this.genHttpLink(address, remark);
            // anytls / shadowtls / naive / wireguard / mixed / direct 无标准 URL scheme，
            // 由 DBInbound.hasLink 返回 false 隐藏复制按钮。
            default: return '';
        }
    }

    genVmessLink(address='', remark='') {
        const s = this.settings;
        const user = (s.users || [])[0] || {};
        const network = this.transportType || 'tcp';
        const host = (s.transport && s.transport.host && s.transport.host[0]) || '';
        const path = (s.transport && s.transport.path) || '';
        let addr = address;
        if (this.tls && this.serverName) addr = this.serverName;
        const obj = {
            v: '2',
            ps: remark,
            add: addr,
            port: this.port,
            id: user.uuid || '',
            aid: user.alterId || 0,
            net: network === 'tcp' ? 'tcp' : network,
            type: 'none',
            host: host,
            path: path,
            tls: this.tls ? 'tls' : '',
        };
        return 'vmess://' + base64(JSON.stringify(obj, null, 2));
    }

    genVlessLink(address='', remark='') {
        const s = this.settings;
        const user = (s.users || [])[0] || {};
        const uuid = user.uuid || '';
        const network = this.transportType || 'tcp';
        const params = new URLSearchParams();
        params.set('type', network);
        if (this.tls) {
            const reality = s.tls && s.tls.reality && s.tls.reality.enabled;
            params.set('security', reality ? 'reality' : 'tls');
            if (this.serverName) params.set('sni', this.serverName);
            if (s.tls && s.tls.alpn && s.tls.alpn.length) params.set('alpn', s.tls.alpn.join(','));
            if (reality) {
                params.set('pbk', s.tls.reality.public_key || '');
                params.set('sid', (s.tls.reality.short_id || [''])[0] || '');
            }
        } else {
            params.set('security', 'none');
        }
        if (user.flow) params.set('flow', user.flow);
        if (s.transport) {
            if (s.transport.path) params.set('path', s.transport.path);
            if (s.transport.host && s.transport.host.length) params.set('host', s.transport.host.join(','));
            if (s.transport.service_name) params.set('serviceName', s.transport.service_name);
        }
        let addr = address;
        if (this.tls && this.serverName) addr = this.serverName;
        return `vless://${uuid}@${addr}:${this.port}?${params.toString()}#${encodeURIComponent(remark)}`;
    }

    genTrojanLink(address='', remark='') {
        const user = (this.settings.users || [])[0] || {};
        const password = user.password || '';
        const params = new URLSearchParams();
        if (this.serverName) params.set('sni', this.serverName);
        const type = this.transportType;
        if (type) params.set('type', type);
        return `trojan://${encodeURIComponent(password)}@${address}:${this.port}` +
               (params.toString() ? ('?' + params.toString()) : '') +
               '#' + encodeURIComponent(remark);
    }

    genSsLink(address='', remark='') {
        const s = this.settings;
        let userInfo;
        if (isSS2022(s.method)) {
            // 2022 系列：userinfo 保留为 method:password 的 base64，兼容 Shadowsocks 2022 URL。
            userInfo = base64(`${s.method}:${s.password}`);
        } else {
            userInfo = safeBase64(`${s.method}:${s.password}`);
        }
        return `ss://${userInfo}@${address}:${this.port}#${encodeURIComponent(remark)}`;
    }

    genHysteria2Link(address='', remark='') {
        const user = (this.settings.users || [])[0] || {};
        const params = new URLSearchParams();
        if (this.serverName) params.set('sni', this.serverName);
        if (this.settings.up_mbps) params.set('up', String(this.settings.up_mbps));
        if (this.settings.down_mbps) params.set('down', String(this.settings.down_mbps));
        return `hysteria2://${encodeURIComponent(user.password || '')}@${address}:${this.port}` +
               (params.toString() ? ('?' + params.toString()) : '') +
               '#' + encodeURIComponent(remark);
    }

    genTuicLink(address='', remark='') {
        const user = (this.settings.users || [])[0] || {};
        const params = new URLSearchParams();
        if (this.settings.congestion_control) params.set('congestion_control', this.settings.congestion_control);
        if (this.serverName) params.set('sni', this.serverName);
        params.set('alpn', 'h3');
        const userInfo = `${user.uuid || ''}:${encodeURIComponent(user.password || '')}`;
        return `tuic://${userInfo}@${address}:${this.port}?${params.toString()}#${encodeURIComponent(remark)}`;
    }

    // SOCKS5 代理 URL（事实标准，多数客户端识别）：socks://user:pass@host:port#name
    // 无账号时退化为 socks://host:port#name。
    genSocksLink(address='', remark='') {
        const user = (this.settings.users || [])[0] || {};
        let auth = '';
        if (user.username) {
            auth = encodeURIComponent(user.username);
            if (user.password) auth += ':' + encodeURIComponent(user.password);
            auth += '@';
        }
        return `socks://${auth}${address}:${this.port}#${encodeURIComponent(remark)}`;
    }

    // HTTP(S) 代理 URL 惯例：http://user:pass@host:port#name。
    // x-ui 默认走 http（若用户在 settings.tls 里启用，才用 https）。
    genHttpLink(address='', remark='') {
        const user = (this.settings.users || [])[0] || {};
        let auth = '';
        if (user.username) {
            auth = encodeURIComponent(user.username);
            if (user.password) auth += ':' + encodeURIComponent(user.password);
            auth += '@';
        }
        const scheme = this.tls ? 'https' : 'http';
        return `${scheme}://${auth}${address}:${this.port}#${encodeURIComponent(remark)}`;
    }
}

/* ================= 协议私有 settings 默认值 & 序列化 ================= */

const InboundSettings = {
    vmess() {
        return {
            users: [{ name: '', uuid: RandomUtil.randomUUID(), alterId: 0 }],
            tls: new TlsBlock(),
            transport: new TransportBlock(),
        };
    },
    vless() {
        return {
            users: [{ name: '', uuid: RandomUtil.randomUUID(), flow: '' }],
            tls: new TlsBlock(),
            transport: new TransportBlock(),
        };
    },
    trojan() {
        return {
            users: [{ name: '', password: RandomUtil.randomSeq(16) }],
            tls: new TlsBlock(true),
            transport: new TransportBlock(),
        };
    },
    shadowsocks() {
        return {
            method: SSMethods.AES_256_GCM,
            password: RandomUtil.randomSeq(16),
            network: '',
        };
    },
    hysteria2() {
        return {
            up_mbps: 100,
            down_mbps: 100,
            users: [{ name: '', password: RandomUtil.randomSeq(16) }],
            masquerade: '',
            ignore_client_bandwidth: false,
            tls: new TlsBlock(true),
        };
    },
    tuic() {
        return {
            users: [{ name: '', uuid: RandomUtil.randomUUID(), password: RandomUtil.randomSeq(16) }],
            congestion_control: 'bbr',
            auth_timeout: '3s',
            zero_rtt_handshake: false,
            heartbeat: '10s',
            tls: new TlsBlock(true),
        };
    },
    anytls() {
        return {
            users: [{ name: '', password: RandomUtil.randomSeq(16) }],
            padding_scheme: [],
            tls: new TlsBlock(true),
        };
    },
    shadowtls() {
        return {
            version: 3,
            users: [{ name: '', password: RandomUtil.randomSeq(16) }],
            handshake: { server: 'www.microsoft.com', server_port: 443 },
            strict_mode: false,
        };
    },
    naive() {
        return {
            users: [{ username: '', password: RandomUtil.randomSeq(16) }],
            tls: new TlsBlock(true),
        };
    },
    wireguard() {
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
    socksLike(proto) {
        const base = {
            users: [{ username: '', password: RandomUtil.randomSeq(10) }],
        };
        if (proto === Protocols.SOCKS) base.users[0].username = RandomUtil.randomSeq(6);
        return base;
    },
    direct() {
        return {
            override_address: '',
            override_port: 0,
            network: '',
        };
    },

    fromJson(protocol, json) {
        // 为简化，直接返回 JSON；TLS/Transport 若存在则用其类封装。
        const out = JSON.parse(JSON.stringify(json));
        if (out.tls) out.tls = TlsBlock.fromJson(out.tls);
        if (out.transport) out.transport = TransportBlock.fromJson(out.transport);
        return out;
    },

    toJson(protocol, settings) {
        const out = {};
        for (const k of Object.keys(settings || {})) {
            const v = settings[k];
            if (v === undefined || v === null || v === '') continue;
            if (v && typeof v.toJson === 'function') {
                const vv = v.toJson();
                if (vv !== undefined) out[k] = vv;
            } else if (Array.isArray(v)) {
                if (v.length) out[k] = v;
            } else if (typeof v === 'object') {
                if (Object.keys(v).length) out[k] = v;
            } else {
                out[k] = v;
            }
        }
        // Hysteria2 masquerade 支持 URL 字符串 | {"type":"file",...} | {"type":"proxy",...} 三种形态。
        // 前端 textarea 统一收集为字符串，这里做一次 JSON 解析：若解析成对象则替换为对象，
        // 否则保留为 URL 字符串（sing-box 两种形态都接受）。
        if (protocol === Protocols.HYSTERIA2 && typeof out.masquerade === 'string' && out.masquerade) {
            const s = out.masquerade.trim();
            if (s.startsWith('{') && s.endsWith('}')) {
                try {
                    out.masquerade = JSON.parse(s);
                } catch (_) {
                    // 解析失败保留原串交给 sing-box 报错，避免悄悄吞掉用户输入。
                    out.masquerade = s;
                }
            } else {
                out.masquerade = s;
            }
        }
        return out;
    },
};
