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
- **多语言面板**：简体中文 / 繁体中文 / English 全量覆盖
  （默认跟随浏览器 `Accept-Language`；侧边栏「语言」菜单写入 `lang` cookie 后以 cookie 为准）
- **首次启动自动生成随机管理员密码**（bcrypt 存储，明文只打印一次）
- **CSRF 防护**：所有非幂等接口强制校验 `X-CSRF-Token`
- **健康探针**：`GET /healthz`（存活）、`GET /readyz`（数据库可达）
- 更多高级配置，详见面板

# 首次登录

面板首次启动（数据库为空）时会用 `crypto/rand` 生成一个 20 位随机管理员密码，
以 bcrypt 哈希入库，并把明文**只打印一次**到 stderr。取回方式：

```bash
# systemd 部署
journalctl -u x-ui -n 50 | grep -A3 "初始管理员账号"

# Docker 部署
docker logs x-ui 2>&1 | grep -A3 "初始管理员账号"
```

用户名固定为 `admin`。登录后面板顶部会一直显示告警，直到你在
「面板设置 → 用户设置」里改掉密码为止。

忘记密码时重设：

```bash
/usr/local/x-ui/x-ui setting -username <新用户名> -password <新密码>
```

> 注意：`x-ui setting -show` 不会回显密码。数据库里只有 bcrypt 哈希，
> 它既不能用来登录，也不该被打印到终端或运维日志。

# 安装 & 升级

```bash
bash <(curl -Ls https://raw.githubusercontent.com/chaogei/x-ui/main/install.sh)
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

# 首次启动生成的管理员密码在容器日志里，只打印一次：
docker logs x-ui
```

数据库路径可用环境变量覆盖（容器/测试场景有用）：

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `XUI_DB_PATH` | 数据库文件完整路径 | 空 |
| `XUI_DB_FOLDER` | 数据库所在目录 | `/etc/x-ui` |
| `XUI_DEBUG` | `true` 时开启调试模式（模板热加载、SQL 日志） | 空 |
| `XUI_LOG_LEVEL` | `debug` / `info` / `warn` / `error` | `info` |

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

# 运维

## 健康检查

两个端点均无需登录、不返回任何机密信息：

| 端点 | 语义 |
| --- | --- |
| `GET /healthz` | 进程存活即 200，不触碰数据库 |
| `GET /readyz` | 数据库 Ping 通即 200 |
| `GET /readyz?core=1` | 额外要求 sing-box 内核在运行 |
| `GET /api/v1/health`、`GET /api/v1/ready` | 上面两者的别名 |

自定义 `basePath` 时，这些路径在根路径与 basePath 下同时可用。

## 反向代理与客户端 IP

面板默认**不信任任何反向代理**：`X-Forwarded-For` / `X-Real-IP` 被完全忽略，
客户端 IP 取 TCP 对端地址。登录失败限流与审计日志都基于这个判定，
因此伪造请求头无法绕过锁定。

若面板确实部署在 Nginx / Caddy / CDN 之后，在
「面板设置 → 受信代理网段」填入前置代理的 CIDR（逗号分隔），此后才会沿 XFF 回溯。

## 服务加固

`x-ui.service` 默认启用 `NoNewPrivileges`、`ProtectSystem=strict`、
`ProtectHome=yes`、`PrivateTmp=yes`，并通过 `ReadWritePaths` 只放开
`/etc/x-ui` 与 `/usr/local/x-ui`。这些指令在 root 下同样生效。

若改为非 root 用户运行，绑定 1024 以下端口需要额外补：

```ini
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
```

# 开发

```bash
go build ./...
go vet ./...
go test ./...          # 全部用例，不需要 sing-box 二进制
go test -race ./...    # CI 使用的形式
```

测试全程使用 `t.TempDir()` 下的临时 SQLite 数据库（经 `XUI_DB_PATH` 注入），
不会读写 `/etc/x-ui`，也不需要 sing-box 二进制。SQLite 驱动为纯 Go 的
`glebarez/sqlite`，因此 `CGO_ENABLED=0` 也能构建（`-race` 仍需 cgo）。

端到端用例（`web/e2e_*_test.go`）把真正的 gin 引擎架在 `httptest` 上，
中间件栈与内嵌模板都与生产一致，覆盖：

| 场景 | 关键断言 |
| --- | --- |
| 首次启动 | 随机口令、bcrypt 落库、公告只打印一次 |
| 登录 | 缺失 / 错配 / 跨会话的 CSRF token 一律 403 |
| 限流 | 连续失败锁定；伪造 `X-Forwarded-For` 换不到新分桶 |
| 入站 CRUD | 保留键、非对象 settings、坏端口、Reality 密钥错配均被拒且不落库 |
| 设置 | 非法时区 / 端口 / CIDR 被拒，且不留下部分写入 |
| 登出 | 删除 cookie 的 Path 与登录时一致（含自定义 basePath） |
| 探针 | `/healthz`、`/readyz`（`?core=1` 时纳入内核状态）无需认证且不泄露信息 |
| i18n | `lang` cookie 优先于 `Accept-Language`；并发混合语言请求不串台 |
| 生命周期 | 优雅停机不超时；端口被占时 `Start` 失败也能干净收场 |

单元测试另外用 golden 文件锁定 14 种协议的 sing-box 配置序列化结果，
并对照 `protocol_spec.js` 校验前后端协议表一致——这两份表分处 Go 与 JS，
没有编译期约束。

# 版本历史

## v1.0.1（构建修复与安全加固）

### 构建

