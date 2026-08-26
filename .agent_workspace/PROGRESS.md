# SOTA Performance / Residual Bug Audit

## Goal

再审计 x-ui（HEAD `e65c443`，毛玻璃 Vue 3 面板 + sing-box）是否还有性能缺陷或遗留 bug。在真实产品边界内做 SOTA 打磨：面板侧的并发、统计管道、配置生成、HTTP、SQLite、前端轮询与包体。不伪造与 sing-box 数据面无关的“底层网络传输栈”。

## Git 分支

- Parent: `cursor/sota-perf-audit-7b92`（基于 `origin/main` @ `e65c443`）
- 子代理：云端独立分支，Round 结束后由 Parent 语义合并

## 初始代码状态

- 产品：Go 1.22 单二进制面板 + 外部 sing-box 子进程
- 前端：Vite + Vue 3 + Ant Design Vue 4 玻璃主题，产物嵌入 `web/assets/dist`
- 已知限制（上一轮）：inbound-tag 级流量、Vite 单 chunk ~1.6MB、Safari backdrop-filter 色差、窄屏入站表横向滚动

## Build / Test 基线

- `go version`: go1.22.2 linux/amd64
- Sample: `go test ./core/singbox/... ./web/service/...` PASS（本机，Round 1 前）
- 全量 `go test` / `-race` / CI：Round 1 后由 Parent 复跑

## 已知问题（进入本轮前）

- 流量 job 每 10s 拉 StatsService；status 前端 2s 轮询
- 客户端流量与 inbound 流量可能重复入账（`CoreTrafficJob` 注释写明）
- 无独立网络数据面：代理转发在 sing-box 进程内
- `go.uber.org/atomic` 仍可能冗余；`op/go-logging` 仍在

## Round 状态

- Round 1: **in progress**（6 云端子代理并发）
- Round 2: pending
- Round 3: pending

## 子代理执行记录

（Round 1 派发后回填）

## 修改文件清单 / 测试 / Benchmark / 未解决问题 / 下一轮目标

（各轮结论简报回填）
