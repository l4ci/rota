---
subsystem: foundations
summary: leaf primitives every other package builds on: atomic files, locks and line stores, git and process execution, JSON, timestamps, path globs, bullet lines, liveness and small string helpers
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose

The lowest layer of `internal/`. Each package is the one place for its job: writing state files atomically under a lock (and appending or rewriting line stores), running git, running any external binary, encoding JSON, formatting timestamps, matching configured paths, formatting a knowledge bullet, deciding whether a pid is alive, and the Python-compatible string behavior some ports must keep.

Depends on: each other only. `git` and `pidlive` import `proc`; `fsio` imports `jsonx` and `pystr`; `pystr` imports `stamp`; `strutil` imports `pystr`. `stamp`, `shlex`, `proc`, `jsonx`, `pathglob` and `bulletfmt` import no rota package. Used by every other subsystem.

## Entry points
- internal/fsio/fsio.go:66 — `WriteFileAtomic`; `WriteJSONAtomic` at :126, `Locked` (sidecar advisory lock) at :174, `UpdateJSON` at :248
- internal/fsio/lines.go:12 — `AppendLine`; `ReadLines` at :54 (`Lenient`/`Strict` policy) and `RewriteLines` at :77; `ReadJSON[T]` in readjson.go:15
- internal/git/git.go:49 — `Runner`; `Exec` at :52 runs git, `Repo` (git.go:37) wraps a directory
- internal/proc/proc.go:74 — `Run`: the one external-binary runner (`Cmd` at :38, `Runner` at :71)
- internal/jsonx/jsonx.go:65 — `Decode` (key order preserved); `Marshal` at :131 matches Python `json.dumps(indent=2)`; `Obj` at :429 and `Strings` at :439 build objects
- internal/stamp/stamp.go:18 — `Format`: UTC, second precision, trailing `Z`; `Parse` at :21; `Date` at :29 and `ParseDate` at :34 for `DateLayout` dates
- internal/pathglob/pathglob.go:37 — `MatchEntry`: entry equal to, a directory above, or a glob of the file; `MatchUnder` at :47, `Match` at :68 (`**` as a segment)
- internal/bulletfmt/bulletfmt.go:23 — `Title`: bold title of a `- **Title** — body` bullet; `Render` at :33
- internal/pidlive/pidlive.go:61 — `Alive`: signal 0 succeeds and the process is not a zombie
- internal/shlex/shlex.go:16 — `Split`: Python `shlex.split` POSIX mode

## Key files / dirs
- `internal/fsio/` — `fsio.go` (atomic writes, `Locked`, markers, `UpdateJSON`), `lines.go`, `jsonl.go` (`AppendJSONL`, `ReadJSONL`), `readjson.go`, `temp.go`, `globdir.go`, `home.go` (`ExpandHome`), `fsiotest/`, `testdata/`
- `internal/git/` — `git.go` (`Repo`, `Base`, `CurrentBranch`, `Dirty`, `CommonDirVia`, `CommitsSince`, `WorktreePath`), `diff.go` (`ChangedPaths`, `ChangedLines`, `RemoteBase`), `worktree.go` (`ParseWorktrees`)
- `internal/proc/` — `proc.go` (`DefaultTimeout`, `NoTimeout`, `WaitDelay`), `duplex.go`
- `internal/jsonx/` — `jsonx.go` ordered `Object`, `Decode`, `Marshal`, `MarshalCompact`, `PyFloat`; `testdata/`
- `internal/pathglob/` — `pathglob.go` only (`Clean`, `Match`, `MatchEntry`, `MatchUnder`, `ValidEntry`, `MatchSegments`); callers: knowledge, change, config, overlap, gate, reviewpolicy
- `internal/bulletfmt/` — `bulletfmt.go` only (`Title`, `Render`, `HasTitle`, `Continues`, `End`); callers: knowledge, extknowledge (belongs with knowledge-and-map by use)
- `internal/stamp/`, `internal/pidlive/`, `internal/shlex/`, `internal/pystr/` (with `testdata/`), `internal/strutil/`

## Stack
Go 1.22, standard library only (`os/exec`, `syscall`, `encoding/json`).

## Conventions specific here
- Invariants: `internal/git` is the one place git is run, so timeout, context and `ErrNoGit` come with it (`TestNoDirectGitExec` in `internal/git/boundary_test.go` fails on direct `exec.Command("git")` under `internal` and `cmd`); `proc` is the one place any external binary runs (exit-code mapping, call timeout and pipe wait delay live there).
- Atomic writes and the sidecar lock stay compatible with the earlier Python helpers so both can share state files; `jsonx` output must not diff against theirs.
- `AppendLine` appends under the file lock and fsyncs; `ReadLines` with `Lenient` counts rejected lines as skipped, with `Strict` stops with a `*LineError`.
- `stamp` is the one registry timestamp format; writers call `Format`, readers call `Parse`, which reports a malformed value rather than returning the zero time.
- `pathglob` is the one answer to "does this repo-relative file match this configured path"; callers choose `Match`, `MatchEntry` or `MatchUnder` by what the configured string means.
- `bulletfmt` owns the bullet line shape; what goes in the stamp and where bullets live stay with each knowledge store.
- `pidlive` is the one rule for "is this pid running"; the host and the orchestrator lease both use it so an unreaped exited holder reads as gone.
- `fsio` errors callers match on: `ErrLockTimeout`, `ErrNotLanded`, `ErrUnreadable`.
- `pystr` and `shlex` exist because Go's `\s`, `\d`, `\w` are ASCII; ports that must match Python use these.
- Callers adapt `git.Runner` and `proc.Runner` instead of carrying their own exec wrappers; tests inject fakes.

## Test commands
- `go test ./internal/fsio/... ./internal/git/ ./internal/stamp/ ./internal/pidlive/ ./internal/proc/ ./internal/jsonx/ ./internal/shlex/ ./internal/pystr/ ./internal/strutil/ ./internal/pathglob/ ./internal/bulletfmt/`

## Concerns
- `jsonx.go` and `fsio.go` byte-compat requirements make small changes affect every state file; the testdata fixtures guard them.
- `pidlive` reads `/proc` and `ps` output (`ProcStat`, `StartTicks`), so it is platform-sensitive.
- `pathglob` is shared by several policy packages, so a change to `Match` semantics moves review, scope-drift, lens and knowledge scoping together.
