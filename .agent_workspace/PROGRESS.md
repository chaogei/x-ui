# SOTA Performance / Residual Bug Audit

## Goal

再审计 x-ui 控制面性能与遗留缺陷，在真实产品边界内打磨（不伪造 sing-box 数据面）。

## Git 分支

- Parent: `cursor/sota-perf-audit-7b92`
- Round 1 合入：O1 `cursor/backend-traffic-and-core-hardening-5bea`、O2 `cursor/frontend-perf-and-glass-fixes-1640`、G1 `cursor/go-benchmark-probes-c878`、G2 `cursor/stress-boundary-tests-1986`

## 基线

- `origin/main` @ `e65c443`
- Round 1 合并后：`CGO_ENABLED=0 go test ./...` PASS（Parent 复验）

## Round 状态

- Round 1: **complete**（F2 首次因策略拦截失败，已用去敏化提示重跑成功；未换模型）
- Round 2: **in progress**
- Round 3: pending

---

# 《Round 1 结论简报》

## 已实现功能

**O1 后端**
- SQLite WAL + busy_timeout + 连接池；空路径拒绝
- 流量 batched `UPDATE ... FROM`；失败 delta 有界回补
- stats gRPC `Close()`，避免重启泄漏
- 崩溃循环真正推进 restart backoff
- 关机先 drain cron 再停 core
- LoginLimiter 有界；Telegram 64 深队列

**O2 前端**
- `usePolling`：隐藏标签页停轮询、退避、不重叠请求
- antd 按需注册：bundle 1.68MiB → 1.25MiB（gzip 519→385 KB）
- 入站摘要按 id Map；键盘可达操作；skip-link

**G1** `Benchmark*`：MarshalJSON、Equals、stats aggregate、ApplyClients；`scripts/bench.sh`

**G2** 并发 XFF/CRUD/sub/2FA/metrics/traffic；空 DB path 拒绝

**F1/F2 审计共识（仍开放）**
- H1 `ServerController` lastStatus 无锁（cron vs HTTP）
- H2 reset-before-persist 仍可能丢字节（O1 有 pending buffer，未改成 persist-then-reset）
- H3 入站 Update 用表单里的 stale up/down 覆盖实时计数
- M4 TOTP last_used_step 非条件更新
- M5 pumpLogs vs cmd.Wait 顺序
- 无 gzip 中间件；无 waitDone 事件驱动重启
- CSRF 非恒定时间比较；controller 包无单测；harness 不启 cron 所以 H1 测不到

**明确不是 bug**
- inbound 与 client 两本账是有意的双维度，不是重复计费错误

## 遗留缺陷

| Sev | ID | 状态 |
|---|---|---|
| High | ServerController data race | **open** |
| High | traffic reset-before-commit | **partial** (carry buffer) |
| High | inbound save clobbers counters | **open** |
| Med | TOTP replay race | **open** |
| Med | pumpLogs/Wait | **open** |
| Med | gzip / visibility poll | poll **done**; gzip **open** |
| Low | dead code, CSP Vue2 comment | **open** |

## 性能瓶颈

- `aggregateTraffic` ~5k allocs / 2000 counters（regex）
- `ApplyClients` 分配偏高
- `/proc/net/*` 每 2s 全量扫（大连接数时）
- 无 HTTP 压缩嵌入资源

## Round 2 攻坚重点

1. ServerController 加锁 + harness 启 cron 的 -race 测试
2. 入站更新不写 up/down；独立 resetTraffic API
3. TOTP 条件 UPDATE；pumpLogs WaitGroup
4. persist-then-reset 或单事务双账本（若代价可控）
5. gzip/brotli 或预压缩 assets；SkipIfStillRunning
6. stats 解析去正则；恒定时间 CSRF
7. 死代码/过期注释清扫

## 子代理记录

| Agent | Model | 结果 |
|---|---|---|
| F1 | claude-fable-5-thinking-xhigh | 审计完成 |
| F2 | claude-fable-5-thinking-xhigh | 首次拦截；重试完成 |
| O1 | claude-opus-5-thinking-high-fast | 已合入 |
| O2 | claude-opus-5-thinking-high-fast | 已合入 |
| G1 | gpt-5.6-sol-xhigh-fast | 已合入 |
| G2 | gpt-5.6-sol-xhigh-fast | 已合入（冲突已语义合并） |
