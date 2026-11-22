---
subsystem: foundations
summary: leaf primitives every other package builds on: atomic files, locks and line stores, git and process execution, JSON, timestamps, path globs, bullet lines, liveness and small string helpers
touched: 2026-11-22
last_mapped_commit: 9544e238cf1b73e8e121cbe5007c8993b37af7fd
---

## Purpose

The lowest layer of `internal/`. Each package is the one place for its job: writing state files atomically under a lock (and appending or rewriting line stores), running git, running any external binary, encoding JSON, formatting timestamps, matching configured paths, formatting a knowledge bullet, deciding whether a pid is alive, ordering release versions, naming host-session env vars, reading a git remote URL, and the Python-compatible string behavior some ports must keep.

Depends on: each other only. `git` imports `proc`; `pidlive` imports `proc`, `fsio` and `jsonx`; `fsio` imports `jsonx` and `pystr`; `pystr` imports `stamp`; `strutil` imports `pystr`; `bulletfmt` imports `stamp`. `stamp`, `shlex`, `proc`, `jsonx`, `pathglob`, `semver`, `hostenv` and `remoteurl` import no rota package. Used by every other subsystem.

## Entry points
- internal/fsio/atomic.go:19 — `WriteFileAtomic`; `WriteJSONAtomic` at writejson.go:14, `UpdateJSON` at writejson.go:49, `Locked` (sidecar advisory lock) at lock.go:40
- internal/fsio/lines.go:14 — `AppendLine`; `ReadLines` at :100 (`Lenient`/`Strict` policy) and `RewriteLines` at :123; `ReadJSON[T]` in readjson.go:37
- internal/git/git.go:51 — `Runner` (`In` at :59 binds ctx and dir into a `Bound`); `Exec` at :64 runs git, `Via` at :70 adapts a `proc.Runner`, `Repo` (git.go:38) wraps a directory
- internal/proc/proc.go:104 — `Run`: the one external-binary runner (`Cmd` at :51, `Runner` at :101)
- internal/jsonx/jsonx.go:65 — `Decode` (key order preserved); `Marshal` at :131 matches Python `json.dumps(indent=2)`; `Obj` at :429 and `Strings` at :439 build objects
- internal/stamp/stamp.go:18 — `Format`: UTC, second precision, trailing `Z`; `Parse` at :21; `Date` at :34 and `ParseDate` at :39 for `DateLayout` dates
- internal/pathglob/pathglob.go:54 — `MatchEntry(Flavour, entry, file)`: entry equal to, a directory above, or a glob of the file, read `Recursive` (as `Match`) or `SingleLevel`; `MatchUnder` at :71, `Match` at :84 (`**` as a segment)
- internal/bulletfmt/bulletfmt.go:79 — `Title`: bold title of a `- **Title** — body` bullet; `Render` at :118; `ResolveTitle` at :159 (the one title-lookup rule: index, or `ErrNotFound`/`ErrMultiMatch`)
- internal/bulletfmt/marker.go:16 — `CutMarker(line, markers)` with the marker sets `DashOnly`, `DashStar`, `AnyMarker`; `Count` at :26
- internal/pidlive/pidlive.go:71 — `Alive`: signal 0 succeeds and the process is not a zombie
- internal/shlex/shlex.go:16 — `Split`: Python `shlex.split` POSIX mode

