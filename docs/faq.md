# FAQ

## How is this different from a TODO file or issue tracker?

A TODO file or tracker is where most workflows start and stay. Three things tend to go wrong: commits stop being atomic and one PR touches six unrelated things, you re-discover the same gotcha three sessions in a row because nothing reads it back, and a session doesn't survive `/clear` because the live hypothesis is gone. `/rota-work` enforces atomic per-task commits, `/rota-learn` writes durable gotchas that future runs consult automatically, and `/rota-pause` with `/rota-work` carry intent across context resets. If those problems never bite you, stock Claude Code is fine.

## What does rota cost?

rota is MIT licensed and free. The agents run on your own Claude or Codex plan. A [parallel round](usage/parallel-rounds.md) spends quota roughly once per worker, so three workers use it about three times as fast as one session. `rota round summary` (also printed at wind-down) shows what a round cost per issue, per slot and per account.

## Do I need GitHub?

No. The backlog lives in `.rota/BACKLOG.md` by default. Set `issues.source` to use GitHub, GitLab, Jira, Linear or itsaplan issues instead: see the [issue backend](usage/issue-backend.md). A project with an `origin` remote needs `gh` or `glab` logged in even on the file backend, because workers open PRs and the gate merges through the forge. With no remote (`forge.provider: local`) there are no PRs and finished work merges directly.

## Can a worker merge broken code?

Workers never merge. They open a PR, and the orchestrator merges through the gate (`rota worker gate`). The gate runs your full test suite (`test.full`) on a scratch merge of the PR and the current base, and merges only on a pass. It refuses to merge while `test.full` is empty, unless you pass `--no-verify`. To approve every merge yourself, set `ship.mergeApproval`: see [merge approval](usage/parallel-rounds.md#merge-approval).

## What happens when I hit a usage limit?

`rota limit watch` notices the limit, waits for the reset and types a resume prompt into the pane. `rota orchestrate` runs it for you under `rota keepalive run`. With several accounts in `round.accounts`, a limited worker's issue moves to an idle slot on an account with headroom instead of waiting (`limits.mode: switch`, the default); with no such slot it waits. See [usage limits](usage/unattended-rounds.md#-usage-limits).

## Can a round run overnight?

Yes, once a supervised round works. Install the handoff hooks (`rota hook install`) and start the orchestrator with `rota orchestrate`, which runs it under `rota keepalive run`. The orchestrator then hands off before its context fills, restarts from the handoff, and waits out usage limits. See [unattended rounds](usage/unattended-rounds.md).

## Do I need herdr or tmux?

No. Workers in a [parallel round](usage/parallel-rounds.md) get a tab each in herdr or tmux when the orchestrator runs inside one. Without either, `rota round start` picks solo mode and the orchestrator runs each worker as a subagent in its own worktree. Solo mode has no host to notify you, so questions go on the issue or PR thread. Solo workers share the orchestrator's account and usage limit, and run Claude only, so keep solo rounds to two or three workers.

## What if I'm not using Claude Code?

Codex is supported. `rota skills install` writes the skills to `~/.agents/skills` as well as the Claude Code directory. In a round, Codex and [OpenCode](usage/opencode-workers.md) can run as workers beside Claude Code, and the orchestrator itself can run in Claude Code, Codex, Hermes or OpenCode ([orchestrator harnesses](usage/orchestrator-harnesses.md)). A few skill instructions are still labelled Claude-only; [skills in Codex](usage/codex-skills.md) lists them.

The `.rota/` folder, the backlog formats and the `rota` binary are agent-agnostic, so you can call `rota` from any shell.

## Does rota work on Windows?

Through WSL2. There is no native Windows build. Install rota, git, `gh` and your agent inside the WSL2 distro and keep the repo on the Linux filesystem. See [Windows](install.md#windows-wsl2).

## Is `.rota/` tracked by default?

Yes. The backlog (on the default file backend), knowledge, decisions, plans, designs, milestones and per-item detail files travel with the repo, so the team shares context from the first clone. Per-developer and transient files stay gitignored: active work, local config overrides, handoff notes, review packages, QA runs, the round's registry, ledger and receipts, proof rows and lockfiles. [The `.rota/` folder](reference/rota-folder.md) lists every path and whether it is tracked.

> [!TIP]
> To keep the whole backlog private (solo development, or experiments not ready to share), add a blanket `.rota/` line to `.gitignore` before your first commit.

## Can I share `.rota/` with my team?

Sharing is the default. On the file backend, item ID counters in `counters.json` are shared, so ID numbering needs coordinating; `KNOWLEDGE.md` accumulates team learnings; `DECISIONS.md` becomes a team contract. Per-developer settings (autonomy level, model preferences) go in the gitignored `.rota/config.local.json` to avoid stepping on each other.

This suits small teams. For larger ones, set `issues.source` to `github` or `gitlab` so GitHub or GitLab issues hold the backlog and get the tracker's conflict resolution and permissions; see the [issue backend](usage/issue-backend.md).

## Does this work with monorepos, or several repos?

Yes. `.rota/` lives at the root of the directory you run `rota init` from. In a monorepo, two options:

- **One `.rota/` at the monorepo root** for project-wide work and cross-package tracking.
- **One `.rota/` per package or app subdirectory** for scoped backlogs that stay close to the code they track.

`rota` and the managed `CLAUDE.md` blocks resolve relative to the current working directory, so per-package setups work as long as you run rota from inside the package. You can mix both styles in one repo; each `.rota/` is independent.

For several separate repos side by side under one folder, [umbrella mode](usage/umbrella-mode.md) keeps one backlog and knowledge base at the parent while each repo keeps its own history.

## Can I undo a shipped cycle?

For a cycle that merged directly into the base branch, yes: `/rota-ship --undo` previews the rollback, then resets the base branch past the merge commit and reopens the items it completed. Cycles shipped as a PR are refused; revert those through the forge. See [rolling back a cycle](usage/undo.md).

## How do I update rota when a new release ships?

Run `rota update` (needs `gh`). It detects how you installed rota (Homebrew, the install script, or a dev build) and prints the command, for example `brew update && brew upgrade rota && rota skills update`, or the install script's `curl` line followed by `rota skills update`. It doesn't run the update itself.

`rota skills update` refreshes the installed skills to match the new binary. Nothing else in your projects needs refreshing. Run `rota version --drift` in a project to see whether its stamped version trails the installed one. See [install](install.md#upgrading).
