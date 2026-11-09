---
subsystem: cli-dispatch
summary: the rota command dispatcher, exit-code contract, error translation, per-verb gates and support fence, and the terminal UI (bare-rota palette, --ui screens)
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose

Turns `rota <group> <verb>` into a verb call and owns the global conventions: global flags, the `--json` envelope, the stderr format and the exit codes (`docs/contributing/contract/cli-conventions.md`). Verbs return data or an `*Error` and never print those parts themselves. Also holds the project-root lookup, the per-verb gate and support-fence tables, and the terminal screen kit behind bare `rota` and `--ui`.

Depends on (by map file): knowledge-and-map, config-and-setup (config, initproj, projects, repos, toolver, update, stalebin, version, migrate, hvmigrate, legacystate, rotastate, rotatree), doctor-and-reap, foundations, backlog-items, tracker-issues, round-orchestration (round, roundtick, roundwatch, roundcfg, ...), workers (worker, harness), gate-landing, terminal-hosts (host), hooks-guard (hook) and review-verdicts. `internal/cli` imports most of `internal/`; `exitcode` imports nothing, `tui` and `palette` import almost none.

## Entry points
- cmd/rota/main.go:11 — `main`: `os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))`
- internal/cli/cli.go:250 — `Main`: builds the tree and default deps, calls `run` (cli.go:261, then `runWithContext` at :268)
- internal/cli/cli.go:27 — `Command`: tree node (groups are `Subs`, verbs `Verb`); `RunFunc` at :54, `Result` at :59, `Ctx` at :68
- internal/cli/tree.go:14 — `Tree`: the whole command tree
- internal/cli/tree_gates.go:81 — `fenceDecls`: per-verb support-fence class (refused, fenced inside the verb, allowed); `applyGates` at :14
- internal/cli/root.go:17 — `findRoot`: the one project-root lookup in cli (masking on or off)
- internal/cli/deps.go:48 — `Deps`: the injectable seams (clock, env, git, host, forge) verbs reach through `Ctx`
- internal/exitcode/exitcode.go:12 — exit-code constants (0, 1, 2, 3, 4, 5, 6, 70, 71); `Error` at :40
- internal/exitmap/exitmap.go:93 — `Translate`: domain error to exit-coded error, per `Options` (`Class` bit flags at :20, config classes in `exitmap/config.go`)
- internal/palette/palette.go:139 — `State.Update`: pure key handling for the bare-`rota` menu

## Key files / dirs
- `cmd/rota/` — binary entry; otherwise tests (`frozen_test.go`, `scenarios_*_test.go`, `testdata/frozen`)
- `internal/cli/` — one file group per verb family (`round_*.go`, `worker_*.go`, `knowledge*.go`, `item*.go`, `doctor*.go`, `migrate*.go`, ...); `cli.go` (dispatch), `root.go`, `tree.go`, `tree_gates.go`, `gates.go`, `deps*.go` (seams), `hook_identity.go`, `refusal.go`, `palette.go` (wires `palette` to verbs), `ui.go` and `view_*.go` (`--ui` screens), `testdata/`
- `internal/exitcode/` — exit-code table and the one exit-coded `Error`/`Refusal`; imports nothing from rota
- `internal/exitmap/` — `exitmap.go` (`Translate`: backlog sentinels, `tracker.Error`, busy lock), `config.go` (config usage and internal classes)
- `internal/palette/` — `palette.go` (State, Scope), `render.go`, `run.go` (`Run`, `RunTerminal`, `RunDefault`)
- `internal/tui/` — `doc.go` (model contract), `run.go`, `keys.go`, `list.go`, `detail.go`, `field.go`, `confirm.go`, `layout.go`, `style.go`, `term.go`, `testdata/`

## Stack
Go 1.22, standard library only (`flag`, no cobra). `tui` and `palette` are stdlib only; raw mode goes through `stty`.

## Conventions specific here
- Verb shape: `RunFunc func(c *Ctx, args []string) (Result, error)`; `Result.Data` fills the `--json` envelope, `Result.Text` is the plain output.
- Invariants: verbs never print the envelope, stderr line or exit code themselves; `exitcode` imports nothing from rota (so cli, artifact and worker share it); `tui` Update and Render are pure, side effects go in a `Cmd`; `palette` core is pure and terminal work lives in `run.go` behind a `Config` tests fill with fakes.
- Every verb is listed once in `fenceDecls` under its own path; a path in two support-fence classes panics.
- Project root comes from `findRoot`; masked (a stray `.rota/` in a registered sub-repo) and missing roots are different errors (`rotatree.ErrMasked`, `ErrNoRoot`).
- Verb families differ in which domain errors `exitmap.Translate` recognises (`Class` bit flags in `Options`).
- Tests isolate from the real machine with `isolate_test.go` / `testiso` (blank import) so global config loads as absent.
- Frames are compared to goldens in `internal/tui/testdata` via `Strip(Render(...))`.

## Test commands
- `go test ./cmd/rota/ ./internal/cli/ ./internal/exitcode/ ./internal/exitmap/ ./internal/palette/ ./internal/tui/`
- `go vet ./...`

## Concerns
- `internal/cli` is the largest package (about 370 files); adding a verb touches `tree.go`, its entry in `fenceDecls`, plus its own file group.
- `internal/cli/doctor.go`, `reap.go`, `migrate*.go`, `hook.go` are thin verbs over packages mapped in other files; the logic is not here.
