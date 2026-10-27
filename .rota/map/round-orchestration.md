---
subsystem: round-orchestration
summary: orchestrator side of a parallel round: lease, drift report, assign, autopilot tick, watch, escalation, ledger, receipts, launcher
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose
Rebuilds a round's state from disk and the outside world, assigns issues to slots, runs the autopilot pass, wakes the orchestrator on changes, asks the human questions, records events, and launches the orchestrator session.
Depends on: workers (worker, harness, limits), terminal-hosts (host, layout), hooks-guard (none; hook imports round), plus tracker, config, rotastate/rotatree, fsio, jsonx, gate/postgate, backlog.

## Entry points
- internal/cli/round.go:6 — `rota round` verb tree (start, assign, tick, watch, wait, status, reconcile, wind-down, ...)
- internal/round/start.go:50 — Env.Start: take the lease, provision roster, list candidates
- internal/round/assign.go:612 — Env.Assign: readiness check and hand an item to a slot
- internal/round/round.go:1052 — LocalReport: drift report over registry, worktrees, host, forge
- internal/roundtick/tick.go:235 — Run: one autopilot pass (repair, merge behind gate, assign)
- internal/roundwatch/roundwatch.go:165 — Run: background watcher behind `rota round watch`
- internal/escalation/escalation.go:192 — Send: question to the human on an issue/PR thread
- internal/orchestrate/orchestrate.go:97 — Env.Resolve: plan for the orchestrator launch (`rota orchestrate`)

## Key files / dirs
- internal/round/ — reconcile, assign, start, wind-down, lease re-export, pick/best-of, slots, needs_you, queue, support, saga, quota
- internal/roundcfg/ — round.* config keys (roundcfg.go:61 Settings, :192 FromConfig), support fence, merge-train ordering
- internal/roundlease/ — per-repo orchestrator lease keyed on the git common dir (lease.go:29)
- internal/roundtick/ — autopilot pass; state.go persists per-round tick state; holds.go, review.go
- internal/roundwatch/ — watcher, marker file next to the lease, maintainer snapshot
- internal/orchestrate/ — orchestrator launcher; does not run round start or doctor
- internal/escalation/ — escalate send|check, answer parsing, options; record in workers.json `escalations`
- internal/ledger/ — append-only .rota/ledger.jsonl (ledger.go:120 Append) and summary fold (summary.go:162)
- internal/receipt/ — per-verb request-id receipts so a retry replays (receipt.go:135 Do); cli wrapper internal/cli/receipt.go:31
- skills/rota-orchestrate/ — orchestrator skill and its detail files; skills/references/ is symlinked in

## Stack
Go 1.22, stdlib only; forge calls through internal/tracker; state in .rota/workers.json and the git common dir.

## Conventions specific here
- Invariants: one orchestrator per repo (lease on the common dir, not per worktree); round owns no state file; roundtick never decides anything a person would notice (it returns NeedsYou); a failed ledger append never fails the verb; receipt file is gitignored runtime state, pruned at wind-down; forge calls in escalation go only through internal/tracker.
- Everything outside memory (clock, env, runner, forge) is injected via Env structs; tests use fakes.
- Ledger, receipts, flakes etc. are per-developer gitignored files.

## Test commands
- go test ./internal/round/... ./internal/roundcfg/... ./internal/roundlease/... ./internal/roundtick/... ./internal/roundwatch/... ./internal/orchestrate/... ./internal/escalation/... ./internal/ledger/... ./internal/receipt/...
- go test ./internal/cli/ -run 'Round|Receipt|Orchestrate'
- bash test/sections/ files 81_round_wait.sh, 82_round_status.sh, 85_round_start.sh, 103_orchestrate.sh, 108_round_watch.sh, 114_round_autopilot.sh (via test/smoke.sh only at integration boundaries)

## Concerns
- internal/round is large (about 40 files) and imports worker, harness, host, limits, tracker; roundlease comment says round re-exports Read/ClearStale.
- roundtick and cli/round_tick.go both hold autopilot glue (autopilotTick at cli/round_tick.go:79).
