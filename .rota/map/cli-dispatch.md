---
subsystem: cli-dispatch
summary: The rota command dispatcher, exit-code contract, error translation and the terminal UI (bare-rota palette, --ui screens)
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose

Turns `rota <group> <verb>` into a verb call and owns the global conventions: global flags, the `--json` envelope, the stderr format and the exit codes (`docs/contributing/contract/cli-conventions.md`). Verbs return data or an `*Error` and never print those parts themselves. Also holds the terminal screen kit behind bare `rota` and `--ui`.

Depends on (by map file): knowledge-and-map (knowledge, extknowledge, mapqa, stale), config-and-setup (config, initproj, projects, repos, toolver, update, stalebin, version, migrate, hvmigrate, legacystate, rotastate, rotatree), doctor-and-reap, foundations (fsio, git, jsonx, proc, pystr, shlex, stamp, strutil), plus the domain packages that are not in these seven maps (backlog, tracker, round*, worker, ship, gate, host, ...). `internal/cli` imports about 60 internal packages; `exitcode` and `tui`/`palette` import almost none.

## Entry points
- cmd/rota/main.go:12 — `main`: `os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))`
- internal/cli/cli.go:247 — `Main`: builds the tree and default deps, calls `run`
- internal/cli/cli.go:258 — `run`: global flags, verb lookup, `--json`/`--ui`, exit code
- internal/cli/tree.go:14 — `Tree`: the whole command tree (groups are `Command.Subs`, verbs `Command.Verb`)
- internal/cli/deps.go:45 — `Deps`: the injectable seams (clock, env, git, host, forge) verbs reach through `Ctx`
- internal/exitcode/exitcode.go:12 — exit-code constants (0, 1, 2, 3, 4, 5, 6, 70, 71) and `Error` at :40
- internal/exitmap/exitmap.go:76 — `Translate`: domain error to exit-coded error, per `Options`
- internal/palette/palette.go:139 — `State.Update`: pure key handling for the bare-`rota` menu

## Key files / dirs
- `cmd/rota/` — binary entry; tests only otherwise (`frozen_test.go`, `scenarios_*_test.go`, `testdata/frozen`)
- `internal/cli/` — one file group per verb family (`round_*.go`, `worker_*.go`, `knowledge*.go`, `item*.go`, `doctor*.go`, `migrate*.go`, ...); `cli.go` (dispatch), `deps*.go` (seams), `refusal.go`, `palette.go` (wires `palette` to verbs), `view_*.go` (`--ui` screens), `testdata/`
- `internal/exitcode/` — exit-code table and the one exit-coded `Error`/`Refusal`; imports nothing from rota
- `internal/exitmap/` — `Translate`: backlog sentinels, `tracker.Error`, busy lock to exit codes
- `internal/palette/` — `palette.go` (State, Scope), `render.go`, `run.go` (`Run`, `RunTerminal`, `RunDefault`)
- `internal/tui/` — `doc.go` (model contract), `run.go`, `keys.go`, `list.go`, `detail.go`, `field.go`, `confirm.go`, `layout.go`, `style.go`, `term.go`, `testdata/`

## Stack
Go 1.22, standard library only (`flag`, no cobra). `tui` and `palette` are stdlib only; raw mode goes through `stty`.

## Conventions specific here
- Verb shape: `Verb func(fs *flag.FlagSet) RunFunc`; `RunFunc` returns `(Result, error)`; `Result.Data` fills the `--json` envelope, `Result.Text` is the plain output.
- Invariants: verbs never print the envelope, stderr line or exit code themselves; `exitcode` imports nothing from rota (so cli, artifact and worker share it); `tui` Update and Render are pure, side effects go in a `Cmd`; `palette` core is pure and terminal work lives in `run.go` behind a `Config` tests fill with fakes.
- Verb families differ in which domain errors `exitmap.Translate` recognises (`Class` bit flags in `Options`).
- Tests isolate from the real machine with `isolate_test.go` / `testiso` (blank import) so global config loads as absent.
- Frames are compared to goldens in `internal/tui/testdata` via `Strip(Render(...))`.

## Test commands
- `go test ./cmd/rota/ ./internal/cli/ ./internal/exitcode/ ./internal/exitmap/ ./internal/palette/ ./internal/tui/`
- `go vet ./...`

## Concerns
- `internal/cli` is the largest package (hundreds of files, about 60 internal imports); adding a verb touches `tree.go` plus its own file group.
- `internal/cli/doctor.go`, `reap.go`, `migrate*.go` are thin verbs over packages mapped in other files; the logic is not here.
