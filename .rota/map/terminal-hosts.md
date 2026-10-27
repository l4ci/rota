---
subsystem: terminal-hosts
summary: terminal host abstraction (herdr, tmux, Orca) that runs worker sessions, and the herdr pane layout (split/tabs)
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose
Drives the terminal host that runs worker sessions (tmux windows, herdr tabs, Orca terminals) behind one Host interface, and rearranges a round's herdr panes between one grid and one tab per worker.
Depends on: fsio, harness, pidlive, proc, shlex, strutil, tui, repos (layout). Used by workers and round-orchestration.

## Entry points
- internal/host/host.go:275 — Host interface; :308 New(kind, Deps) builds the backend
- internal/host/host.go:221 — Capabilities (what a backend can do)
- internal/host/resolve.go:54 — Resolve: pick the host kind; :106 ResolveLaunch
- internal/host/herdr.go:33 — herdr backend; tmux.go:28 tmux backend; orca.go:22 Orca backend
- internal/host/layout.go:26 — Layouter interface (pane moves)
- internal/host/events.go:34 — Watcher interface (output events)
- internal/layout/layout.go:461 — Arrange; :228 ToSplit and :346 ToTabs
- internal/cli/layout.go:24 — `rota layout [split|tabs]`

## Key files / dirs
- internal/host/ — host.go (interface, Deps, Capabilities), resolve.go (Kind, Solo), herdr.go, tmux.go, orca.go, tabs.go, panes.go, snapshot.go, open.go, label.go, draft.go (dialog/draft), events.go, layout.go
- internal/layout/ — grid for a wide screen: left column 40% for the orchestrator (see package comment); state via layout.State

## Stack
Go 1.22; shells out to herdr, tmux, Orca binaries only through Deps.Run (ExecRunner at host.go:40). Kinds: herdr, tmux, orca; `solo` (resolve.go:12) is a round-level value with no pane and no Host.

## Conventions specific here
- Invariants: SAFETY: closing a herdr tab, tmux window or Orca terminal kills live agents; nothing execs a binary directly, every command goes through Deps.Run, which tests replace with a fake; layout only moves panes (no restarts, nothing typed).
- Orca is opt-in.
- Fixtures live in internal/host/testdata.

## Test commands
- go test ./internal/host/... ./internal/layout/...
- go test ./internal/cli/ -run 'Layout|HostResolve'
- bash test/sections/93_codex_workers.sh and 100_continuous_round.sh touch hosts (smoke only at integration boundaries)

## Concerns
- host/herdr.go is the biggest backend; hand-parsed dialog keys (herdr.go:190 DialogKeys) depend on harness prompt text.
- The package comment says it ports the old bin/hv-host-*.sh helpers.
