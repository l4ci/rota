---
subsystem: config-and-setup
summary: Layered config, project seeding and registry, on-disk layout paths, version and update checks, and the one-shot migrations
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose

Everything a project needs to exist and be configured: the config schema and its layered load, `rota init` seeding of `.rota/`, the machine-wide project registry, the umbrella sub-repo registry, the single source of truth for in-tree and per-checkout paths, build and update reporting, and the migrations that move older layouts forward.

Depends on (by map file): foundations (fsio, git, jsonx, proc, pystr, strutil, stamp), knowledge-and-map (knowledge, mapqa, frontmatter), skills-packaging (skills, used by hvmigrate). Also imports `golden`, `gopath`, `labels`, `itembody`, `overlap`, `milestone`, `section`, `blockedby`, `hook`, `tracker`, `worker`, `connections`, `roundcfg`, `backlog` (not in these seven maps). Used by every other subsystem.

## Entry points
- internal/config/config.go:24 — `Load`: schema default < global < project `config.json` < `config.local.json`
- internal/config/keys.go:114 — `Key`: schema row (type, scope, default); `ScopeOf` at :324
- internal/initproj/init.go:51 — `Init`: seed `.rota/` in a directory
- internal/projects/projects.go:183 — `Register`: add a project to the machine-wide registry
- internal/rotatree/rotatree.go:46 — `Dir`/`File`: the in-tree `.rota/` layout
- internal/rotastate/rotastate.go:26 — `CommonDir`: git common dir; owns `<git-common-dir>/rota/`
- internal/migrate/migrate.go:109 — `Run`: backlog-file to tracker issues migration
- internal/hvmigrate/hv.go:669 — `RunHv`: the hv-era to rota project migration

## Key files / dirs
- `internal/config/` — `config.go` (layering), `global.go` (machine-wide layer), `local.go`, `keys.go` and `schema.go` (typed schema), `settings.go`, `autonomy.go`, `labels.go`, `lenses.go`, `reviewdepth.go`, `fill.go`, `typed.go`, `migrate_*.go` (config key migrations), `refpage.go` + `reference.tmpl.md`, `genref/main.go` (writes the generated config reference), `testdata/`, `divergent/`
- `internal/initproj/` — `init.go` (Init, `ErrSeed`), `seed.go`, `blocks.go`, `gitignore.go`, `umbrella.go`, `check.go` (`Check`, preflight), `testdata/golden`
- `internal/projects/` — `projects.go` (registry in `$XDG_STATE_HOME/rota`, `Register`, `List`, `Cleanup`, `Remove`), `screen.go`
- `internal/repos/` — `repos.go` (`Load`, `Paths`, `Realpath` for `.rota/repos.json`), `edit.go`, `umbrella.go`
- `internal/rotatree/` — in-tree path helpers (`Config`, `Workers`, `Verdicts`, `Backlog`, `Decisions`, ...), `walk.go`
- `internal/rotastate/` — per-checkout runtime dir (`File`, `SessionDir`, `CodexDir`, `OpencodeDir`, `MainCheckout`)
- `internal/toolver/` — `Detect`, `Newer`: tool versions from manifests, reads files only
- `internal/update/` — `Check`, `Compare`: install method, latest release; never runs the update
- `internal/stalebin/` — `Check`: installed binary behind the source checkout
- `internal/version/` — `Get`: ldflags `Version`/`Commit`/`Date`, else build info
- `internal/migrate/` — `migrate.go`, `labels.go`, `milestones.go`, `plan.go`: file backlog to issues, relabel, milestone bodies
- `internal/hvmigrate/` — one-shot hv-to-rota project migration (directory name `hvmigrate`, Go package name `migrate`)
- `internal/legacystate/` — `LegacyState`, `FindState`: probe for the hv-era state folder
- CLI verbs for this subsystem live in `internal/cli/{config,init,projects,migrate*,setup}.go`

## Stack
Go 1.22, standard library; JSON files read and written through `jsonx` and `fsio`.

## Conventions specific here
- Invariants: `config.Load` treats a missing or unparseable file as absent, nested objects merge by key, any other override replaces the base; keys marked `ProjectOwned` (version stamp, base branch, umbrella switch) are never layered from global.
- Packages ask `rotatree` or `rotastate` for a path instead of joining `".rota"` themselves.
- `rotastate` resolves the git common dir symlink-safe so every worktree and spelling of a path agree.
- A corrupt global config warns once per process (`WarnWriter`).
- `legacystate` is the one live dependency on the old layout and names the old spelling on purpose; `hvmigrate` can be deleted without touching it.
- `update` reports only; it never runs the update. `toolver` never runs a tool.
- Seeding failures wrap `ErrSeed` (cli maps to exit 70); migration refusals (`ErrNothingToMigrate`, `ErrUmbrellaMigrate`, `ErrBadMap`) map to exits 3 and 4.
- `migrate.Run` without `Apply` only reports; the tracker adapter is built lazily and never in a preview.

## Test commands
- `go test ./internal/config/... ./internal/initproj/ ./internal/projects/ ./internal/repos/ ./internal/toolver/ ./internal/update/ ./internal/stalebin/ ./internal/version/ ./internal/migrate/ ./internal/hvmigrate/ ./internal/legacystate/ ./internal/rotastate/ ./internal/rotatree/`
- Regenerate the config reference page: `go run ./internal/config/genref <output file>` (`-` prints to stdout)

## Concerns
- `internal/migrate` has no package doc comment; `internal/hvmigrate` declares `package migrate`, the same name as `internal/migrate`.
- `config` imports `projects` and `itembody`; `projects` imports `repos`, so config sits above the registry packages in the import graph.
