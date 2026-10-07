# FAQ

Common questions about rota.

## How is this different from a TODO file or issue tracker?

That's how every workflow starts, and how most of them stay. The places it tends to drift are the ones rota tries to address: commits stop being atomic and one PR ends up touching six unrelated things, you re-discover the same gotcha three sessions in a row because nothing reads it back, and sessions don't survive `/clear` because you lose the live hypothesis when you step away. `/rota-work` enforces atomic per-task commits, `/rota-learn` writes durable gotchas that future runs auto-consult, `/rota-pause` and `/rota-work` carry intent across context resets. If those problems never bite you, stock Claude Code is fine.

## Is `.rota/` tracked by default?

Yes. Backlog (with the default `file` backend; under `issues` it lives on the tracker), knowledge, decisions, plans, designs, milestones, and per-item detail files all travel with the repo so team members share context from the first clone. These paths stay gitignored: `.rota/status.json` (per-developer active work), `.rota/repos.json` (umbrella registry with absolute paths), `.rota/config.local.json` (per-developer config overrides, deep-merged on top of `.rota/config.json`), `.rota/handoff/` (per-developer scratch notes from `/rota-pause`), `.rota/review/` (per-branch review packages from `rota review package`), `.rota/qa-runs/` (bulky timestamped artifacts from `/rota-qa`), `.rota/verdicts.json` (typed review verdicts), `.rota/gate-audit.jsonl` (the log of manual-gate approvals), `.rota/train-cache.json` (the merge train's verdict cache), `.rota/workers.json` (the worker slot registry), and `.rota/**/*.lock` (transient sidecar lockfiles).

> [!TIP]
> If you'd rather keep the whole backlog private (solo development, or experimentation that isn't ready to share), add a blanket `.rota/` line to `.gitignore` before your first commit. The default assumes you want context to travel.

## Can I share `.rota/` with my team?

You already are; sharing is the default. A few things to know: on the file backend, item ID counters in `counters.json` are shared, so coordinating ID numbering matters; `KNOWLEDGE.md` accumulates team learnings; `DECISIONS.md` becomes a team contract. Per-developer settings (autonomy level, model preferences) go in the gitignored `.rota/config.local.json` to avoid stepping on each other.

This works well for small teams. For larger ones, set `backlog.backend` to `issues` so GitHub or GitLab issues hold the backlog and get the tracker's conflict resolution and permissions; see the [issue backend](usage/issue-backend.md).

## What if I'm not using Claude Code?

Codex is supported. `rota skills install` writes the skills to `~/.agents/skills` as well as the Claude Code directory, and Codex can also run as a worker in a round. See [using the skills in Codex](usage/codex-skills.md) and [Codex workers](usage/codex-workers.md). One caveat: skill bodies still name Claude Code tools (`AskUserQuestion`, `Agent`), so a skill may not run end to end in Codex.

The `.rota/` folder, the backlog formats and the `rota` binary are agent-agnostic; you can call `rota` from any shell. Other harnesses are untested.

## Do I need herdr or tmux?

No. Workers in a [parallel round](usage/parallel-rounds.md) get a tab each in herdr or tmux when the orchestrator runs inside one. Without either, `rota round start` picks solo mode and the orchestrator runs each worker as a subagent in its own worktree. With solo mode there is no host to notify you, so questions go on the issue or PR thread. Solo workers share the orchestrator's account and usage limit, and run Claude only, so keep solo rounds to two or three workers. The one-Claude-one-Codex default for a `best-of:2` issue applies only to rounds with a host (herdr or tmux), and only when `round.tiers` configures both.

## How do I update rota when a new release ships?

Run `rota update` (needs `gh`). It detects how you installed rota (Homebrew, the install script, or a dev build) and prints the command, for example `brew update && brew upgrade rota && rota skills update`, or the install script's `curl` line followed by `rota skills update`. It doesn't run the update itself.

`rota skills update` refreshes the installed skills to match the new binary. Nothing else in your projects needs refreshing. Run `rota version --drift` in a project to see whether its stamped version trails the installed one. See [install](install.md#upgrading).

## Does this work with monorepos?

Yes. `.rota/` lives at the root of whatever directory you run `rota init` from. For monorepos you have two reasonable options:

- **One `.rota/` at the monorepo root** for project-wide work and cross-package tracking.
- **One `.rota/` per package or app subdirectory** for scoped backlogs that stay close to the code they track.

`rota` and the managed `CLAUDE.md` blocks resolve relative to the current working directory, so per-package setups work as long as you run rota from inside the package. You can mix both styles in one repo; each `.rota/` is independent.
