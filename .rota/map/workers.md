---
subsystem: workers
summary: worker slots, registry, gate/train/done verbs, agent harness adapters, subagent definitions, keepalive supervisor, usage limits
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose
Standing worker slots (registry, worktrees, accounts, dispatch, merge gate and train, done), the harness seam to Claude/Codex/OpenCode, generated subagent definitions, the keepalive supervisor of the orchestrator, and the usage-limit watcher.
Depends on: terminal-hosts (host, layout), round-orchestration (roundcfg, roundlease, ledger), hooks-guard (keepalive imports hook), gate-landing (gate, land, landfreeze, overlap), review-verdicts (verdict, proof, flakeledger, testledger), plus config, tracker, git, proc, rotatree/rotastate, fsio, jsonx, stamp.

## Entry points
- internal/cli/worker.go:45 — `rota worker` verb tree (pool, adopt, dispatch, inbox, reply, send, poll, session, done, gate, train, account, ...)
- internal/worker/registry.go:48 — Registry: .rota/workers.json model; `LoadRegistry` :63, `LoadRegistryTolerant` :82, `SlotHolding` :447
- internal/worker/listsection.go:9 — `ListSection[T]`: typed Read/Write/Update of one list section of the registry
- internal/worker/pool.go:64 — Env.PoolInit: create slot worktrees and register them
- internal/worker/train.go:258 — Env.Train: verify several PRs together, then land
- internal/worker/done.go:90 — Done: refuse without a test.fast proof row at HEAD
- internal/harness/harness.go:302 — HostTraits (what the round's host offers a harness); concern interfaces from :358 Launcher; :519 Orchestrator interface
- internal/keepalive/keepalive.go:208 — Run: supervise the orchestrator child
- internal/limits/watcher.go:128 — New: watcher behind `rota limit watch`

## Key files / dirs
- internal/worker/ — about 70 files (about 18700 lines): registry, pool, dispatch, account*, gate*, train*, done, review loop (reviewloop, reviewdepth, reviewinputs), session, pane, isolate, land lock, verdict, verify tiers, inbox, adopt, bestof, park
- internal/harness/ — adapters claude.go, codex.go, opencode.go (+ opencode_guard.js), promptsig.go (signed prompts), readiness.go, tiers.go, relay.go, quota.go/codex_quota.go, startup_dialog.go, core_*.go (host-core launch/shell identity)
- internal/agents/ — renders .claude/agents/rota-<role>.md and .codex/agents/*.toml (agents.go:110 Render); CLI internal/cli/agents.go:15
- internal/keepalive/ — supervisor, state file, usage handoff marker, hold.go; CLI internal/cli/keepalive.go:33
- internal/limits/ — limit parse, cooling, account pick, feed from pane output; `limits` list in workers.json; CLI internal/cli/limit.go:33
- internal/cli/hook_identity.go — `hookIdentify`: who a hook call belongs to (roundlease holder vs slot)

## Stack
Go 1.22; Claude Code, Codex and OpenCode CLIs behind the harness concern interfaces (harness.go:27 kinds, `Default` = Claude); accounts via CLAUDE_CONFIG_DIR / CODEX_HOME. Slot worktrees live under `rotatree.WorktreeRoot` (`.worktrees`).

## Conventions specific here
- Invariants: callers pick a harness adapter once and never branch on a kind string; agents never overwrites a same-named file lacking the generated header; registry files stay byte-identical to the old helper output; keepalive holds the round lease for its whole life and injects spawn/clock/signals; limits transitions are written under the registry lock.
- Harness reads host facts only through `harness.HostTraits`, never a host kind.
- Registry writers use .lock sidecars via `Update`/`UpdateChanged`; tolerant readers (LoadRegistryTolerant) for views; list sections go through `worker.ListSection`.
- Hosts and forge are reached through injected Deps; tests use fakes (internal/worker/testdata, internal/harness/testdata).

## Test commands
- go test ./internal/worker/... ./internal/harness/... ./internal/agents/... ./internal/keepalive/... ./internal/limits/...
- go test ./internal/cli/ -run 'Worker|Keepalive|Limit|Agents'
- bash test/sections/ files 49_worker_dispatch.sh, 91_keepalive.sh, 92_limits.sh, 93_codex_workers.sh, 152_opencode_workers.sh, 111_worker_train.sh (smoke only at integration boundaries)

## Concerns
- internal/worker is the largest package (about 70 files) and mixes registry, gate, train and review concerns; the package comment and several files still describe themselves as a port of the old hv-worker helpers.
- worker imports about 35 internal packages, including roundcfg, roundlease, ledger and landfreeze, so it sits above the round layer it is also used by.
- keepalive imports hook and limits imports ledger/host/worker: cross-subsystem coupling.
