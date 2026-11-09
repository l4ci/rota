---
subsystem: terminal-hosts
summary: terminal host abstraction (herdr, herdr-core, tmux, Orca) that runs worker sessions, and the herdr pane layout (split/tabs)
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose
Drives the terminal host that runs worker sessions (tmux windows, herdr tabs, herdr-core terminals, Orca terminals) behind one Host interface, and rearranges a round's herdr panes between one grid and one tab per worker.
Depends on: fsio, harness, pidlive, proc, shlex, strutil, tui, repos (layout). Used by workers and round-orchestration.

## Entry points
- internal/host/host.go:413 — Host interface; :444 New(kind, Deps) builds the backend
- internal/host/host.go:292 — Capabilities (what a backend can do); `CapabilitiesForKind` :357
- internal/host/policy.go:75 — `kindPolicy`: per-kind capability overlay (InSessionRule, ServerProc, Probe, Health, SessionAttach)
- internal/host/resolve.go:30 — Resolve: pick the host kind; :80 ResolveLaunch
- internal/host/herdr.go:33 — herdr backend; tmux.go:28 tmux; orca.go:22 Orca; core.go:23 herdr-core (`coreHost`)
- internal/host/layout.go:26 — Layouter interface (pane moves)
- internal/host/events.go:30 — Watcher interface (output events)
- internal/layout/layout.go:463 — Arrange; :228 ToSplit and :346 ToTabs
- internal/cli/layout.go:25 — `rota layout [split|tabs]`

## Key files / dirs
- internal/host/ — 28 files: host.go (interface, Kind, Deps, Capabilities), policy.go, resolve.go (Resolve, Solo), herdr.go, tmux.go, orca.go, core.go + core_*.go (herdr-core transport: input, observe, pending, pids, spawn, watch), tabs.go, panes.go, snapshot.go, open.go, label.go, draft.go (dialog/draft), events.go, stream.go, layout.go, address.go, filesystem.go, pickup.go, sendclass.go, process_evidence.go
- internal/layout/ — grid for a wide screen: left column 40% for the orchestrator (see package comment); state via layout.State

## Stack
Go 1.22; shells out to herdr, tmux, Orca binaries only through Deps.Run (ExecRunner at host.go:80); herdr-core is a separate socket transport (dials the inherited endpoint, never starts a server). Kinds (host.go:40): herdr, herdr-core, tmux, orca; `solo` (host.go:32) is a round-level value with no pane and no Host.

## Conventions specific here
- Invariants: SAFETY: closing a herdr tab, tmux window or Orca terminal kills live agents; nothing execs a binary directly, every command goes through Deps.Run, which tests replace with a fake; layout only moves panes (no restarts, nothing typed).
- Callers read `Capabilities` (host.go:292, `CapabilitiesOf`) and never compare a Kind; a new host edits policy.go and its adapter only.
- A herdr-core context (HC_TERMINAL_ID/HC_TERMINAL_SOCKET) wins in Resolve/ResolveLaunch; an explicit conflicting round.dispatch is refused.
- Orca is opt-in.
- Fixtures live in internal/host/testdata (captured pane text per Claude Code version).

## Test commands
- go test ./internal/host/... ./internal/layout/...
- go test ./internal/cli/ -run 'Layout|HostResolve'
- bash test/sections/51_herdr_dispatch.sh, 113_tmux_boot_detect.sh, 93_codex_workers.sh, 100_continuous_round.sh touch hosts (smoke only at integration boundaries)

## Concerns
- host is about 7000 non-test lines; core.go (1007), herdr.go (907) and orca.go (838) are the biggest backends; hand-parsed dialog keys (herdr.go:191 DialogKeys) depend on harness prompt text.
- Package comment still says it ports the old bin/hv-host-*.sh helpers.
- host imports harness, and harness reads host facts through HostTraits: the seam is a copy of facts, not a shared type.
