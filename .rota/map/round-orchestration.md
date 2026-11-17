---
subsystem: round-orchestration
summary: orchestrator side of a parallel round: lease, drift report, assign, autopilot tick, watch, escalation, ledger, receipts, launcher
touched: 2026-11-17
last_mapped_commit: feae867b58d0ea7ed33556ef145f85dc41dc03e8
---

## Purpose
Rebuilds a round's state from disk and the outside world, assigns issues to slots, runs the autopilot pass, wakes the orchestrator on changes, asks the human questions, records events, and launches the orchestrator session.
Depends on: workers (worker, harness, limits), terminal-hosts (host, layout), hooks-guard (none; `hook` imports no round code, `roundhook` imports round-orchestration packages), plus tracker, config, rotastate/rotatree, fsio, jsonx, gate/postgate, landfreeze, overlap, backlog, marker, stamp, usagelimit.

## Entry points
- internal/cli/round.go:6 — `rota round` verb tree (freeze, wait, watch, start, assign, tick, status, reconcile, wind-down, ...)
- internal/round/start.go:49 — Env.Start: take the lease, provision roster, list candidates
- internal/round/assign.go:377 — Env.Assign: readiness check and hand an item to a slot
- internal/round/round.go:1129 — LocalReport: registry-only report; `Env.Status` at :475 is the full drift report over registry, worktrees, host, forge
- internal/round/winddown.go:76 — Env.WindDown: end the round, verify, report slot outcomes
- internal/roundtick/tick.go:295 — Run: one autopilot pass (repair, merge behind gate, assign); `StopVerifyFailed` at internal/roundtick/stop.go:16
- internal/roundwatch/roundwatch.go:256 — Run: background watcher behind `rota round watch`
- internal/escalation/escalation.go:215 — Send: question to the human on an issue/PR thread
- internal/orchestrate/orchestrate.go:97 — Env.Resolve: plan for the orchestrator launch (`rota orchestrate`)

## Key files / dirs
- internal/round/ — about 52 files: reconcile, assign, start, winddown, lease (re-exports roundlease Read/ClearStale), pick/bestof, slots, needs_you, queue, support, saga, quota, accept/adopt, claims, retarget
- internal/roundcfg/ — round.* config keys (roundcfg.go:61 Settings, :218 FromConfig), support_fence.go (FenceClass, MayRun, SupportMachine), train.go (merge-train ordering)
- internal/roundlease/ — per-repo orchestrator lease keyed on the git common dir (lease.go Acquire :207, Read :162, ClearStale :258)
- internal/roundtick/ — autopilot pass; state.go persists per-round tick state incl. Stopped/StoppedReason (`SetStop` :92); holds.go, review.go, followup.go, targets.go, stop.go (`StopVerifyFailed`), observe.go, readiness.go, adapt.go, wire.go
- internal/roundwatch/ — watcher, autopilot.go, marker file next to the lease (`Armed`), maintainer snapshot, seen.go; the Stop/prompt-hook digest that read this marker moved out to `internal/roundhook`
- internal/roundhook/ — `roundhook.go`: the orchestrator-facing one-line digest, exit digest and Stop block (`Digest`, `ExitDigest`, `NeedsWatch`, `StopBlock`) that `rota hook` prints; reads the registry and `roundwatch.Armed` only (also listed in hooks-guard)
- internal/orchestrate/ — orchestrator launcher; does not run round start or doctor
- internal/escalation/ — escalate send|check, answer parsing, options, delivery; record in workers.json `escalations`
- internal/ledger/ — append-only .rota/ledger.jsonl (ledger.go:125 Append, :138 Load, :265 Trim), recorder.go (Recorder: Note/AppendErr, failure-tolerant appends), summary fold (summary.go:183 Fold), summary_flakes.go, summary_metrics.go, assign.go, session.go
- internal/receipt/ — per-verb request-id receipts so a retry replays (receipt.go:129 Store.Do); cli wrapper internal/cli/receipt.go:48 `receipted`
- CLI glue: internal/cli/round_*.go (tick at round_tick.go:80 `runAutopilotTick`), orchestrate.go, receipt_show.go; each verb's support-fence class is its `Command.Fence` where it is registered (internal/cli/round.go and siblings)
- skills/rota-orchestrate/ — orchestrator skill and its detail files; skills/references/ is symlinked in

## Stack
Go 1.22, stdlib only; forge calls through internal/tracker; state in .rota/workers.json and the git common dir.

## Conventions specific here
- Invariants: one orchestrator per repo (lease on the common dir, not per worktree); round owns no state file; roundtick never decides anything a person would notice (it returns NeedsYou); a failed ledger append never fails the verb (ledger.Recorder); receipt file is gitignored runtime state, pruned at wind-down; forge calls in escalation go only through internal/tracker.
- A support machine (roundcfg.SupportMachine) may run only verbs whose fence class permits it; every verb declares its class on `Command.Fence` (an unclassified verb is refused).
- Everything outside memory (clock, env, runner, forge) is injected via Env structs; tests use fakes.
- Ledger, receipts, flakes etc. are per-developer gitignored files.
- Slot lookup by issue goes through `worker.Registry.SlotHolding` (registry.go:553).

## Test commands
- go test ./internal/round/... ./internal/roundcfg/... ./internal/roundlease/... ./internal/roundtick/... ./internal/roundwatch/... ./internal/roundhook/... ./internal/orchestrate/... ./internal/escalation/... ./internal/ledger/... ./internal/receipt/...
- go test ./internal/cli/ -run 'Round|Receipt|Orchestrate'
- bash test/sections/ files 81_round_wait.sh, 82_round_status.sh, 85_round_start.sh, 103_orchestrate.sh, 108_round_watch.sh, 114_round_autopilot.sh (via test/smoke.sh only at integration boundaries)

## Concerns
- internal/round is large (about 13300 non-test lines, 52 files) and imports worker, harness, host, limits, tracker, landfreeze, overlap.
- roundtick and cli/round_tick.go both hold autopilot glue (`runAutopilotTick` at cli/round_tick.go:80).
- ledger imports gate and flakeledger; round_* CLI files number about 32, so the verb layer is as wide as the domain package.
