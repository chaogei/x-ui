# x-ui

支持多协议多用户的 **sing-box** 面板

> 自 v1.0.0 起，x-ui 已将底层代理内核从 Xray 切换为 [sing-box](https://github.com/SagerNet/sing-box)。
> 旧版 Xray schema 的 inbound 数据会在首次启动时自动重命名为 `inbounds_xray_backup_{时间戳}` 表保留备份，
> 新协议下的入站请在面板中重新创建。

# 功能介绍

- 系统状态监控
- 多用户、多协议面板可视化操作
- **支持 14 种 sing-box inbound/endpoint 协议**：
  vmess、vless (含 Reality)、trojan、shadowsocks (含 2022 系列)、
  hysteria2、tuic、anytls、shadowtls、naive、wireguard、
  socks、http、mixed、direct
- 流量统计 / 限制流量 / 限制到期时间（通过 sing-box `experimental.v2ray_api`）
- 可自定义 sing-box 配置模板
- 支持 https 访问面板（自备域名 + SSL 证书）
- 支持一键 SSL 证书申请并自动续签
- **多语言面板**：简体中文 / 繁体中文 / English 全量覆盖（跟随浏览器语言，cookie `lang` 可强制切换）
- 更多高级配置，详见面板

# 安装 & 升级

```bash
bash <(curl -Ls https://raw.githubusercontent.com/chaogei/x-ui/master/install.sh)
```

脚本会自动从 [SagerNet/sing-box releases](https://github.com/SagerNet/sing-box/releases/latest)
拉取与服务器架构对应的最新 sing-box 二进制，放置到 `/usr/local/x-ui/bin/sing-box-linux-{arch}`。

## 手动安装 & 升级

1. 从 https://github.com/chaogei/x-ui/releases 下载最新压缩包，一般选择 `amd64`。
2. 将压缩包上传到服务器 `/root/`，以 `root` 用户登录。

> 架构非 `amd64` 请自行将命令中的 `amd64` 替换为目标架构。

```bash
cd /root/
rm x-ui/ /usr/local/x-ui/ /usr/bin/x-ui -rf
tar zxvf x-ui-linux-amd64.tar.gz

# 将 sing-box 二进制放入 bin/ 目录（以 v1.11.0/linux-amd64 为例；WireGuard endpoint 需要 1.11+）
SINGBOX_TAG=v1.11.0
SINGBOX_VER=${SINGBOX_TAG#v}
wget -O /tmp/sing-box.tar.gz \
  "https://github.com/SagerNet/sing-box/releases/download/${SINGBOX_TAG}/sing-box-${SINGBOX_VER}-linux-amd64.tar.gz"
tar -xzf /tmp/sing-box.tar.gz -C /tmp
cp /tmp/sing-box-${SINGBOX_VER}-linux-amd64/sing-box x-ui/bin/sing-box-linux-amd64
chmod +x x-ui/x-ui x-ui/bin/sing-box-linux-* x-ui/x-ui.sh
cp x-ui/x-ui.sh /usr/bin/x-ui
cp -f x-ui/x-ui.service /etc/systemd/system/
mv x-ui/ /usr/local/
systemctl daemon-reload
systemctl enable x-ui
systemctl restart x-ui
```

## 使用 Docker 安装

```bash
docker build -t x-ui .            # 默认拉取 sing-box v1.11.0（可通过 --build-arg SINGBOX_VERSION=... 自定义）
docker run -itd --network=host \
  -v $PWD/db/:/etc/x-ui/ \
  -v $PWD/cert/:/root/cert/ \
  --name x-ui --restart=unless-stopped \
  x-ui
```

## SSL 证书申请

脚本内置 SSL 证书申请功能，需满足：

- 知晓 Cloudflare 注册邮箱
- 知晓 Cloudflare Global API Key
- 域名已在 Cloudflare 完成解析并指向当前服务器

默认使用 Let's Encrypt 作为 CA，证书存储于 `/root/cert/`，默认申请泛域名证书。

## Telegram 机器人

面板设置中填入：

- Bot Token
- Bot ChatId
- 周期运行时间（crontab 语法）

通知内容：节点流量使用、面板登录提醒、节点到期提醒、流量预警。

## 建议系统

- CentOS 7+
- Ubuntu 18+
- Debian 10+

# 常见问题

## 关于 Xray 旧数据

x-ui v1.0.0 移除了 Xray 内核与 v2-ui 迁移能力。
首次启动时若检测到旧 Xray 格式的 `inbounds` 表，会自动重命名备份，
新协议下的入站请在面板中重新创建并绑定至 sing-box。

# 版本历史

## v1.0.0（sing-box 单内核重构）

- **重构**：底层代理内核由 Xray 切换为 sing-box；删除 `xray/` 包与 `bin/xray-linux-*` 二进制
- **新增**：核心抽象包 `core/`，`core/singbox/` 提供 sing-box 的 `Config/Process/Stats/Template` 实现
- **新增**：协议枚举扩展至 14 种（vmess、vless、trojan、shadowsocks、hysteria2、tuic、anytls、shadowtls、naive、wireguard、socks、http、mixed、direct）
- **新增**：VLESS 支持 Reality；Shadowsocks 支持 2022 系列加密；Hysteria2/TUIC 支持订阅链接生成
- **新增**：数据库启动时自动检测并备份旧 Xray schema（`inbounds_xray_backup_{ts}`）
- **升级**：Go 1.16 → 1.22；`gin` v1.7 → v1.10；`gorm` v1.21 → v1.25；`gopsutil` v3 → v4；`go-i18n` v2.1 → v2.4
- **重构**：service/controller/job/web 全链路从 `XrayService` 改为 `CoreService`，命名收敛
- **重构**：前端 `xray.js` 移除，新增 `core.js`（含 `TlsBlock`/`RealityBlock`/`TransportBlock`/`SniffBlock` 抽象）
- **重构**：`form/inbound.html` 按 sing-box inbound type 分支；新增 14 个协议表单模板与共享 `_tls.html`/`_transport.html`
- **重构**：`component/inbound_info.html` 信息展示按新协议重写
- **移除**：`v2ui/` 包与 `x-ui v2-ui` 子命令（旧版 Xray schema 迁移已不适用）
- **移除**：`form/stream/`、`form/tls_settings.html`（sing-box 无独立 stream/TLS 对象，合入协议表单）
- **兼容**：保留 `/getXrayVersion`、`/installXray/:version` HTTP 路径以降低前端改造成本；内部实际调用 sing-box 版本获取与安装
- **新增**：面板 UI 全量 i18n 化——扫描并替换 26 个 HTML 模板共 ~210 处硬编码中文为 `{{ i18n "key" }}`，覆盖侧边栏、首页状态卡、入站列表、14 个协议子表单、TLS/transport/sniffing 公共块、信息弹窗、设置页全部 5 个 tab 与所有确认对话框
- **新增**：`web/translation/translate.{zh_Hans,zh_Hant,en_US}.toml` 三语词典同步扩充 91 个 key（按功能分组带注释），原有中文字面量 0 残留（仅 HTML/JS 注释保留作为架构文档）
- **规范**：建立 i18n key 命名规范 `<模块>_<字段>[_ph|_desc|_hint]`（如 `proto_password` / `setting_web_listen_desc` / `sniff_timeout_ph`），后续新增文案按同一约定落 key
- **安全**：密码改用 bcrypt 存储；旧版明文密码在用户首次登录时自动升级为哈希，用户零感知，`web/service/password.go` 提供 `HashPassword` / `VerifyPassword` / `IsBcryptHash` 工具
- **安全**：日志脱敏，移除登录失败路径打印用户提交明文密码的 `logger.Infof`；改为仅记录 `username + ip`
- **安全**：session cookie 强化 `HttpOnly` / `SameSite=Lax` / `MaxAge=6h`，HTTPS 模式下自动加 `Secure`；session 不再持久化密码字段（`session.SetLoginUser` 过滤 `Password`）
- **新增**：登录失败 IP 限流器 `web/service/login_limiter.go`（10 分钟窗口 5 次失败 → 锁 15 分钟，内存存储 + 惰性 GC）
- **新增**：CSRF 中间件 `web/middleware/csrf.go`——session 绑定 token，GET 下发 / 非幂等方法强制校验；模板渲染时通过 `<meta name="csrf-token">` 注入，前端 `axios-init.js` interceptor 自动附带 `X-CSRF-Token`
- **新增**：结构化审计日志 `web/service/audit.go`——登录成功/失败/锁定/登出、用户改密、入站 CRUD、设置变更、面板重启 共 10 类事件，以 `AUDIT {...json}` 行写入主 logger
- **新增**：服务端鉴权文案 i18n 化——`index.go` / `base.go` / `setting.go` / `xui.go` 10 余处硬编码中文改走 `I18n(c, key)`（新增 `auth_*` key 10 条，三语同步）
- **依赖**：新增 `golang.org/x/crypto v0.29.0`（bcrypt）
- **架构**：协议元数据单一来源（SSoT）重构——新增后端包 `core/singbox/spec`（`Spec{Key, Network, IsEndpoint, Shareable, Users}` + 14 协议注册表 + `init` 自检），`database/model.Protocol.IsEndpoint/Network` 改为委托查询，消除枚举双份维护
- **架构**：新增 HTTP 接口 `GET /xui/api/protocols` 与 HTML 模板注入 `window.__PROTOCOL_SPECS__`（`controller/util.go:html` + `common/head.html`），前端零异步即可消费权威元数据
- **架构**：新增前端模块 `web/assets/js/model/protocol_spec.js`，合并后端元数据与 14 协议 `defaults()` 补丁，导出 `ProtocolSpecs` / `isEndpointProtocol` / `isShareableProtocol` / `getProtocolSpec` / `allProtocolKeys`；`js.html` 调整加载顺序确保先于 `core.js`
- **架构**：`core.js` 清理——`Protocols` 常量改为 `allProtocolKeys()` 派生；删除 `EndpointProtocols`/`SHAREABLE_PROTOCOLS`/`isEndpointProtocol` 内部硬编码集合；`Inbound.defaultSettings` 简化为 spec 委托；`InboundSettings` 移除 14 个协议默认值方法仅保留 `fromJson`/`toJson`；`Inbound.canShare` 委托 `isShareableProtocol`
- **架构**：`models.js:DBInbound.hasLink` 14 分支 switch 改为 `isShareableProtocol(this.protocol)` 单行委托
- **架构**：新增协议路径由 "5~7 处同步" 收敛至 "后端注册表 1 处 + 前端 defaults 1 处 + 表单模板 1 处 + i18n key"，并为后续 multi-user 铺路（`Spec.Users` 携带 `Container/Identifier/Credentials` 三元组可定位任意协议的用户字段路径）
- **修复**：`core.js:Inbound.password` getter 移除 `Protocols.NAIVE` 重复 case（NAIVE 本已归入 TROJAN/ANYTLS/SHADOWTLS 组，旧代码在 SOCKS/HTTP/MIXED 组里又列了一次），行为无变化，纯代码整洁
- **架构**：协议能力元数据进一步下沉（方案 C）——`Spec` 新增 `Sniffable bool`，14 协议注册表补齐（12 个代理协议 true，wireguard/direct false）；`protocol_spec.js` 导出 `isSniffableProtocol`；`core.js:Inbound.canSniff` 12 分支 switch 收敛为单行委托
- **架构**：`core.js` 新增 `Inbound._getUserField(fieldName)` 私有方法，按 `UserSchema.{Container, Identifier, Credentials}` 统一派生用户字段取值路径——`uuid` / `password` / `username` 三个 getter 共计 14 个 switch 分支全部收敛为单行委托；shadowsocks 顶层 `password` 特殊形态（Container=""）与 tuic `uuid+password` 双凭证形态（Credentials）均由同一派生规则表达
- **修复**：`inbound_info.html` 的 AnyTLS/ShadowTLS 分支把 "用户名" 值源从 `inbound.username` 改为 `inbound.settings.users[0].name`——此前依赖 `u.username || u.name` 的 fallback 让认证字段名与备注名语义混淆，严格按 `UserSchema.Identifier=password` 语义后，`inbound.username` 对 AnyTLS/ShadowTLS 返回空，备注名改从 `settings.users[0].name` 直读以保持 UI 展示不变

# Stargazers over time

[![Stargazers over time](https://starchart.cc/chaogei/x-ui.svg)](https://starchart.cc/chaogei/x-ui)
