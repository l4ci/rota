---
subsystem: terminal-hosts
summary: terminal host abstraction (herdr, herdr-core, tmux, Orca) that runs worker sessions, and the herdr pane layout (split/tabs)
touched: 2026-11-22
last_mapped_commit: 9544e238cf1b73e8e121cbe5007c8993b37af7fd
---

## Purpose
Drives the terminal host that runs worker sessions (tmux windows, herdr tabs, herdr-core terminals, Orca terminals) behind one Host interface, and rearranges a round's herdr panes between one grid and one tab per worker.
Depends on: fsio, git, harness, pidlive, proc, shlex, strutil, tui, repos (layout). Used by workers and round-orchestration.

## Entry points
- internal/host/host.go:383 — Host interface; :426 New(kind, Deps) builds the backend
- internal/host/host.go:285 — Capabilities (what a backend can do); `CapabilitiesForKind` :341
- internal/host/policy.go:245 — `kindPolicy`: per-kind overlay of Capabilities fields (InSessionRule, LaunchKindBound, Harness, Server, Attach, Diagnose, ...) from the `kindSpecs` table (:64, built in `init`); `PaneKinds` at :67 lists the pane kinds in resolution order
- internal/host/resolve.go:30 — Resolve: pick the host kind; :79 ResolveLaunch
- internal/host/herdr.go:31 — herdr backend; tmux.go:29 `tmux`; orca.go:17 `orca`; core.go:23 herdr-core (`coreHost`)
- internal/host/layout.go:26 — Layouter interface (pane moves)
- internal/host/events.go:30 — Watcher interface (output events)
- internal/layout/layout.go:459 — Arrange; :225 ToSplit and :344 ToTabs
- internal/cli/layout.go:25 — `rota layout [split|tabs]`

## Key files / dirs
- internal/host/ — 38 non-test files: host.go (interface, Kind, Deps, Capabilities), policy.go, resolve.go (Resolve, Solo), herdr.go + herdr_kill/send/status.go, tmux.go (boot wait answers startup dialogs with `DialogKeys`, up to `maxBootDialogs`; a stuck pane returns a `*DialogError`), orca.go + orca_handle/kill/send.go, core.go + core_*.go (herdr-core transport: input, observe, pending, pids, spawn, watch), bootready.go, panelist.go, tabs.go, panes.go, snapshot.go, open.go, label.go, draft.go (dialog/draft), events.go, stream.go, layout.go, address.go, filesystem.go (`FileSystem.Glob(dir, pattern)` over `fsio.GlobDir`), pickup.go, sendclass.go, process_evidence.go (process identity: PID plus start time, fail-closed while alive) and process_evidence_store.go (`evidenceStore`: the one locked, enveloped on-disk store the herdr-core `core_pids.go` and Orca `orca_kill.go` backends share), delivery_evidence.go (`relayDeliveryEvidence`: relay-echo matching), dialog.go (`DialogKeys`: startup-dialog grammar)
- internal/layout/ — grid for a wide screen: left column 40% for the orchestrator (see package comment); state via layout.State

## Stack
Go 1.22; shells out to herdr, tmux, Orca binaries only through Deps.Run (`Deps.Run` at host.go:81, default `proc.Run`); herdr-core is a separate socket transport (dials the inherited endpoint, never starts a server). Kinds (host.go:38): herdr, herdr-core, tmux, orca; `Solo` (host.go:31) is a round-level value with no pane and no Host.

## Conventions specific here
- Invariants: SAFETY: closing a herdr tab, tmux window or Orca terminal kills live agents; nothing execs a binary directly, every command goes through Deps.Run, which tests replace with a fake; layout only moves panes (no restarts, nothing typed).
- Callers read `Capabilities` (host.go:285; `CapabilitiesOf` at :357) and never compare a Kind; a new host edits policy.go and its adapter only.
- A herdr-core context (HC_TERMINAL_ID/HC_TERMINAL_SOCKET) wins in Resolve/ResolveLaunch; an explicit conflicting round.dispatch is refused.
- Orca is opt-in.
- Which env vars mark a host session is `hostenv` (foundations); `host` does not read them itself.
- Fixtures live in internal/host/testdata (captured pane text per Claude Code version).

## Test commands
- go test ./internal/host/... ./internal/layout/...
- go test ./internal/cli/ -run 'Layout|HostResolve'
- bash test/sections/51_herdr_dispatch.sh, 113_tmux_boot_detect.sh, 93_codex_workers.sh, 100_continuous_round.sh touch hosts (smoke only at integration boundaries)

## Concerns
- host is about 7500 non-test lines (tmux.go 400); core.go (1014) is the biggest backend, herdr.go (383, plus herdr_kill/send/status.go) and orca.go (407, plus orca_*.go) are split by concern; hand-parsed dialog keys (`DialogKeys`, dialog.go:96, `DialogError` :161) depend on harness prompt text.
- host imports harness, and harness reads host facts through HostTraits: the seam is a copy of facts, not a shared type.