## Key files / dirs
- `internal/fsio/` — `atomic.go` (`WriteFileAtomic`), `writejson.go` (`WriteJSONAtomic`, `UpdateJSON`), `lock.go` (`Locked`, sidecar lock), `marker.go`, `lines.go`, `jsonl.go` (`AppendJSONL`, `ReadJSONL`), `readjson.go`, `readtext.go`, `temp.go`, `directory.go` (incl. `GlobDir`: literal dir, base-name pattern), `symlink.go`, `home.go` (`ExpandHome`), `doc.go`, `fsiotest/`, `testdata/`
- `internal/git/` — `git.go` (`Repo`, `Runner`/`Bound`, `Via`, `Base`, `CurrentBranch`, `Dirty`, `DirtyPaths`, `StatusPaths`, `CommonDirVia`, `CommitsSince`, `WorktreePath`), `blame.go` (`BlameLineDates`, `ParseBlameDates`), `diff.go` (`ChangedPaths`, `ChangedLines`, `RemoteBase`), `worktree.go` (`ParseWorktrees`), `ancestry.go` (`IsAncestorWith`, `AncestryError`)
- `internal/proc/` — `proc.go` (`Cmd`, `Result`, `Run`, `DefaultTimeout`, `NoTimeout`, `WaitDelay`), `duplex.go` (`Duplex`)
- `internal/jsonx/` — `jsonx.go` ordered `Object`, `Decode`, `Marshal`, `MarshalCompact`, `PyFloat`; `testdata/`
- `internal/pathglob/` — `pathglob.go` only (`Clean`, `Match`, `MatchEntry` with its `Flavour`, `MatchUnder`, `Valid`); callers: knowledge, change, config, overlap, gate, reviewpolicy, roundcfg
- `internal/bulletfmt/` — `bulletfmt.go` (`Title`, `Render`, `Parse`, `Spans`, `HasTitle`, `End`, `SplitStamp`, `ResolveTitle`), `marker.go` (`CutMarker`, `Count`); callers: knowledge, extknowledge, itembody, milestone, round, cli (belongs with knowledge-and-map by use)
- `internal/pidlive/` — `pidlive.go` (`Source`, `Real`, `Alive`, `Running`, `StartTicks`, `Tree`, `KillTree`), `marker.go` (pid-stamped marker `Codec` via `JSON`/`Text`, `Stamp`, `Marker`), `listeners.go` (`Listener`, `Real.Listeners`)
- `internal/semver/` — `semver.go` only: the lenient rule never rejects (`CompareLenient`), `Strict` accepts only a three-part core; `Compare`, `Before`; callers: update, release, config
- `internal/hostenv/` — `hostenv.go` (`Prefixes` and `ConfigVars`, `Drop`, `Scrub`: which env vars name a host session or point at the real harness config; `gen/` prints the prefix list to `test/lib/host-env-prefixes`); callers: worker, testiso
- `internal/remoteurl/` — `remoteurl.go` (`Parse`: host, owner, name of a git remote URL); callers: tracker, cli, bugreport
- `internal/stamp/`, `internal/shlex/`, `internal/pystr/` (with `testdata/`), `internal/strutil/`

## Stack
Go 1.22, standard library only (`os/exec`, `syscall`, `encoding/json`).

## Conventions specific here
- Invariants: `internal/git` is the one place git is run, so timeout, context and `ErrNoGit` come with it (`TestNoDirectGitExec` in `internal/git/boundary_test.go` fails on direct git starts under `internal` and `cmd`: `exec.Command("git")`, `LookPath("git")`, a `proc.Cmd` or runner call naming git); `proc` is the one place any external binary runs (exit-code mapping, call timeout and pipe wait delay live there).
- Atomic writes and the sidecar lock stay compatible with the earlier Python helpers so both can share state files; `jsonx` output must not diff against theirs.
- `AppendLine` appends under the file lock and fsyncs; `ReadLines` with `Lenient` counts rejected lines as skipped, with `Strict` stops with a `*LineError`.
- `stamp` is the one registry timestamp format; writers call `Format`, readers call `Parse`, which reports a malformed value rather than returning the zero time.
- `pathglob` is the one answer to "does this repo-relative file match this configured path"; callers choose `Match`, `MatchEntry` or `MatchUnder` by what the configured string means.
- `bulletfmt` owns the bullet line shape, the list-marker rule (`CutMarker`), the bullet count and the title lookup; what goes in the stamp and where bullets live stay with each knowledge store.
- `pidlive` is the one rule for "is this pid running"; the host and the orchestrator lease both use it so an unreaped exited holder reads as gone.
- `fsio` also has `LockedCtx`, `AppendLineUnique`, `UpdateJSONStrict`, `WriteFileAtomicMode`; errors callers match on: `ErrLockTimeout`, `ErrNotLanded`, `ErrUnreadable`.
- `pystr` and `shlex` exist because Go's `\s`, `\d`, `\w` are ASCII; ports that must match Python use these.
- Callers adapt `git.Runner` and `proc.Runner` instead of carrying their own exec wrappers; tests inject fakes.

## Test commands
- `go test ./internal/fsio/... ./internal/git/ ./internal/stamp/ ./internal/pidlive/ ./internal/proc/ ./internal/jsonx/ ./internal/shlex/ ./internal/pystr/ ./internal/strutil/ ./internal/pathglob/ ./internal/bulletfmt/`

## Concerns
- `jsonx.go` and the `fsio` writers (`atomic.go`, `writejson.go`, `lock.go`) byte-compat requirements make small changes affect every state file; the testdata fixtures guard them.
- `pidlive` reads `/proc` and `ps` output (`StartTicks`, `parseProcStat`), so it is platform-sensitive.
- `pathglob` is shared by several policy packages, so a change to `Match` semantics moves review, scope-drift, lens and knowledge scoping together.
