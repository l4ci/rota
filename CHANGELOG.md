# Changelog

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
