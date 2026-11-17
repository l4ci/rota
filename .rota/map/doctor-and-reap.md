---
subsystem: doctor-and-reap
summary: read-only preflight checks (rota doctor) and fail-closed cleanup of what a round left behind (rota reap)
touched: 2026-11-17
last_mapped_commit: feae867b58d0ea7ed33556ef145f85dc41dc03e8
---

## Purpose

`doctor` runs one read-only check per thing a parallel round depends on (git, jq, the dispatch host, the forge CLI, worker accounts, herdr's agent integration, installed skills, disk, labels, decisions, external knowledge) and reports pass/fail/skip/warn. `reap` finds worktrees, branches, tabs, leases, skill caches and project temp entries that nothing live owns and, on request, removes only what is provably safe.

Depends on (by map file): config-and-setup (config, rotatree, rotastate, initproj, stalebin), foundations (fsio, git, jsonx, pidlive, proc, strutil, stamp), knowledge-and-map (knowledge), skills-packaging (skills), cli-dispatch (exitcode, reap only). Also imports workers (`harness`, `worker`, `usagelimit`), hooks-guard (`hook`), terminal-hosts (`host`), tracker-issues (`labels`, `tracker`), backlog-items (`backlog`), round-orchestration (`round`, `roundlease`, `roundcfg`) and `repoconfig` (below). The verbs live in cli-dispatch (`internal/cli/doctor*.go`, `reap.go`).

## Entry points
- internal/doctor/doctor.go:270 — `Run`: all checks, returns a `Report`
- internal/doctor/doctor.go:75 — `Input`: working dir, config-derived settings and remote scan data the checks read (`Exec` and `Look` are injected by the caller)
- internal/doctor/doctor.go:40 — `Check`: one report line (Name, Status, Detail, Hint)
- internal/reap/reap.go:58 — `Kinds`: worktree, branch, tab, lease, skill cache, tmp
- internal/reap/reap.go:251 — `Find`: list candidates for the given kinds
- internal/reap/reap.go:502 — `Apply`: remove the provably safe candidates
- internal/reap/census.go:43 — `TakeCensus`: read-only tally of leftovers per class, which `doctor` reports (`Census` at :22)
- internal/repoconfig/repoconfig.go:102 — `Record`: baseline of per-key digests of repo-scope git config; `Check` at :129 names the keys that moved
- internal/cli/doctor.go:36 — `doctorCommand`: the `rota doctor` verb (`runDoctor` at :43)
- internal/cli/reap.go:33 — `reapCommand`: the `rota reap` verb

## Key files / dirs
- `internal/doctor/` — `doctor.go` (`Input`, `Report`, `Run`, `checkTable`), `runner` and the checks split by theme: `toolchain.go` (git, jq, host), `project.go` (config dir, gitignore, stale claims, state dir), `tracker.go` (connection, forge, accounts), `hooks.go` (hook, statusline, stop hook), `skills.go`, `codex.go` (Codex homes and kinds), `disk.go` (`Disk`, repo-config drift), `decisions.go`, `labels.go`, `external_knowledge.go`, `ports.go` (`SlotBlock`), `remote.go` (`RemoteScanner`)
- `internal/reap/` — `reap.go` (Candidate, HostOps, LeaseOps, Input, Result, `Find`, `Apply`), `ownership.go`, `tmp.go` (`ListTmp`, `StaleTmp`, `findTmp`, `removeTmp`), `procuse.go` (`ReadProcUses`: what live processes hold by path), `census.go`
- `internal/repoconfig/` — `repoconfig.go` only (`Record`, `Check`, `Drift`); also called by `rota round start`
- `internal/cli/doctor.go`, `doctor_disk.go`, `reap.go` — verb side

## Stack
Go 1.22, standard library. No direct `os/exec`: `doctor` takes `Exec`/`Look`, `reap` takes injected `Git` and `HostOps`/`LeaseOps`.

## Conventions specific here
- Invariants (doctor): read-only; a missing tool is a failed check, never an error; every `Fail` carries a `Hint`; `Warn` does not fail the report.
- Host-specific checks branch on `host.CapabilitiesForKind(...)` (a host's `Diagnose` and `Harness.HerdrIntegration`) rather than on the host name.
- Invariants (reap, fail-closed): the live set is the round's status rows plus the host's agents matched by cwd, never the registry alone; a running agent is never killed; a candidate holding work is listed with `Held` and never deleted; registered slots, `park/*` worktrees and branches, the base branch and any branch checked out in a worktree are never candidates.
- Tab candidates need proof no agent is under them; what a host cannot prove is not listed. Tmp candidates (entries under `<main checkout>/.rota/tmp`, only when `rotastate.Owned`) are held unless the process table (`ReadProcUses`) is readable and shows no live process holding them.
- `doctor` counts leftovers through `reap.TakeCensus`, so doctor and reap apply one rule for what is left behind.
- Tests fake git, the host and the lease; `isolate_test.go` keeps config global-free.

## Test commands
- `go test ./internal/doctor/ ./internal/reap/`
- `go test ./internal/cli/ -run 'Doctor|Reap'`

## Concerns
- The checks hang off one `runner` type across the `internal/doctor/*.go` files (`doctor.go` is about 295 lines); the table of checks is `checkTable` in `doctor.go`.
- `reap.go` is about 635 lines covering the worktree, branch, tab, lease and skill-cache kinds (ownership proofs in `ownership.go`, tmp in `tmp.go`).
