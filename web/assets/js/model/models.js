class User {

    constructor() {
        this.username = "";
        this.password = "";
    }
}

class Msg {

    constructor(success, msg, obj) {
        this.success = false;
        this.msg = "";
        this.obj = null;

        if (success != null) {
            this.success = success;
        }
        if (msg != null) {
            this.msg = msg;
        }
        if (obj != null) {
            this.obj = obj;
        }
    }
}

class DBInbound {

    constructor(data) {
        this.id = 0;
        this.userId = 0;
        this.up = 0;
        this.down = 0;
        this.total = 0;
        this.remark = "";
        this.enable = true;
        this.expiryTime = 0;

        this.listen = "";
        this.port = 0;
        this.protocol = "";
        this.settings = "";
        this.tag = "";
        this.sniffing = "";

        if (data == null) {
            return;
        }
        ObjectUtil.cloneProps(this, data);
    }

    get totalGB() {
        return toFixed(this.total / ONE_GB, 2);
    }

    set totalGB(gb) {
        this.total = toFixed(gb * ONE_GB, 0);
    }

    get isVMess()     { return this.protocol === Protocols.VMESS; }
    get isVLess()     { return this.protocol === Protocols.VLESS; }
    get isTrojan()    { return this.protocol === Protocols.TROJAN; }
    get isSS()        { return this.protocol === Protocols.SHADOWSOCKS; }
    get isHysteria2() { return this.protocol === Protocols.HYSTERIA2; }
    get isTUIC()      { return this.protocol === Protocols.TUIC; }
    get isAnyTLS()    { return this.protocol === Protocols.ANYTLS; }
    get isShadowTLS() { return this.protocol === Protocols.SHADOWTLS; }
    get isNaive()     { return this.protocol === Protocols.NAIVE; }
    get isWG()        { return this.protocol === Protocols.WIREGUARD; }
    get isSocks()     { return this.protocol === Protocols.SOCKS; }
    get isHTTP()      { return this.protocol === Protocols.HTTP; }
    get isMixed()     { return this.protocol === Protocols.MIXED; }
    get isDirect()    { return this.protocol === Protocols.DIRECT; }

    get address() {
        let address = location.hostname;
        if (!ObjectUtil.isEmpty(this.listen) && this.listen !== "0.0.0.0") {
            address = this.listen;
        }
        return address;
    }

    get _expiryTime() {
        if (this.expiryTime === 0) {
            return null;
        }
        return moment(this.expiryTime);
    }

    set _expiryTime(t) {
        if (t == null) {
            this.expiryTime = 0;
        } else {
            this.expiryTime = t.valueOf();
        }
    }

    get isExpiry() {
        return this.expiryTime < new Date().getTime();
    }

    toInbound() {
        let settings = {};
        if (!ObjectUtil.isEmpty(this.settings)) {
            settings = JSON.parse(this.settings);
        }

        let sniffing = {};
        if (!ObjectUtil.isEmpty(this.sniffing)) {
            sniffing = JSON.parse(this.sniffing);
        }
        const config = {
            port: this.port,
            listen: this.listen,
            protocol: this.protocol,
            settings: settings,
            tag: this.tag,
            sniffing: sniffing,
        };
        return Inbound.fromJson(config);
    }

    // 委托 protocol_spec.js，分享链接支持性的单一来源在后端 core/singbox/spec。
    // 新增协议只需更新后端 Shareable 字段即可生效。
    hasLink() {
        return isShareableProtocol(this.protocol);
    }

    genLink() {
        const inbound = this.toInbound();
        return inbound.genLink(this.address, this.remark);
    }
}

class AllSetting {

    constructor(data) {
        this.webListen = "";
        this.webPort = 54321;
        this.webCertFile = "";
        this.webKeyFile = "";
        this.webBasePath = "/";
        this.tgBotEnable = false;
        this.tgBotToken = "";
        this.tgBotChatId = 0;
        this.tgRunTime = "";
        this.coreTemplateConfig = "";

        this.timeLocation = "Asia/Shanghai";

        if (data == null) {
            return
        }
        ObjectUtil.cloneProps(this, data);
    }

    equals(other) {
        return ObjectUtil.equals(this, other);
    }
}