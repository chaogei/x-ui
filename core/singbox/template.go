package singbox

// DefaultTemplate 是全新安装时写入设置表的 sing-box 默认配置模板。
//
// 关键点：
//   - experimental.v2ray_api.listen 必须开启并监听本地端口，
//     x-ui 通过这里拉取按 tag 聚合的流量统计。
//   - 默认 route 规则将私网/BT 流量直连/阻断，与旧 Xray 模板语义对齐。
//   - inbound 部分为空数组，由面板中的 inbound 记录动态拼入。
const DefaultTemplate = `{
  "log": {
    "level": "info",
    "timestamp": true
  },
  "dns": {
    "servers": [
      { "tag": "google", "address": "tls://8.8.8.8" },
      { "tag": "local", "address": "local", "detour": "direct" }
    ],
    "strategy": "prefer_ipv4"
  },
  "inbounds": [],
  "outbounds": [
    { "type": "direct", "tag": "direct" },
    { "type": "block", "tag": "block" },
    { "type": "dns", "tag": "dns-out" }
  ],
  "route": {
    "rules": [
      { "protocol": "dns", "outbound": "dns-out" },
      { "ip_is_private": true, "outbound": "direct" },
      { "protocol": "bittorrent", "outbound": "block" }
    ],
    "final": "direct",
    "auto_detect_interface": true
  },
  "experimental": {
    "v2ray_api": {
      "listen": "127.0.0.1:62789",
      "stats": {
        "enabled": true,
        "inbounds": []
      }
    }
  }
}`
