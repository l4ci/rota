---
subsystem: doctor-and-reap
summary: Read-only preflight checks (rota doctor) and fail-closed cleanup of what a round left behind (rota reap)
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose

`doctor` runs one read-only check per thing a parallel round depends on (git, jq, the dispatch host, the forge CLI, worker accounts, herdr's agent integration, installed skills, disk, labels, decisions, external knowledge) and reports pass/fail/skip/warn. `reap` finds worktrees, branches, tabs, processes, leases and skill caches that nothing live owns and, on request, removes only what is provably safe.

Depends on (by map file): config-and-setup (config, rotatree, stalebin), foundations (fsio, git, proc, strutil), knowledge-and-map (knowledge), skills-packaging (skills), cli-dispatch (exitcode). Also imports `harness`, `hook`, `host`, `labels`, `tracker`, `worker`, `round`, `roundlease` (not in these seven maps). The verbs live in cli-dispatch (`internal/cli/doctor*.go`, `reap.go`).

## Entry points
- internal/doctor/doctor.go:275 — `Run`: all checks, returns a `Report`
- internal/doctor/doctor.go:89 — `Input`: working dir, config-derived settings and remote scan data the checks read (`Exec` and `Look` are injected by the caller)
- internal/doctor/doctor.go:42 — `Check`: one report line (Name, Status, Detail, Hint)
- internal/reap/reap.go:51 — `Kinds`: worktree, branch, tab, process, lease, skill cache
- internal/reap/reap.go:238 — `Find`: list candidates for the given kinds
- internal/reap/reap.go:591 — `Apply`: remove the provably safe candidates
- internal/cli/doctor.go:39 — `doctorCommand`: the `rota doctor` verb (flags, host wiring)
- internal/cli/reap.go:43 — `reapCommand`: the `rota reap` verb

## Key files / dirs
- `internal/doctor/` — `doctor.go` (checks, `runner`), `codex.go` (Codex homes and kinds), `decisions.go`, `labels.go`, `ports.go` (`ParseSS`, `ParseLsof`, `ParseLsofCwd`), `testdata/`, `orca_test.go`, `stale_test.go`
- `internal/reap/` — `reap.go` only (Candidate, Process, HostOps, LeaseOps, Input, Result)
- `internal/cli/doctor.go`, `doctor_disk.go`, `doctor_labels.go`, `reap.go` — verb side

## Stack
Go 1.22, standard library. No direct `os/exec`: `doctor` takes `Exec`/`Look`, `reap` takes injected `Git` and `HostOps`/`LeaseOps`.

## Conventions specific here
- Invariants (doctor): read-only; a missing tool is a failed check, never an error; every `Fail` carries a `Hint`; `Warn` does not fail the report.
- Invariants (reap, fail-closed): the live set is the round's status rows plus the host's agents matched by cwd, never the registry alone; a running agent is never killed; a candidate holding work is listed with `Held` and never deleted; registered slots, `park/*` worktrees and branches, the base branch and any branch checked out in a worktree are never candidates.
- Tab and process candidates need proof no agent is under them; what a host cannot prove is not listed.
- Tests fake git, the host and the lease; `isolate_test.go` keeps config global-free.

## Test commands
- `go test ./internal/doctor/ ./internal/reap/`
- `go test ./internal/cli/ -run 'Doctor|Reap'`

## Concerns
- `internal/doctor/doctor.go` is about 1090 lines; the check list lives in one `runner` type.
- `reap.go` is about 690 lines in one file covering every kind.
