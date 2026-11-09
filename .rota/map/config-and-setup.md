---
subsystem: config-and-setup
summary: layered config, project seeding and registry, on-disk layout paths, version and update checks, and the one-shot migrations
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose

Everything a project needs to exist and be configured: the config schema and its layered load, the config migrate/reconcile/diagnose steps, `rota init` seeding of `.rota/`, the machine-wide project registry, the umbrella sub-repo registry, the single source of truth for in-tree and per-checkout paths, build and update reporting, and the migrations that move older layouts forward.

Depends on (by map file): foundations (fsio, git, jsonx, proc, pystr, strutil, stamp, pathglob), knowledge-and-map (knowledge, mapqa, frontmatter), skills-packaging (skills, used by hvmigrate). Also imports tracker-issues (`labels`, `tracker`), backlog-items (`milestone`, `section`, `blockedby`, `backlog`), gate-landing (`change` via config), hooks-guard (`hook`, used by hvmigrate), workers (`worker`) and round-orchestration (`roundcfg`, used by migrate). Used by every other subsystem.

## Entry points
- internal/config/config.go:24 — `Load`: schema default < global < project `config.json` < `config.local.json`
- internal/config/keys.go:117 — `Key`: schema row (type, scope, default); `ScopeOf` at :335
- internal/config/orchestrate.go:26 — `Migrate`: round-key, accounts, merges and scope steps (preview or apply); `Reconcile` at :77, `Diagnose` at :115
- internal/initproj/init.go:70 — `Init`: seed `.rota/` in a directory
- internal/projects/projects.go:182 — `Register`: add a project to the machine-wide registry
- internal/rotatree/rotatree.go:62 — `Dir`/`File` (:65): the in-tree `.rota/` layout; `FindRoot` in root.go:34
- internal/rotastate/rotastate.go:25 — `CommonDirVia`: git common dir; owns `<git-common-dir>/rota/`
- internal/migrate/migrate.go:110 — `Run`: backlog-file to tracker issues migration
- internal/hvmigrate/hv.go:643 — `RunHv`: the hv-era to rota project migration

## Key files / dirs
- `internal/config/` — `config.go` (layering), `global.go` (machine-wide layer), `local.go`, `keys.go`, `schema.go`, `types.go`, `typed.go` (typed schema), `scope.go`, `settings.go`, `autonomy.go`, `change.go`, `labels.go`, `lenses.go`, `reviewdepth.go`, `verbs.go`, `fill.go`, `orchestrate.go` (Migrate, Reconcile, Diagnose), `migrate_{round,accounts,merges,scope,source,files}.go` (config key migrations), `refpage.go` + `reference.tmpl.md`, `genref/main.go` (writes the generated config reference), `testdata/`, `divergent/`
- `internal/initproj/` — `init.go` (Init, `ErrSeed`), `seed.go`, `blocks.go`, `gitignore.go`, `umbrella.go`, `check.go` (`Check`, preflight), `testdata/golden`
- `internal/projects/` — `projects.go` (registry in `$XDG_STATE_HOME/rota`, `Register`, `List`, `Cleanup`, `Remove`), `screen.go`
- `internal/repos/` — `repos.go` (`Load`, `Rels`, `Paths`, `Realpath` for `.rota/repos.json`), `edit.go`, `umbrella.go`
- `internal/rotatree/` — `rotatree.go` in-tree path helpers (`Config`, `Workers`, `Status`, ...), `root.go` (`FindRoot`, `AtUmbrellaRoot`, `ErrNoRoot`, `ErrMasked`), `walk.go`
- `internal/rotastate/` — per-checkout runtime dir (`File`, `SessionDir`, `CodexDir`, `OpencodeDir`, `MainCheckout`)
- `internal/toolver/` — `Detect`, `Newer`: tool versions from manifests, reads files only
- `internal/update/` — `Check`, `Compare`: install method, latest release; never runs the update
- `internal/stalebin/` — `Check`: installed binary behind the source checkout
- `internal/version/` — `Get`: ldflags `Version`/`Commit`/`Date`, else build info
- `internal/migrate/` — `migrate.go`, `labels.go`, `milestones.go`, `plan.go`: file backlog to issues, relabel, milestone bodies
- `internal/hvmigrate/` — `hv.go`, `common.go`: one-shot hv-to-rota project migration
- `internal/legacystate/` — `LegacyState`, `FindState`: probe for the hv-era state folder
- CLI verbs for this subsystem live in `internal/cli/{config,config_edit,init,init_umbrella,projects,migrate*,setup*}.go`

## Stack
Go 1.22, standard library; JSON files read and written through `jsonx` and `fsio`.

## Conventions specific here
- Invariants: `config.Load` treats a missing or unparseable file as absent, nested objects merge by key, any other override replaces the base; keys marked `ProjectOwned` (version stamp, base branch, umbrella switch) are never layered from global.
- `config.Migrate` plans every step first on apply, so a refusal in any one step leaves every file untouched; each step has a `*Files` form with an `apply` flag that only reports without it.
- Packages ask `rotatree` or `rotastate` for a path instead of joining `".rota"` themselves.
- `rotastate` resolves the git common dir symlink-safe so every worktree and spelling of a path agree.
- A corrupt global config warns once per process (`WarnWriter`).
- `legacystate` is the one live dependency on the old layout and names the old spelling on purpose; `hvmigrate` can be deleted without touching it.
- `update` reports only; it never runs the update. `toolver` never runs a tool.
- Seeding failures wrap `ErrSeed` (cli maps to exit 70); config errors map through the `exitmap` config classes (usage 2, internal 70); migration refusals (`ErrNothingToMigrate`, `ErrUmbrellaMigrate`, `ErrBadMap`) map to exits 3 and 4.
- `migrate.Run` without `Apply` only reports; the tracker adapter is built lazily and never in a preview.

## Test commands
- `go test ./internal/config/... ./internal/initproj/ ./internal/projects/ ./internal/repos/ ./internal/toolver/ ./internal/update/ ./internal/stalebin/ ./internal/version/ ./internal/migrate/ ./internal/hvmigrate/ ./internal/legacystate/ ./internal/rotastate/ ./internal/rotatree/`
- Regenerate the config reference page: `go run ./internal/config/genref <output file>` (`-` prints to stdout)

## Concerns
- `internal/migrate` has no package doc comment.
- `config` imports `projects`; `projects` imports `repos` and `tui`, so config sits above the registry packages in the import graph.
- `hvmigrate` imports `hook` and `skills`, so the one-shot migration reaches into two live subsystems.
