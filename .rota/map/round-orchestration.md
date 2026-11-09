---
subsystem: round-orchestration
summary: orchestrator side of a parallel round: lease, drift report, assign, autopilot tick, watch, escalation, ledger, receipts, launcher
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose
Rebuilds a round's state from disk and the outside world, assigns issues to slots, runs the autopilot pass, wakes the orchestrator on changes, asks the human questions, records events, and launches the orchestrator session.
Depends on: workers (worker, harness, limits), terminal-hosts (host, layout), hooks-guard (none; hook imports round), plus tracker, config, rotastate/rotatree, fsio, jsonx, gate/postgate, landfreeze, overlap, backlog, marker, stamp.

## Entry points
- internal/cli/round.go:6 — `rota round` verb tree (freeze, wait, watch, start, assign, tick, status, reconcile, wind-down, ...)
- internal/round/start.go:50 — Env.Start: take the lease, provision roster, list candidates
- internal/round/assign.go:721 — Env.Assign: readiness check and hand an item to a slot
- internal/round/round.go:1093 — LocalReport: registry-only report; `Env.Status` at :468 is the full drift report over registry, worktrees, host, forge
- internal/round/winddown.go:77 — Env.WindDown: end the round, verify, report slot outcomes
- internal/roundtick/tick.go:270 — Run: one autopilot pass (repair, merge behind gate, assign); `StopVerifyFailed` at :33
- internal/roundwatch/roundwatch.go:166 — Run: background watcher behind `rota round watch`
- internal/escalation/escalation.go:215 — Send: question to the human on an issue/PR thread
- internal/orchestrate/orchestrate.go:97 — Env.Resolve: plan for the orchestrator launch (`rota orchestrate`)

## Key files / dirs
- internal/round/ — about 45 files: reconcile, assign, start, winddown, lease (re-exports roundlease Read/ClearStale), pick/bestof, slots, needs_you, queue, support, saga, quota, accept/adopt, claims, retarget
- internal/roundcfg/ — round.* config keys (roundcfg.go:61 Settings, :209 FromConfig), support_fence.go (FenceClass, MayRun, SupportMachine), train.go (merge-train ordering)
- internal/roundlease/ — per-repo orchestrator lease keyed on the git common dir (lease.go Acquire :207, Read :162, ClearStale :258)
- internal/roundtick/ — autopilot pass; state.go persists per-round tick state incl. Stopped/StoppedReason (SetStop :94); holds.go, review.go, followup.go, targets.go
- internal/roundwatch/ — watcher, autopilot.go, marker file next to the lease, maintainer snapshot, seen.go
- internal/orchestrate/ — orchestrator launcher; does not run round start or doctor
- internal/escalation/ — escalate send|check, answer parsing, options, delivery; record in workers.json `escalations`
- internal/ledger/ — append-only .rota/ledger.jsonl (ledger.go:125 Append, :138 Load, :265 Trim), recorder.go (Recorder: Note/AppendErr, failure-tolerant appends), summary fold (summary.go:164 Fold), summary_flakes/metrics.go
- internal/receipt/ — per-verb request-id receipts so a retry replays (receipt.go:129 Store.Do); cli wrapper internal/cli/receipt.go:45 `receipted`
- CLI glue: internal/cli/round_*.go (tick at round_tick.go:91 autopilotTick), orchestrate.go, receipt_show.go; per-verb support-fence classes live in internal/cli/tree_gates.go (`fenceDecls`)
- skills/rota-orchestrate/ — orchestrator skill and its detail files; skills/references/ is symlinked in

## Stack
Go 1.22, stdlib only; forge calls through internal/tracker; state in .rota/workers.json and the git common dir.

## Conventions specific here
- Invariants: one orchestrator per repo (lease on the common dir, not per worktree); round owns no state file; roundtick never decides anything a person would notice (it returns NeedsYou); a failed ledger append never fails the verb (ledger.Recorder); receipt file is gitignored runtime state, pruned at wind-down; forge calls in escalation go only through internal/tracker.
- A support machine (roundcfg.SupportMachine) may run only verbs whose fence class permits it; every verb declares its class in cli/tree_gates.go.
- Everything outside memory (clock, env, runner, forge) is injected via Env structs; tests use fakes.
- Ledger, receipts, flakes etc. are per-developer gitignored files.
- Slot lookup by issue goes through `worker.Registry.SlotHolding` (registry.go:447).

## Test commands
- go test ./internal/round/... ./internal/roundcfg/... ./internal/roundlease/... ./internal/roundtick/... ./internal/roundwatch/... ./internal/orchestrate/... ./internal/escalation/... ./internal/ledger/... ./internal/receipt/...
- go test ./internal/cli/ -run 'Round|Receipt|Orchestrate'
- bash test/sections/ files 81_round_wait.sh, 82_round_status.sh, 85_round_start.sh, 103_orchestrate.sh, 108_round_watch.sh, 114_round_autopilot.sh (via test/smoke.sh only at integration boundaries)

## Concerns
- internal/round is large (about 12000 non-test lines, 45 files) and imports worker, harness, host, limits, tracker, landfreeze, overlap.
- roundtick and cli/round_tick.go both hold autopilot glue (autopilotTick at cli/round_tick.go:91).
- ledger imports gate and flakeledger; round_* CLI files number about 40, so the verb layer is as wide as the domain package.
