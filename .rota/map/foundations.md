---
subsystem: foundations
summary: leaf primitives every other package builds on: atomic files, locks and line stores, git and process execution, JSON, timestamps, path globs, bullet lines, liveness and small string helpers
touched: 2026-11-17
last_mapped_commit: feae867b58d0ea7ed33556ef145f85dc41dc03e8
---

## Purpose

The lowest layer of `internal/`. Each package is the one place for its job: writing state files atomically under a lock (and appending or rewriting line stores), running git, running any external binary, encoding JSON, formatting timestamps, matching configured paths, formatting a knowledge bullet, deciding whether a pid is alive, ordering release versions, naming host-session env vars, reading a git remote URL, and the Python-compatible string behavior some ports must keep.

Depends on: each other only. `git` imports `proc`; `pidlive` imports `proc`, `fsio` and `jsonx`; `fsio` imports `jsonx` and `pystr`; `pystr` imports `stamp`; `strutil` imports `pystr`; `bulletfmt` imports `stamp`. `stamp`, `shlex`, `proc`, `jsonx`, `pathglob`, `semver`, `hostenv` and `remoteurl` import no rota package. Used by every other subsystem.

## Entry points
- internal/fsio/fsio.go:67 — `WriteFileAtomic`; `WriteJSONAtomic` at :171, `Locked` (sidecar advisory lock) at :219, `UpdateJSON` at :293
- internal/fsio/lines.go:14 — `AppendLine`; `ReadLines` at :100 (`Lenient`/`Strict` policy) and `RewriteLines` at :123; `ReadJSON[T]` in readjson.go:15
- internal/git/git.go:50 — `Runner`; `Exec` at :53 runs git, `Repo` (git.go:38) wraps a directory
- internal/proc/proc.go:104 — `Run`: the one external-binary runner (`Cmd` at :51, `Runner` at :101)
- internal/jsonx/jsonx.go:65 — `Decode` (key order preserved); `Marshal` at :131 matches Python `json.dumps(indent=2)`; `Obj` at :429 and `Strings` at :439 build objects
- internal/stamp/stamp.go:18 — `Format`: UTC, second precision, trailing `Z`; `Parse` at :21; `Date` at :29 and `ParseDate` at :34 for `DateLayout` dates
- internal/pathglob/pathglob.go:54 — `MatchEntry(Flavour, entry, file)`: entry equal to, a directory above, or a glob of the file, read `Recursive` (as `Match`) or `SingleLevel`; `MatchUnder` at :71, `Match` at :84 (`**` as a segment)
- internal/bulletfmt/bulletfmt.go:56 — `Title`: bold title of a `- **Title** — body` bullet; `Render` at :66
- internal/pidlive/pidlive.go:71 — `Alive`: signal 0 succeeds and the process is not a zombie
- internal/shlex/shlex.go:16 — `Split`: Python `shlex.split` POSIX mode

## Key files / dirs
- `internal/fsio/` — `fsio.go` (atomic writes, `Locked`, markers, `UpdateJSON`), `lines.go`, `jsonl.go` (`AppendJSONL`, `ReadJSONL`), `readjson.go`, `temp.go`, `globdir.go`, `home.go` (`ExpandHome`), `fsiotest/`, `testdata/`
- `internal/git/` — `git.go` (`Repo`, `Base`, `CurrentBranch`, `Dirty`, `CommonDirVia`, `CommitsSince`, `WorktreePath`), `diff.go` (`ChangedPaths`, `ChangedLines`, `RemoteBase`), `worktree.go` (`ParseWorktrees`), `ancestry.go` (`IsAncestorWith`, `AncestryError`)
- `internal/proc/` — `proc.go` (`DefaultTimeout`, `NoTimeout`, `WaitDelay`), `duplex.go`
- `internal/jsonx/` — `jsonx.go` ordered `Object`, `Decode`, `Marshal`, `MarshalCompact`, `PyFloat`; `testdata/`
- `internal/pathglob/` — `pathglob.go` only (`Clean`, `Match`, `MatchEntry` with its `Flavour`, `MatchUnder`, `Valid`); callers: knowledge, change, config, overlap, gate, reviewpolicy, round
- `internal/bulletfmt/` — `bulletfmt.go` only (`Title`, `Render`, `HasTitle`, `Continues`, `End`); callers: knowledge, extknowledge (belongs with knowledge-and-map by use)
- `internal/pidlive/` — `pidlive.go` (`Source`, `Real`, `Alive`, `Running`, `StartTicks`, `Tree`, `KillTree`), `marker.go` (pid-stamped marker `Codec`, `Stamp`, `Marker`), `listeners.go` (`Listener`, `Real.Listeners`)
- `internal/semver/` — `semver.go` only: `Lenient` never rejects, `Strict` accepts only a three-part core; `Compare`, `CompareLenient`, `Before`; callers: update, release, config
- `internal/hostenv/` — `hostenv.go` (`Is`, `IsKV`: which env vars name a host session; `gen/` prints the prefix list to `test/lib/host-env-prefixes`); callers: worker, testiso
- `internal/remoteurl/` — `remoteurl.go` (`Parse`: host, owner, name of a git remote URL); callers: tracker, cli, bugreport
- `internal/stamp/`, `internal/shlex/`, `internal/pystr/` (with `testdata/`), `internal/strutil/`

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
- `pidlive` reads `/proc` and `ps` output (`StartTicks`, `parseProcStat`), so it is platform-sensitive.
- `pathglob` is shared by several policy packages, so a change to `Match` semantics moves review, scope-drift, lens and knowledge scoping together.
