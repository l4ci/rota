# Changelog

## v0.13.0 — 2026-10-07

Stricter merge gate, best-of-2 rounds, generated subagent definitions, and test tiers you can configure.

## Breaking

- `rota worker gate` and `rota worker train` refuse to merge when `test.full` and `test.e2e` are both empty. Pass `--no-verify` to merge without verification. (#474)
- The gate refuses a round PR whose body has no closing keyword for the slot's issue. Add `Closes #N`, or label a partial slice `partial-slice`. (#477)
- The gate and the train refuse a PR with a recorded FAIL verdict. (#449)

## New

- **Best-of-2 rounds.** An issue labelled `best-of:2` goes to two workers at once, by default one Claude and one Codex. The orchestrator picks the better PR with `rota round pick`, which closes the other. The gate refuses either attempt until the pick is made. (#489)
- **Generated subagent definitions.** `rota agents write` writes `.claude/agents/rota-{explorer,implementer,reasoner}.md` from new `roles.*` config, plus `.codex/agents/*.toml` when Codex is configured. `rota init` runs it, and `rota doctor` reports missing or out-of-date files. (#487)
- **Round setup.** `round.workerKind` sets the project's default worker harness. `rota round assign` reserves smoke section numbers. `work.itemTimeoutMinutes` caps how long one item may run. New slot worktrees run `work.envSetup`. (#472, #475, #439, #450)
- **Gate and train.**
  - `rota worker gate <PR>` gates any open PR, not only round PRs.
  - A stale branch merges when `git merge` is clean, even if both sides changed a file.
  - Gate and train share one land lock per repo.
  - With `test.fullWhere ci` they wait for CI checks.
  - `test.e2e` runs once on a train's merged tree.
  - The train caches passing verdicts.
  - An exclusion ledger lists tests known to be red. (#479, #480, #445, #411, #408, #451, #452)
- **Test tiers and proof.**
  - Test tiers are declared in config.
  - Every tier runs in an isolated environment.
  - `rota proof record` runs a check and records its result.
  - Acceptance criteria get AC ids that only proof can mark as met.
  - `rota worker done` refuses a finished slot that has no `test.fast` proof.
  - `work.tdd` can be turned off. (#379, #441, #420, #442, #440, #407)
- **Planning.** `rota plan check` checks that every criterion maps to a task and every task has a Verify step. Level-4 items get a critic step. A planning-dial rubric says when an item needs more planning, and plans cite the knowledge they rely on. (#458, #457, #453, #455)
- **Doctor.** Warns when the installed `rota` is older than its source checkout. (#473)

## Fixed

- `rota worker gate` names the real reason local main cannot fast-forward, and says when it skipped checking the merged tree. (#483)
- A no-break space after Claude Code's prompt marker no longer breaks parsing of the prompt line, and `rota` refuses to send into a pane that holds a human's draft. (#438, #429)
- `{files}` expansion includes untracked files and diffs against a remote base that is ahead. (#418, #422)
- `rota worker done` runs from a slot worktree. (#456)
- CI: git housekeeping no longer races fixture copies in the scenario tests, and the nightly smoke job installs minisign. (#486)

## Internal

- One mapping from domain errors to exit codes, and one owner for the Claude config folder and skill folders. (#482, #478)
- Assign and transfer share one worker-delivery module, and the item verbs moved into `backlog`. (#425, #430)
- Contract docs carry a verified sha, and doclint guards them against drift. (#447)

## Stats

192 commits, 295 files changed, +14,739 −1,349 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.12.0...v0.13.0

## v0.12.0 — 2026-10-06

Codex workers on your own account, per-issue harness and model labels, leaner skills, and a fix for slot worktrees vanishing mid-round.

### New

- **Pick a worker's harness and model per issue.** Label an issue `harness:claude` or `harness:codex`, and optionally `model:<id>` (file backend: `Harness:` / `Model:` fields). `rota round assign` picks them up; `--kind` and the new `--model` override them. `candidates` and `status` show the choice. `/rota-capture` adds the labels when you name a harness or model.
- **Codex workers use your default Codex account.** No per-slot login. Optional `work.codexAccounts` spreads slots across several Codex homes, like `work.accounts` does for Claude.
- **No Codex version pin.** Preflight checks that `codex --help` lists the flags the launch line uses. `--accept-codex-version` is now a no-op.
- **Config:** `rota config edit` (an interactive editor, also reachable from the bare `rota` palette), a global config that seeds new projects, and `rota projects cleanup` to prune stale project entries.
- **Rounds:**
  - Split layout names the tab `rota` and the pane `orchestrator`, keeps the CLI on top, and is remembered per round.
  - `rota round bounce` caps review bounces.
  - `transfer --tier`.
  - PR bodies gain `## Rulings`, proof evidence and a door/blast-radius line.
  - Briefs carry the item's Out of scope section.
- **Skills:**
  - `/rota-review` runs separate Spec and Standards reviewers.
  - `/rota-learn --retro` turns mistakes into guardrails.
  - `/rota-debug` builds a feedback loop before guessing, and ranks hypotheses.
  - `/rota-work` resumes from `Task:` trailers.
  - `item create --depends-on` orders items.
  - Behavioural evals cover skill triggers and the core skills.

### Changed

- **Skills are shorter and easier to follow.**
  - Each SKILL.md keeps the happy path inline and moves rare cases into per-skill files.
  - Each workflow opens with a copyable step checklist.
  - Duplicated rules across `skills/references/` now have one owner file (-10% words).
- `rota config show` and the editor show the value rota actually uses, including defaults behind a local `null`.
- The `blockedBy` for a Codex launch problem is now `codex flags` (was `codex version`).

### Fixed

- `rota ship pr` deleted the worktree it ran in, so a round slot could vanish mid-round. Ship verbs now never remove slots, the current directory, or a worktree with changes.
- Model ids from labels, fields and `--model` are validated before they reach the shell launch line.
- **Gate:**
  - The lock stays exclusive during stale-lock takeover.
  - The smoke leak guard no longer overwrites concurrent edits.
  - The gate and runner scrub HOME, XDG, SSH and host variables.
- **Rounds:**
  - `worker poll` lets the last sentinel decide a slot's state.
  - `reconcile --apply` parks slots whose PR merged, so `watch` no longer loops on them.
- **Merges:** merge-abort failures are reported with recovery steps instead of claiming a clean abort.
- **Items:**
  - Debug items get one canonical identity for the Iron Law count.
  - Item plans load without a milestone.
  - Removing the last backlog bullet works without a trailing newline.
  - `item rm` recognizes rota-written flat archives.
  - Archive, reopen and remove keep items across failed or concurrent transfers.
- **Other:**
  - GitLab changed-file lists paginate correctly.
  - Skill evals fail on failed model calls.

### Internal

Large refactors with no behaviour change:
- one merge module for branches and PRs;
- typed worker-registry rows;
- a single host-factory seam and clock on Deps;
- config typing centralised;
- one fake for round tests.

## Stats
107 commits, 440 files changed, +16005 −5435 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.11.0...v0.12.0

## v0.11.0 — 2026-10-05

Split and tab layouts for herdr rounds, an interactive palette on bare `rota`, and leaner CI.

### New

- **`rota layout split|tabs`.** Arranges a round's herdr panes: the orchestrator full height on the left, workers in columns two high to its right, or one tab per worker. It only moves live panes, so nothing restarts. Bare `rota layout` shows each project's current arrangement.
- **Palette.** Bare `rota` in a terminal opens a menu with the banner, version, project and round state. Orchestrate is preselected, so `rota` then Enter works as before. Pipes, `--json` and arguments skip it.
- **`rota repo add|rm`** edits the umbrella registry without re-running `rota init umbrella`. (#182)
- **Skills for every account.** `rota skills install|update|status` cover each `work.accounts` config dir as well as the current one; `--current-account` limits a run to the current dir. `rota doctor` flags an account whose skills lag the binary. (#179)
- `/rota-work` runs `/rota-qa run` after a cycle when `qa.afterWork` is on.

### Removed

- `debug.competingHypotheses`: no code ever read it.

### Fixed

- A test flake where git's background housekeeping wrote into a test repo during cleanup. (#184)
- The `/rota-ship --undo` preview no longer calls plan files gitignored.
- Two mermaid diagrams in the docs failed to render.

### Internal

- CI is one cached workflow that runs on main and on ready PRs, cancels superseded runs and times out after 15 minutes. (#183)
- The bracketed item-ID regexes come from the central ID grammar. (#176)
- An audit corrected the user docs against the code and skills, covering both backlog backends.

### Stats
18 commits, 96 files changed, +3925 −457 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.10.1...v0.11.0

## v0.10.1 — 2026-10-05

The orchestrator now starts under a configured account; skills moved under skills/.

### Fixed

- `rota orchestrate` (and bare `rota`) started Claude under the default `~/.claude` and ignored `work.accounts`. It now starts under your `CLAUDE_CONFIG_DIR` if set, else the `work.accounts` entry with the most headroom, else the first one listed. `rota keepalive run` takes `--config-dir`, and `rota orchestrate --dry-run` names the account. (#178)

### Docs

- The first-round guide covers tmux next to herdr, and its permission example allows `glab` as well as `gh`.

### Internal

- Skill folders and `references/` moved under `skills/`. The installed skill layout is unchanged. (#177)
- `.rota/gate-audit.jsonl` is gitignored in this repo.

### Stats
6 commits, 81 files changed, +255 −97 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.10.0...v0.10.1

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
