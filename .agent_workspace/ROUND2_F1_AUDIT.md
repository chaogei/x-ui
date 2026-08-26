# Round 2 — F1 re-audit of the MERGED tree (`cursor/sota-perf-audit-7b92`)

Audited at merge head `09adb3a`, re-verified after sibling commits `a64e830` +
`7b47282` landed mid-audit. Verification: `CGO_ENABLED=0 go build ./...` and
`go test ./...` PASS at both heads; `go test -race` PASS at `09adb3a`.

**ATTENTION: the branch is now RED under `-race`.** Sibling commit `7b47282`
added `panel.startCron()` and `TestE2EStatusPollingConcurrentWithCronRefresh`,
which reproduces H1 deterministically:
`go test -race -run TestE2EStatusPollingConcurrentWithCronRefresh ./web/` FAILS
("race detected"; write = cron populating the fresh `Status` last at
`web/service/server.go:199` then publishing via `web/controller/server.go:45`,
read = HTTP handler marshaling `a.lastStatus` — a textbook unsynchronized
publish). CI runs `go test -race ./...` (`.github/workflows/ci.yml:139-143`),
so CI stays red until Round 3 fixes the controller. Fixing H1 is therefore no
longer optional polish; it gates the branch.

Also landed mid-audit and verified sound:
- `a64e830` — regex-free `parseTrafficName` (`core/singbox/stats.go:28-65`):
  closes the "aggregateTraffic regex allocs" perf item; handles `>>>`-in-tag,
  empty tags, and preserves the old regex's no-newline semantics.
- `7b47282` — closes the "harness cannot exercise cron" gap (`web/harness_test.go:203-218`).

## 1. Round 1 High/Med items — closed vs open in THIS branch

| Sev | Item | Verdict | Evidence (file:line) |
|---|---|---|---|
| High | ServerController lastStatus race | **OPEN** | `web/controller/server.go:45` (cron write) vs `:63` (HTTP read) of `lastStatus`; `:61` (HTTP write) vs `:53` (cron read) of `lastGetStatusTime` (3-word `time.Time`, torn-read prone); `lastVersions`/`lastGetVersionsTime` `:68-80` also unsynchronized between concurrent HTTP requests. No mutex anywhere in the type. |
| High | traffic reset-before-persist | **PARTIAL (unchanged)** | `web/service/core.go:174` still `proc.GetTraffic(true)` (reset-on-read). The carry buffer (`web/job/core_traffic_job.go:37-38,63-77`) survives DB failures only; a panel crash between kernel reset and commit, or a core restart with unqueried counters, still loses bytes. Buffer is memory-only, not flushed at shutdown. |
| High | inbound save clobbers counters | **OPEN** | Backend: `web/service/inbound.go:147-148` copies form `Up`/`Down` onto the row, then `db.Save` at `:162` (whole-row write, also a GetInbound→Save TOCTOU vs the traffic job). Frontend: `web/frontend/src/views/InboundsView.vue:120-121` sends stale `up`/`down` on every save; `resetTraffic` (`:152-165`) **depends** on this behavior — must be migrated to a dedicated endpoint in the same change. |
| Med | TOTP replay race | **OPEN** | `web/service/twofactor.go:269` check in Go memory, `:272-273` unconditional `Update("last_used_step", step)`, no RowsAffected check. Two concurrent logins with the same code both pass. (Contrast: recovery codes already do it right, `:321-330`.) |
| Med | pumpLogs vs cmd.Wait | **OPEN** | `core/singbox/process.go:320-321` (pump goroutines) vs `:325-336` (Wait goroutine) — no ordering. Per os/exec contract, Wait may close the pipes before the pumps drain the tail of stderr, i.e. exactly the fatal-config lines `GetCoreResult` exists to show. |
| Med | gzip / visibility polling | poll **DONE** (`web/frontend/src/poll.ts`, correct: single in-flight tick, hidden-tab pause, exp backoff, resume-refresh); gzip **OPEN** — no compression middleware in `web/web.go:172-270`; 1.25 MiB `xui.js` served identity-encoded. |
| Low | CSP comment says Vue 2 | **OPEN** | `web/middleware/security.go:10-11` (and `security_test.go:44`). Real issue behind it: `'unsafe-eval'` at `security.go:16` is no longer needed — the bundle is Vue 3 runtime-only, precompiled. |
| Low | CSRF non-constant-time compare | **OPEN** | `web/middleware/csrf.go:46` plain `!=`. |
| Low | harness never starts cron | **CLOSED** (by `7b47282`, mid-audit) | `web/harness_test.go:203-218` adds `startCron()`; the new stress test makes H1 fail under `-race`. Note `web/controller` itself still has zero test files. |