- **修复**：`web/entity/entity.go` 因 `err` 变量作用域错误无法编译，`AllSetting.CheckValid` 从未通过构建
- **修复**：`go.sum` 缺失约 363 条目（新增 `golang.org/x/crypto` 时未 `go mod tidy`）
- **移除**：`github.com/sagernet/sing-box` —— 声明为直接依赖但零 import
- **修复**：`util/sys` 的 `//go:linkname HostProc` 指向 gopsutil **v3** 内部符号而项目已升到 v4，Linux 链接失败；改为自实现（`$HOST_PROC` / `/proc`），并删除配套的空 `a.s`
- **变更**：SQLite 驱动 `mattn/go-sqlite3` → `glebarez/sqlite`（纯 Go），构建与发布不再需要 CGO
- **新增**：`.github/workflows/ci.yml` —— push / PR 触发 build、vet、gofmt、tidy 校验与 `go test -race`

### 安全

- **Critical**：session secret 原由 `math/rand`（`UnixNano` 种子）在包初始化时生成；`util/random` 改用 `crypto/rand`，secret 改为首次使用时惰性生成并持久化
- **Critical**：文件权限 —— `bin/config.json` 0777 → 0600；内核二进制 0777 → 0700；数据目录 `fs.ModeDir`（实为 0000）→ 0700；数据库文件 0600
- **High**：`installCore` 的 `version` 未经校验即拼进下载 URL 与 `os.Create` 文件名；现按正则白名单校验、归档写入 `os.CreateTemp`、HTTP 客户端带超时且重定向限定 GitHub 主机
- **High**：`install.sh` / `x-ui.sh` 移除全部 `--no-check-certificate`
- **High**：不再播种 `admin/admin` 明文口令；首次启动生成 20 位 CSPRNG 随机密码（bcrypt 存储，明文只打印一次），并重新启用被 `v-if="false"` 写死隐藏的面板告警
- **High**：`getRemoteIp` 与审计日志不再无条件信任 `X-Forwarded-For`；默认 `SetTrustedProxies(nil)`，新增 `webTrustedProxies` 设置项
- **Low**：登出时清除 cookie 的 Path 与登录下发时一致（自定义 basePath 下原先根本没删掉）
- **Low**：新增 CSP、`X-Content-Type-Options`、`Referrer-Policy`、`X-Frame-Options`，HTTPS 下补 HSTS
- **Low**：显式 `tls.Config{MinVersion: tls.VersionTLS12}`
- **Low**：`x-ui setting -show` 不再把 bcrypt 哈希当密码打印
- **Low**：`x-ui.service` 增加 `NoNewPrivileges` / `ProtectSystem=strict` / `ProtectHome` / `PrivateTmp` / `ReadWritePaths`

### 正确性

- **Medium**：i18n localizer 由包级共享变量改为请求级；模板渲染改走 `web/render`（每请求 Clone 模板并重绑 FuncMap），消除并发请求语言串台
- **Medium**：实现 README 早已声明的 `lang` cookie（优先级高于 `Accept-Language`），并在侧边栏加语言切换菜单
- **Medium**：`jsonMsg` 的「成功 / 失败」后缀与各控制器操作名改走 i18n key
- **Medium**：Telegram 通知在构造 bot 前先检查 `tgBotEnable`；bot 客户端缓存复用、带 10s 超时、`Debug` 跟随 `config.IsDebug()`；登录路径改异步投递
- **Medium**：入站 Settings 在写库前校验（必须是 JSON 对象、禁止 `type`/`tag`/`listen`/`listen_port` 保留键），错误带字段名返回
- **Medium**：`Server.Stop` 不再把已 cancel 的 context 传给 `httpServer.Shutdown`；改用独立的 10s 超时 context，排空后才 cancel
- **修复**：Reality 分享链接 —— `RealityBlock` 缺 `public_key` 导致 `pbk` 恒为空、客户端全部握手失败；补齐模型与表单字段，新增 `POST /xui/api/reality/keypair` 服务端成对生成，并在写库时校验 `public_key` 由 `private_key` 派生
- **修复**：内核重启防抖循环加入指数退避（10s → 封顶 10min，成功即复位），避免配置永久非法时每 10 秒重启一次
- **修复**：`//go:embed html/*` 会跳过以 `_` 开头的文件，`html/xui/form/_tls.html` 与 `_transport.html` 因此没进二进制；release 构建里每个协议表单都在渲染时报 `no such template "form/_tls"`，入站页返回 200 但内容为空（开发模式从磁盘读模板，本地看不出来）。改用 `all:` 前缀，并补两条护栏用例
- **修复**：`master` → `main` 分支引用（`x-ui.sh`、`README.md`）
- **清理**：`web/service/config.json` 与 `core/singbox.DefaultTemplate` 是逐字节相同的两份默认模板，改为单一来源；`autp_https_conn.go` 更名为 `auto_https_conn.go`
- **新增**：`XUI_DB_PATH` / `XUI_DB_FOLDER` 环境变量覆盖数据库路径
- **新增**：`/healthz`、`/readyz` 探针
- **新增**：审计日志改用 `log/slog` JSON handler，字段不可被用户输入伪造；新增结构化访问日志中间件
- **新增**：完整测试套件（单元 + httptest E2E + 并发/竞态），此前仓库零测试文件

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
- **接口**：内核相关接口路径为 `POST /server/getCoreVersion` 与 `POST /server/installCore/:version`（Xray 时代的 `/getXrayVersion`、`/installXray/:version` 已随内核一并移除，未保留别名）
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
