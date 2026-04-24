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

# Stargazers over time

[![Stargazers over time](https://starchart.cc/chaogei/x-ui.svg)](https://starchart.cc/chaogei/x-ui)
