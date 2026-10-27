---
subsystem: workers
summary: worker slots, registry, gate/train/done verbs, agent harness adapters, subagent definitions, keepalive supervisor, usage limits
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose
Standing worker slots (registry, worktrees, accounts, dispatch, merge gate and train, done), the harness seam to Claude/Codex/OpenCode, generated subagent definitions, the keepalive supervisor of the orchestrator, and the usage-limit watcher.
Depends on: terminal-hosts (host, layout), round-orchestration (roundcfg, roundlease, ledger), hooks-guard (keepalive imports hook), plus config, tracker, gate, git, proc, rotatree/rotastate, fsio, jsonx, golden.

## Entry points
- internal/cli/worker.go:44 — `rota worker` verb tree (pool, dispatch, gate, train, done, account, session, poll)
- internal/worker/registry.go:45 — Registry: .rota/workers.json model
- internal/worker/pool.go:63 — Env.PoolInit: create slot worktrees and register them
- internal/worker/train.go:176 — Env.Train: verify several PRs together, then land
- internal/worker/done.go:85 — Done: refuse without a test.fast proof row at HEAD
- internal/harness/harness.go:345 — Harness interface (one agent CLI); :419 Orchestrator interface
- internal/keepalive/keepalive.go:207 — Run: supervise the orchestrator child
- internal/limits/watcher.go:115 — New: watcher behind `rota limit watch`

## Key files / dirs
- internal/worker/ — registry, pool, dispatch, account, gate*, train, done, review loop, session, pane, isolate, land lock, verdict, verify tiers
- internal/harness/ — adapters claude.go, codex.go, opencode.go (+ opencode_guard.js), promptsig.go (signed prompts), readiness.go, tiers.go, relay.go
- internal/agents/ — renders .claude/agents/rota-<role>.md and .codex/agents/*.toml (agents.go:104 Render); CLI internal/cli/agents.go:15
- internal/keepalive/ — supervisor, state file, usage handoff marker; CLI internal/cli/keepalive.go:34
- internal/limits/ — limit parse, cooling, account pick, feed from pane output; `limits` list in workers.json; CLI internal/cli/limit.go:34

## Stack
Go 1.22; Claude Code, Codex and OpenCode CLIs behind harness.Harness (harness.go:38 kinds; Default = Claude); accounts via CLAUDE_CONFIG_DIR / CODEX_HOME.

## Conventions specific here
- Invariants: callers pick a harness adapter once and never branch on a kind string; agents never overwrites a same-named file lacking the generated header; registry files stay byte-identical to the old helper output; keepalive holds the round lease for its whole life and injects spawn/clock/signals; limits transitions are written under the registry lock.
- Registry writers use .lock sidecars; tolerant readers (LoadRegistryTolerant) for views.
- Hosts and forge are reached through injected Deps; tests use fakes (internal/worker/testdata, internal/harness/testdata).

## Test commands
- go test ./internal/worker/... ./internal/harness/... ./internal/agents/... ./internal/keepalive/... ./internal/limits/...
- go test ./internal/cli/ -run 'Worker|Keepalive|Limit|Agents'
- bash test/sections/ files 49_worker_dispatch.sh, 91_keepalive.sh, 92_limits.sh, 93_codex_workers.sh, 152_opencode_workers.sh, 111_worker_train.sh (smoke only at integration boundaries)

## Concerns
- internal/worker is the largest package (about 60 files) and mixes registry, gate, train and review concerns; the package comment still calls it a port of the old hv-worker helpers.
- keepalive imports hook and limits imports ledger/host: cross-subsystem coupling.
