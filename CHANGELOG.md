# Changelog

## v0.10.0 — 2026-10-05

Continuous rounds with a sharded merge gate, an easier start, and a large internal cleanup.

### Breaking

- `autonomy.level: loop` is gone. Use `auto`.
- `rota issues list` and `/rota-capture --from-github`/`--from-gitlab` are removed. With `backlog.backend: issues`, the tracker already is the backlog.
- `rota-learn` no longer verifies by default. Pass `--strict` to opt in.

### New

- **Getting started.** Bare `rota` runs `rota setup` (an interactive config walkthrough) in a directory without `.rota/`. Otherwise it launches the orchestrator, in a herdr session when you are outside a multiplexer. `rota projects` lists every rota project on the machine.
- **Continuous rounds.** A slot frees as soon as its PR opens, so workers get the next issue without waiting for the merge. New verbs: `rota round watch` (it wakes the orchestrator on any slot, PR or escalation change), `rota round tick` and `--autopilot` for mechanical assign and gate, `rota worker train` (verify several PRs in one gate), and an `open` round scope.
- **Automatic architecture review.** After `round.architectureEvery` non-refactor issues (default 20), or when the queue runs dry with a slot idle, the round mints a review item and assigns it. Its findings become issues. A review finishes with `ROTA-DONE <slot> issues:#a,#b` instead of a PR. `/rota-refactor` is now this findings-first review.
- **Orchestrator harnesses.** Claude Code, Codex, Hermes and opencode can run the orchestrator.
- **Faster gate.** `test/gate.sh` runs validate, doclint, vet, `go test -race` and four smoke shards at once. It takes about 150 s instead of about 590 s, and only one gate runs per machine. Workers run targeted checks only. The full gate runs once, at merge.
- **Safer merges.** The gate merges the exact SHA it verified, through the tracker adapter (gh and glab). Releases require a `.minisig` for every asset, and `install.sh` verifies minisign signatures.
- **Doctor** warns on low disk. The gate fails when tests leak temp files.

### Fixed

- Dispatch submits a brief left unsent on the prompt line, and only when the agent is idle.
- `round candidates` and `round assign` skip issues that already have an open PR.
- The tmux host detects a booted Claude Code 2.1.289 pane.
- Slots stay usable after `rota migrate issues` runs mid-round.
- Flaky tests are fixed: the fsio concurrent-writers lock budget, the smoke 108 busy pane and the smoke 110 prompt marker.

### Internal

Three architecture reviews ran in this cycle, and every finding is merged. A domain module now owns each of these:
- the item ID grammar
- git and subprocess runners (`internal/git`, `internal/proc`)
- state paths (`rotastate`)
- backlog backend selection
- tracker construction
- the typed worker registry
- the round host resolver
- the ship flow (`internal/ship`)
- migration (`internal/migrate`)

Other changes:
- Verb files and frozen suites are named by domain instead of by port phase.
- Every golden can be regenerated with `-update-golden`.
- The CLI keeps its dependencies on `Ctx` instead of in package globals.
- Skills are leaner: no banners or task-list ceremony, issue-first IDs, a single review pass and one merge owner.

### Stats
155 commits, 634 files changed, +30286 −18950 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.9.0...v0.10.0

## v0.9.0 — 2026-10-04

rota was hv-skills. The CLI moved to a new repo with a fresh history, and what would have been hv-skills 5.0 is rota 0.9.0. Older history stays in hv-skills.

rota moves the mechanics into a Go CLI and adds autonomous rounds: an orchestrator drives parallel workers, each in its own worktree, from GitHub issues.

### Upgrade
Everything is renamed: binary `rota`, skills `/rota-*`, state folder `.rota/`, env vars `ROTA_*`. Install with `curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh` or `brew install l4ci/tap/rota`, then run `rota skills install` to put the skills in place for Claude Code and Codex. `rota init` sets up a project. Projects that still have `.hv/` move over with `rota migrate`. There is no plugin and no `npx` install. Binaries are checked against `checksums.txt` (sha256). That is an integrity check, not proof of who built them; signatures are planned.

### New
- **Autonomous rounds.** `/rota-orchestrate` and `rota round` (start, candidates, assign, wait, status, reconcile, escalate, wind-down) run Claude or Codex workers under herdr or tmux, or as subagents in solo mode, with per-worker model tiers and merge policy.
- **Rules enforced in code.** Manual-gate registry, typed review and QA verdicts, the debug Iron Law, and a verified merge gate (`rota worker gate`).
- **Keepalive.** Orchestrator handoff on a full context, restart in the same pane, and usage-limit handling (sleep to reset or switch account). With `orchestrator.switchOnUsage` on (off by default), the orchestrator moves to another account before it hits the limit.
- **Codex.** Codex workers with a per-slot `CODEX_HOME` (each slot logs in separately), a version check in `rota doctor`, and no tier map required. Skills follow the Agent Skills spec so Codex discovers them.
- **Install.** `install.sh` downloads the release binary, checks it against `checksums.txt` and refuses a mismatch. A Homebrew formula is pushed to `l4ci/homebrew-tap` on each release. The skills ship inside the binary.
- **Release pipeline.** goreleaser builds `rota` for linux and macOS (amd64, arm64) from a version tag into one draft release; `rota release publish` finishes it, and the version bump reaches the branch last. The release workflow refuses a tag that does not match `VERSION`.
- **Helpers are verbs.** Every helper is a `rota` verb with `--json` output and stable exit codes.
- **Skills trimmed.** Capture hands off to work, `/rota-work` with no argument suggests the next item, and init, config, update and migrate are `rota` verbs. `/rota-ship`, `/rota-capture`, `/rota-release` and `/rota-pause` keep only the judgment calls.
- **`rota update` and `rota doctor`.** `update` reports how the binary was installed and prints the matching upgrade command; `doctor` and `rota reap` cover preflight and cleanup for rounds.
