---
subsystem: foundations
summary: Leaf primitives every other package builds on: atomic files and locks, git and process execution, JSON, timestamps, liveness and small string helpers
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose

The lowest layer of `internal/`. Each package is the one place for its job: writing state files atomically under a lock, running git, running any external binary, encoding JSON, formatting timestamps, deciding whether a pid is alive, and the Python-compatible string behavior some ports must keep.

Depends on: only each other. `git` imports `proc` and `rotatree` (config-and-setup); `fsio` imports `jsonx`; `pidlive` imports `proc`; `strutil` imports `pystr`. `stamp`, `shlex`, `pystr`, `proc` import no rota package. Used by every other subsystem.

## Entry points
- internal/fsio/fsio.go:64 — `WriteFileAtomic`; `WriteJSONAtomic` at :108, `Locked` (sidecar advisory lock) at :150, `UpdateJSON` at :213
- internal/git/git.go:50 — `Runner`; `Exec` at :53 runs git, `Repo` (git.go:38) wraps a directory
- internal/proc/proc.go:70 — `Run`: the one external-binary runner (`Cmd`, `Result`, `Runner`)
- internal/jsonx/jsonx.go:65 — `Decode` (key order preserved); `Marshal` at :130 matches Python `json.dumps(indent=2)`
- internal/stamp/stamp.go:14 — `Format`: UTC, second precision, trailing `Z`; `Parse` at :17
- internal/pidlive/pidlive.go:23 — `Alive`: signal 0 succeeds and the process is not a zombie
- internal/shlex/shlex.go:16 — `Split`: Python `shlex.split` POSIX mode
- internal/pystr/pystr.go:43 — `Strip` and the Unicode `IsSpace`/`IsWord`/`IsDigit` classes

## Key files / dirs
- `internal/fsio/` — `fsio.go` (atomic writes, `Locked`, markers, `UpdateJSON`), `globdir.go`, `home.go` (`ExpandHome`), `testdata/`
- `internal/git/` — `git.go` (Repo, Base, CurrentBranch, Dirty, CommonDir, CommitsSince, `WorktreePath`), `diff.go` (`ChangedPaths`, `ChangedLines`, `RemoteBase`)
- `internal/proc/` — `proc.go`: `DefaultTimeout`, `NoTimeout`, `WaitDelay`
- `internal/jsonx/` — `jsonx.go` ordered `Object`, `Decode`, `Marshal`, `MarshalCompact`, `PyFloat`; `testdata/`
- `internal/stamp/`, `internal/pidlive/`, `internal/shlex/`, `internal/pystr/` (with `testdata/`), `internal/strutil/`

## Stack
Go 1.22, standard library only (`os/exec`, `syscall`, `encoding/json`).

## Conventions specific here
- Invariants: `internal/git` is the one place git is run, so timeout, context and `ErrNoGit` come with it (`TestNoDirectGitExec` in `internal/git/boundary_test.go` fails on direct `exec.Command("git")` under `internal` and `cmd`); `proc` is the one place any external binary runs (exit-code mapping, call timeout and pipe wait delay live there).
- Atomic writes and the sidecar lock stay compatible with the earlier Python helpers so both can share state files; `jsonx` output must not diff against theirs.
- `stamp` is the one registry timestamp format; writers call `Format`, readers call `Parse`, which reports a malformed value rather than returning the zero time.
- `pidlive` is the one rule for "is this pid running"; the host and the orchestrator lease both use it so an unreaped exited holder reads as gone.
- `fsio` errors callers match on: `ErrLockTimeout`, `ErrNotLanded`, `ErrUnreadable`.
- `pystr` and `shlex` exist because Go's `\s`, `\d`, `\w` are ASCII; ports that must match Python use these.
- Callers adapt `git.Runner` and `proc.Runner` instead of carrying their own exec wrappers; tests inject fakes.

## Test commands
- `go test ./internal/fsio/ ./internal/git/ ./internal/stamp/ ./internal/pidlive/ ./internal/proc/ ./internal/jsonx/ ./internal/shlex/ ./internal/pystr/ ./internal/strutil/`

## Concerns
- `jsonx.go` and `fsio.go` byte-compat requirements make small changes affect every state file; the testdata fixtures guard them.
- `pidlive` reads `/proc` and `ps` output (`ProcStat`, `StartTicks`), so it is platform-sensitive.
