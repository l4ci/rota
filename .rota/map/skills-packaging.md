---
subsystem: skills-packaging
summary: the rota-* skill sources, their embedding in the binary, install into Claude Code and Codex, the plugin manifests, install.sh and the docs generators
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose

Holds the skills (markdown the agent reads), packs them into the binary with `go:embed`, installs them as self-contained directories for Claude Code and Codex, ships the plugin manifests and the binary installer, and generates the skill lists in the docs from `docs/skills/*.md`.

Depends on (by map file): foundations (fsio), cli-dispatch (verbs in `internal/cli/skills.go`), backlog-items (`blockedby`, imported by `internal/skills`), test-infra (`test/validate-skills.py`, `test/doclint.sh` check the sources). Used by doctor-and-reap (installed-skills check) and config-and-setup (`hvmigrate`).

## Entry points
- embed.go:14 — `FS`: the embedded skills, Codex `agents/*.yaml` and shared references
- internal/skills/set.go:37 — `Embedded`: the embedded set; `Load` at :59
- internal/skills/install.go:254 — `Set.Install`; `Update` at :272, `Uninstall` at :493, `Status` at :581
- internal/skills/get.go:123 — `Set.Serve`: one skill or reference by name (read side)
- internal/cli/skills.go:22 — `skillsCommands`: the `rota skills` verbs
- scripts/skills-docs.py:293 — `main`: regenerate the marked blocks from `docs/skills/*.md` (`--check` writes nothing)
- scripts/site-sync.py:309 — `main`: generate the site's docs pages, nav and skill data
- scripts/codex-plugin.sh:1 — builds `<out>/plugins/rota` with references copied as real files

## Key files / dirs
- `skills/rota-*/` — 20 skill folders, each `SKILL.md` plus detail files, an optional `agents/*.yaml` (Codex invocation policy) and a `references` symlink to `../references`
- `skills/references/` — 37 shared reference files plus `README.md` (index); authoring rules in `authoring-conventions.md`, worker rules in `worker-contract.md`
- `internal/skills/` — `set.go` (Set, digest), `get.go` (Serve, stubs), `install.go` (roots, manifest, install/update/uninstall/status), `extract.go` (cache dir), `links.go`, `plugin.go` (Claude Code plugin roots), tests
- `embed.go` — the only Go package at the module root (package `rota`), because `go:embed` cannot reach a parent directory
- `plugin/skills/rota-install/SKILL.md` — the Claude Code plugin's own skill; `.claude-plugin/{plugin,marketplace}.json`
- `plugins/rota/.codex-plugin/plugin.json` and `.agents/plugins/marketplace.json` — Codex plugin manifest and marketplace
- `install.sh` — verifies minisign signature and sha256, installs the release binary
- `scripts/` — `skills-docs.py`, `site-sync.py`, `fmlite.py` (YAML-subset front matter reader), `codex-plugin.sh`
- `docs/skills/*.md` — front matter is the single source for skill lanes, summaries and chains; `docs/skills/README.md` holds chains

## Stack
Go 1.22 (`embed`, `io/fs`); Python 3 standard library only for scripts (no PyYAML: CI has none); POSIX `sh` for `install.sh`, bash for `codex-plugin.sh`.

## Conventions specific here
- Invariants: each installed skill is a self-contained directory (its markdown plus the references it cites) so `references/x.md` resolves against the skill's own directory; a manifest per root records what rota wrote and rota touches nothing outside it.
- Codex drops symlinks when caching a plugin, so `codex-plugin.sh` copies references as real files; release publishes the result to the `codex` branch.
- Docs lists in other files live between `<!-- generated:skills:<name> begin/end -->` markers; edit `docs/skills/*.md`, then run `python3 scripts/skills-docs.py`. Front matter must stay inside the `fmlite` subset (no comments, tabs, anchors, single quotes).
- `site-sync.py` output is generated; `docs/contributing/` is left out; `install.md` is linked from GitHub, `llms.txt` is published verbatim.
- New skill rules go in `skills/references/authoring-conventions.md`, not inline.
- Install modes are `full` and `stub`; an edited or unmanaged file is reported (`Edited`, `Unmanaged`), not overwritten silently.

## Test commands
- `go test ./internal/skills/ .` (root package tests: `plugin_test.go`, `skill_alignment_test.go`, `skill_autonomy_test.go`, `codex_plugin_test.go`, `plugin_hook_test.go`)
- `python3 test/validate-skills.py`
- `bash test/doclint.sh` (runs `skills-docs.py --check` among its checks)
- `python3 scripts/skills-docs.py --check`

## Concerns
- The `references` symlinks under each `skills/rota-*/` break on platforms that drop symlinks; the Codex build copies them for that reason.
- Skill prose is checked by regex rules (`PROSE_RULES` in `test/validate-skills.py`), so wording changes can fail the validator.
