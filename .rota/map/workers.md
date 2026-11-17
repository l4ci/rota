---
subsystem: workers
summary: worker slots, registry, gate/train/done verbs, agent harness adapters, subagent definitions, keepalive supervisor, usage limits
touched: 2026-11-17
last_mapped_commit: feae867b58d0ea7ed33556ef145f85dc41dc03e8
---

## Purpose
Standing worker slots (registry, worktrees, accounts, dispatch, merge gate and train, done), the harness seam to Claude/Codex/OpenCode, generated subagent definitions, the keepalive supervisor of the orchestrator, and the usage-limit watcher.
Depends on: terminal-hosts (host, layout), round-orchestration (roundcfg, roundlease, ledger), hooks-guard (none: keepalive and `hook` share `keepstate` instead), gate-landing (gate, land, landfreeze, overlap), review-verdicts (verdict, proof, flakeledger, testledger), plus config, tracker, git, proc, rotatree/rotastate, fsio, jsonx, stamp, hostenv (foundations).

## Entry points
- internal/cli/worker.go:62 — `rota worker` verb tree (pool, adopt, dispatch, inbox, reply, send, poll, session, done, gate, train, account, ...)
- internal/worker/registry.go:27 — Registry: .rota/workers.json model; `LoadRegistry` :42, `LoadRegistryTolerant` :61, `SlotHolding` :553
- internal/worker/listsection.go:9 — `ListSection[T]`: typed Read/Write/Update of one list section of the registry
- internal/worker/pool.go:64 — Env.PoolInit: create slot worktrees and register them
- internal/worker/train.go:292 — Env.Train: verify several PRs together, then land
- internal/worker/done.go:91 — Done: refuse without a test.fast proof row at HEAD
- internal/harness/harness.go:302 — HostTraits (what the round's host offers a harness); concern interfaces from :358 Launcher; :520 Orchestrator interface
- internal/keepalive/keepalive.go:226 — Run: supervise the orchestrator child
- internal/limits/watcher.go:129 — New: watcher behind `rota limit watch`

## Key files / dirs
- internal/worker/ — 85 files (about 22000 non-test lines): registry, pool, dispatch, account*, gate*, train*, done, review loop (reviewloop, reviewdepth, reviewinputs), session, pane, isolate, land lock, verdict, verify tiers, inbox, adopt, bestof, park
- internal/harness/ — adapters claude.go, codex.go, opencode.go (+ opencode_guard.js), promptsig.go (signed prompts), readiness.go, tiers.go, relay.go, quota.go/codex_quota.go, usage.go, login.go, roundread.go, startup_dialog.go, core_*.go (host-core launch/shell identity)
- internal/agents/ — renders .claude/agents/rota-<role>.md and .codex/agents/*.toml (agents.go:110 Render); CLI internal/cli/agents.go:15
- internal/keepalive/ — keepalive.go (supervisor), settings.go, usage.go; the state file, handoff stat and usage handoff marker it shares with the Stop hook live in `internal/keepstate`; CLI internal/cli/keepalive.go:34
- internal/keepstate/ — state.go (`StatePath`, `ReadState`, `WriteState`), records.go (handoff and usage records), hold.go (`SupervisedHold`); imports fsio, rotastate, roundlease, stamp, so `hook` and `keepalive` read each other's state without a cycle
- internal/limits/ — limit parse, cooling, account pick, feed from pane output; `limits` list in workers.json; CLI internal/cli/limit.go:34
- internal/usagelimit/ — account.go (`SelectAccount`, `SameConfigDirIn`, `ExpandConfigDirIn`), pane.go (`FindLimit`, `RulesFor`, `DialogFor`, `StripRelays`): the pane-text rules for a usage window and the account choice over the meters; a leaf below both `worker` and `limits` (imports config, harness, pystr; used by round, orchestrate and doctor as well)
- internal/cli/hook_identity.go — `hookIdentify`: who a hook call belongs to (roundlease holder vs slot)

## Stack
Go 1.22; Claude Code, Codex and OpenCode CLIs behind the harness concern interfaces (harness.go:27 kinds, `Default` = Claude); accounts via CLAUDE_CONFIG_DIR / CODEX_HOME. Slot worktrees live under `rotatree.WorktreeRoot` (`.worktrees`).

## Conventions specific here
- Invariants: callers pick a harness adapter once and never branch on a kind string; agents never overwrites a same-named file lacking the generated header; registry files stay byte-identical to the old helper output; keepalive holds the round lease for its whole life and injects spawn/clock/signals; limits transitions are written under the registry lock.
- Harness reads host facts only through `harness.HostTraits`, never a host kind.
- Registry writers use .lock sidecars via `Update`/`UpdateChanged`; tolerant readers (LoadRegistryTolerant) for views; list sections go through `worker.ListSection`.
- Hosts and forge are reached through injected Deps; tests use fakes (internal/worker/testdata, internal/harness/testdata).

## Test commands
- go test ./internal/worker/... ./internal/harness/... ./internal/agents/... ./internal/keepalive/... ./internal/keepstate/... ./internal/limits/... ./internal/usagelimit/...
- go test ./internal/cli/ -run 'Worker|Keepalive|Limit|Agents'
- bash test/sections/ files 49_worker_dispatch.sh, 91_keepalive.sh, 92_limits.sh, 93_codex_workers.sh, 152_opencode_workers.sh, 111_worker_train.sh (smoke only at integration boundaries)

## Concerns
- internal/worker is the largest package (85 files) and mixes registry, gate, train and review concerns; the package comment and several files still describe themselves as a port of the old hv-worker helpers.
- worker imports about 45 internal packages, including roundcfg, roundlease, ledger and landfreeze, so it sits above the round layer it is also used by.
- limits imports ledger, host and worker (and `usagelimit` for the shared rules): cross-subsystem coupling; keepalive no longer imports hook.
