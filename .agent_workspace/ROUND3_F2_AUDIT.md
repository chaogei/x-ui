# Round 3 — F2 maintainability / leftover-bug review of `cursor/sota-perf-audit-7b92`

Reviewed at head `2a814e7`. Independent of F1. Scope: confirm the Round 2 claimed
closures (H1/H3/M4/M5/gzip/CSP) in code, and hunt new bugs introduced by the
Round 2 merges (duplicate gzip, inbound-reset dual APIs, race tests).

> **Addendum:** commits `8a1dfab` (H2 drain-before-stop), `7194222` (dead-code
> sweep) and `4d88d00` (stale comments) landed on the branch while this review
> was in flight. They are **outside this review's scope**; the PASS below covers
> the tree at `2a814e7` only. Note they plausibly address R3-4 and parts of §5;
> a later pass should re-verify them.

## Verdict: **PASS**

All six claimed closures are real, in code, with regression tests. The full CI
matrix reproduced locally is green, including the previously-RED `-race` suite.
No new high- or medium-severity bugs from the O1/O2 merges. Findings below are
low/info and fold into the existing Round 3 sprint.

## 1. Claimed closures — verified in code

| Item | Verdict | Evidence (file:line) |
|---|---|---|
| H1 ServerController race | **CLOSED** | `web/controller/server.go:30` (`mu sync.RWMutex`); cron publish under lock `:64-66`; HTTP read+timestamp under lock `:89-92`; versions cache under lock `:98-117`; collection deliberately outside the lock (`:56-67`, documented `:15-24`). Tests: `web/controller/server_test.go:69` (direct Job.Run vs handler), `web/e2e_status_test.go:16` (full HTTP stack), `web/e2e_stress_test.go:85` (real started cron). Full `go test -race ./...` **PASS** — the item that had CI red is resolved. |
| H3 inbound save clobbers counters | **CLOSED, full stack** | Backend: `web/service/inbound.go:155-174` copies only editable fields then `db.Omit("up","down").Save` — the GetInbound→Save TOCTOU on the counter columns is gone; `ResetTraffic` is a targeted UPDATE with a RowsAffected check `:185-195`; route `web/controller/inbound.go:32,131-144`. Client sibling got the same rule: `web/service/client.go:153` (`Omit("up","down","last_seen")`). Frontend: `web/frontend/src/views/InboundsView.vue:123-136` (payload never carries up/down), `:163` (reset posts to `xui/inbound/resetTraffic/:id`). Tests: `web/e2e_inbound_traffic_test.go:42` (stale up=100/down=200 submitted, live counters survive), `:67` (reset endpoint incl. 404 + auth), `:115` (concurrent edits vs traffic accounting sum exactly, race-clean), `web/e2e_panel_test.go:157`, `web/service/counter_preservation_test.go`. |
| M4 TOTP replay race | **CLOSED** | `web/service/twofactor.go:329-337` — `UPDATE ... WHERE id = ? AND last_used_step < ?`, accepted only on `RowsAffected == 1`; enforced in `Verify` `:277-283`. Concurrency tests: `TestVerifyRejectsConcurrentReplay` (`twofactor_test.go:248`), `TestConsumeTOTPStepOnlyMovesForward` (`:203`). |
| M5 pumpLogs vs cmd.Wait | **CLOSED** | `core/singbox/process.go:323-326` (WaitGroup over both pumps), `:331-332` (`pumps.Wait()` strictly before `cmd.Wait()`), `pumpLogs` Done-on-EOF `:367-368`. Tests: `TestStartCapturesEveryLineBeforeTheProcessIsReaped`, `TestStopDrainsTheLogsOfALongRunningCore`, `TestPumpLogsSurvivesAPanickingSink` (`process_test.go:64,108,159`). |
| gzip for embedded assets | **CLOSED** | One middleware, `web/middleware/compress.go` (`CompressStatic`), wired exactly once at `web/web.go:233`, scoped to the asset prefix with the BREACH rationale documented (`compress.go:13-18`, e2e guard `web/e2e_assets_test.go:71`). Correct Vary (`compress.go:53`), Content-Length dropped when compressing (`:136`), already-encoded responses skipped (`:122-124`), Range neutralised (`:58`), `gzip;q=0` honoured (`:73-91`). Wire size verified by `TestE2EAssetsAreServedCompressed`. |
| CSP unsafe-eval + Vue 2 comment | **CLOSED** | `web/middleware/security.go:20-28` — no `'unsafe-eval'`; the stale Vue 2 comment replaced by an accurate account of the two remaining `'unsafe-inline'` (`:9-19`). Guarded three ways: `security_test.go:68`, and a jsdom boot with eval/Function blocked (`web/frontend/scripts/render-smoke.mjs:79`, `web/e2e_render_test.go:226`) which CI refuses to skip (`XUI_REQUIRE_RENDER_TEST=1`, `.github/workflows/ci.yml:134,142`). |
| CSRF constant-time (bonus claim) | **CLOSED** | `web/middleware/csrf.go:53` `subtle.ConstantTimeCompare`. |