Round 1 items verified as genuinely **CLOSED** in this branch: WAL+busy_timeout+pool
(`database/db.go:172-191`), batched join-UPDATE (`web/service/traffic.go:104-160`,
correctness relies on `Inbound.Tag gorm:"unique"` / `Client.Email uniqueIndex` — both
present in `database/model`), commit errors surfaced (`inbound.go:183`, `client.go:242`),
stats client `Close()` (`core/singbox/stats.go:63-73`, `process.go:342-345,420-436`),
crash-loop backoff with deferred settle (`web/service/core.go:237-281`), cron drained
before `StopCore` (`web/web.go:430-438`), bounded login limiter with eviction that
cannot wash out locked IPs (`web/service/login_limiter.go:148-180`), bounded Telegram
queue + cached bot client (`web/job/notify_queue.go`, `web/job/tgbot.go`).

Re-affirmed **NOT A BUG**: the inbound/client dual ledger (documented at
`web/service/traffic.go:70-76`, fixed by test `TestAddTrafficDimensionsDoNotCrossOver`).

## 2. New bugs introduced by O1/O2 (WAL, batched SQL, pending buffer, notify queue)

No high-severity regressions found. The four new subsystems hold up under review and
under `-race`. Minor findings, none blocking:

- **N1 (low)** `web/job/core_traffic_job.go:52-54`: when the core is not running the
  job returns before attempting to flush `pendingInbound`/`pendingUser`. Deltas that
  failed to persist while the core was up stay stuck in memory until the core returns,
  and are lost if the panel stops first. One-line reorder (try pending flush before the
  `IsCoreRunning` gate) when touching H2.
- **N2 (low)** `web/controller/server.go:60-64` + `web/frontend/src/views/StatusView.vue:120`:
  before the first cron refresh `lastStatus` is nil, the endpoint returns `obj: null`,
  the frontend counts it as a failure and backs off — slow first paint after boot.
  Folds into the H1 fix (refresh synchronously on miss, under the new mutex).
- **N3 (info)** `web/service/core.go:187-203`: `RestartCore` holds `state.mu` across
  `proc.Close()` (graceful stop, up to ~5s), stalling `IsCoreRunning`/status handlers
  for that window. Liveness nit, not a correctness bug.

## 3. Round 3 must-fix list (max 6)

1. **ServerController synchronization (GATES CI)** — one mutex over
   `lastStatus/lastGetStatusTime/lastVersions/lastGetVersionsTime`
   (`web/controller/server.go`); refresh-on-nil to fix N2. The regression test
   already exists (`TestE2EStatusPollingConcurrentWithCronRefresh`) and fails
   under `-race` until this lands.
2. **Stop accepting `up`/`down` on inbound update + dedicated reset endpoint** —
   `UpdateInbound` switches to column-selective `Updates` omitting up/down
   (`web/service/inbound.go:147-148,162`); add `POST /xui/inbound/resetTraffic/:id`;
   frontend stops sending up/down and `resetTraffic` uses the new endpoint
   (`web/frontend/src/views/InboundsView.vue:117-132,152-165`). Must land as one change
   or the reset button silently breaks.
3. **TOTP conditional update** — `UPDATE two_factors SET last_used_step = ? WHERE id = ?
   AND last_used_step < ?`, accept only `RowsAffected == 1`
   (`web/service/twofactor.go:266-276`); concurrency test alongside.
4. **pumpLogs/Wait ordering** — WaitGroup: the Wait goroutine waits for both pump
   goroutines before calling `cmd.Wait()` (`core/singbox/process.go:320-336`), so the
   crash tail is never truncated.
5. **HTTP compression + CSP/CSRF hygiene** — gzip (or precompressed embedded assets)
   for text responses in `web/web.go`; drop `'unsafe-eval'` and the Vue 2 comments in
   `web/middleware/security.go` (bundle is precompiled Vue 3); constant-time compare in
   `web/middleware/csrf.go:46`.
6. **Close the remaining H2 window** — flush traffic once before intentional core
   stop/restart, attempt the pending-buffer flush even when the core is down (N1), and
   flush pending on shutdown while cron drains. (Full persist-then-reset needs kernel
   cooperation sing-box does not offer; document the residual crash window instead.)
