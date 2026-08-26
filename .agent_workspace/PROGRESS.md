# UI Glass Polish — Agent Workspace

## Goal

持续性优化面板 UI 细节与交互，且**不影响半透明毛玻璃样式**。

隔离分支（云端命名约束）：`cursor/ui-glass-polish-7b92`  
（SOP 中的 `agent/<task-name>` 映射为此分支，不另开 `agent/` 分支以免双轨。）

仓库：`github.com/chaogei/x-ui`  
基线：`origin/main` @ `62d14fcba0f740cf58054dedabf2370a55a1a5ee`

## Glass invariants（硬约束，三轮不得打破）

1. 登录页与登录后共用 `body::before` 极光底图 + `body::after` 噪声；禁止每页各刷不透明底。
2. `.xui-glass` / 侧栏 / 顶栏 / `.ant-card` / 表格容器保持半透明填充 + `backdrop-filter`。
3. 禁止把卡片、表格、弹窗刷成不透明白或接近实心的浅色面板。
4. 页面级玻璃填充不超过约 `rgba(255,255,255,0.12)`；浮层继续用 `--xui-elevated-bg`（约 `rgba(19,25,44,0.78)`），避免两层文字叠字。
5. `theme.ts` 与 `style.css` `:root` 令牌必须成对修改。
6. 保留 `prefers-reduced-transparency`、`prefers-reduced-motion`、`@supports not backdrop-filter` 回退。
7. 保留 skip-link、`:focus-visible`、WCAG AA 文本对比。
8. 新文案必须同时写入 `translate.zh_Hans.toml` / `translate.zh_Hant.toml` / `translate.en_US.toml`。
9. UI 改动后必须 `npm --prefix web/frontend run build` 并提交 `web/assets/dist`。
10. 不改 sing-box 数据面、不回退 Vue 2 / xray-core。

## 当前 Git 分支

- Parent isolation: `cursor/ui-glass-polish-7b92`
- Round 1 子代理将各自开 `cursor/<slice>-7b92`，由 Parent 合入本分支

## 初始代码状态

Vue 3 + Vite + Ant Design Vue 4，暗色玻璃主题已在 `theme.ts` + `style.css` 落地。
已有：skip-link、键盘焦点环、窄屏侧栏遮罩、入站空状态、操作列 `<button>`、登录 2FA 按钮可聚焦。

已知交互/细节缺口（Round 1 靶点，非最终清单）：

- Login：缺 `autocomplete`、空提交未禁用、错误主要靠 toast、无 `aria-invalid` / 表单级错误
- AppShell：折叠钮无 `aria-expanded` / `aria-controls`；Esc 不关窄屏菜单；遮罩无键盘关闭
- Status：轮询失败静默；核心 error 主要藏在 tooltip；无 live region；首屏全 0 无骨架
- Inbounds：`load()` 失败静默返回；无搜索/过滤；空状态无主按钮；操作列过密
- Settings：未保存离开无提示；重启等待 5s 无进度反馈
- 微交互：瓦片/工具条 hover 层次弱；按钮按压态不完整

## Build / Test 基线状态

见下方 Baseline 段（Parent 在派发 Round 1 同时执行）。

## 已知问题

- 云端子代理若遇 `resource_exhausted`，Parent 必须报告失败、禁止静默换模型，并重试同 slug。
- `gh` 只读；PR 用 ManagePullRequest。

## Round 状态

- Round 1: **in progress**
- Round 2: pending
- Round 3: pending

## 子代理执行记录

### Round 1（目标：6 并发云端 Task）

| ID | 简称 | 实际 slug | 主攻 | 状态 |
|----|------|-----------|------|------|
| F1 | fable | claude-fable-5-thinking-xhigh | 设计系统/令牌/玻璃层次架构审计 + 安全微抛光 | launching |
| F2 | fable | claude-fable-5-thinking-xhigh | 独立 SOTA UX/a11y/状态机审计 + 交互修复 | launching |
| O1 | opus-fast | claude-opus-5-thinking-high-fast | Login + AppShell + Status 交互落地 | launching |
| O2 | opus-fast | claude-opus-5-thinking-high-fast | Inbounds + Settings + 弹层交互落地 | launching |
| G1 | gpt-sol | gpt-5.6-sol-xhigh-fast | 玻璃不变量探针 + render-smoke 扩展 | launching |
| G2 | gpt-sol | gpt-5.6-sol-xhigh-fast | 交互/边界/reduced-motion/键盘探针 | launching |

## 修改文件清单

（Round 1 结束后由 Parent 汇总）

## 测试结果

（待填）

## 性能 Benchmark

（待填：bundle size、首屏、backdrop-filter 成本）

## UI / GUI / 前端验收状态

未开始。毛玻璃不得回退为实心白/实心深色（reduced-transparency 回退除外）。

## 未解决问题

见已知交互缺口。

## 下一轮目标

Round 1 结论简报产出后注入 Round 2。