## 2. Round 2 merge artifacts — hunted, resolved cleanly

- **Duplicate gzip** (O1 `4c9141b` `compress.go` vs O2 `7605e98` `staticgzip.go`):
  merge `adec257` kept exactly one (`compress.go`) and deleted `staticgzip.go`
  + its tests wholesale. No double wiring, no dead code, no references left
  (grep for `StaticGzip` is empty). Belt-and-braces: even if a second encoder
  appeared, `decide()` skips responses that already carry `Content-Encoding`.
- **Inbound reset dual APIs** (O1 `0d2db22` with a legacy 0/0-form shim vs O2
  `02702fd` + rebuilt bundle): merged to a single route/service/audit-event;
  the shim (`legacyCounterReset`) and its test were then removed in `23f9772`.
  The removal is safe **only** if the embedded bundle really calls the new
  endpoint — verified two ways: the bundle contains `inbound/resetTraffic`,
  and `npm run build:fast` from the merged source reproduces
  `web/assets/dist` byte-for-byte (CI's freshness gate, reproduced locally).
- **Race tests** (parent `7b47282` vs O1 `beb68e5`): both survived under
  distinct names (`TestE2EStatusPollingConcurrentWithCronRefresh` with a real
  started cron; `TestE2EServerStatusUnderConcurrentRefresh` driving the
  registered job directly). Redundant coverage of the same race, no conflicts,
  both green under `-race`. `web/controller` now has real unit tests
  (`server_test.go`, 3 tests), closing the "zero test files" gap F1 noted.

## 3. Verification (local, mirrors `.github/workflows/ci.yml`)

- `go build ./...`, `CGO_ENABLED=0 go build ./...`, `go vet ./...` — PASS
- `CGO_ENABLED=0 XUI_REQUIRE_RENDER_TEST=1 go test -count=1 ./...` — all ok
- `CGO_ENABLED=1 XUI_REQUIRE_RENDER_TEST=1 go test -race -count=1 ./...` —
  **PASS** (x-ui/web 162s; the suite that was RED at Round 2 F1)
- `gofmt -l` clean; `go mod tidy` produces no diff
- frontend: `npm ci`, `npm run typecheck`, `npm run build:fast` → committed
  `web/assets/dist` is byte-identical (BUNDLE-FRESH)

## 4. New findings (none blocking)

- **R3-1 (low)** `web/controller/server.go:77-86`: the every-2s status refresh
  is registered via bare `cron.AddFunc`, bypassing `backgroundJobChain()`
  (`web/web.go:342-347`). No `cron.Recover` means a panic inside
  `ServerService.GetStatus` (gopsutil parsing /proc, disk stats) kills the
  whole panel; every other periodic job gained that containment in `5f15437`.
  Not a Round 2 regression (this job was never wrapped), but the Round 2 brief's
  "SkipIfStillRunning + Recover 包装 cron" overstates coverage by exactly this
  job. One-line fix alongside Round 3 item 1/6.
- **R3-2 (low)** Merge resolution of `web/e2e_assets_test.go` took O1's version,
  silently dropping O2's `0a349e7` assertions (Set-Cookie and Cache-Control
  surviving the compressed response) and the `StaticGzip` concurrency test.
  The concurrency test targeted the deleted cache design (no equivalent state
  exists in `CompressStatic`; its `sync.Pool` use is exercised under `-race`
  by the e2e suite), but the header-survival assertion applied to the current
  design too and is now unpinned. Test-coverage loss only; the code is correct
  (`decide()` touches only Content-Length/Content-Encoding).
- **R3-3 (info)** `web/middleware/compress.go:53`: `Vary: Accept-Encoding` is
  set only on gzip-capable requests, so an identity asset response (cacheable
  a year per `web/web.go:228`) can be stored by a shared cache without the
  Vary key and then served to gzip-capable clients. Correctness is unaffected
  (identity is universally acceptable); a one-line move sets Vary on both paths.
- **R3-4 (info)** Stale comment: `web/e2e_inbound_traffic_test.go:91-92` still
  introduces `TestE2EInboundEditWithoutCounterFieldsKeepsThem` as "the boundary
  of the compat path above", but `23f9772` deleted that compat test. The test
  itself remains valid and valuable.

## 5. Known-open items — bookkeeping is honest

Confirmed still open and correctly declared as Round 3 scope in PROGRESS.md
(no silent regressions, no overclaiming): H2 persist-before-reset
(`web/service/core.go:174` still `GetTraffic(true)`, carry buffer memory-only,
no flush at shutdown); N1 pending flush gated on `IsCoreRunning`
(`web/job/core_traffic_job.go:52-54`); N2 first-poll `obj: null` backoff
(`web/controller/server.go:88-95` + `web/frontend/src/views/StatusView.vue:120`);
N3 `RestartCore` holding `state.mu` across `proc.Close()`.

Recommendation for Round 3 sprint: add R3-1 (wrap the status refresh job) to
the existing list; R3-2/R3-3/R3-4 are fair game for the dead-code/comment
sweep already planned.
