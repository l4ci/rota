# Getting started

Install rota and run your first capture → work → ship cycle in about five minutes.

## 📦 Install

From 0.9.0:

```bash
brew install l4ci/tap/rota   # or: curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh
rota skills install
```

The skills land in the Claude Code and Codex skill directories. Options, upgrading and removal are on the [install page](install.md).

## ⚙ Initialize the project

Run `rota init` once at the project root. It scaffolds `.rota/` and writes the Recommended
config defaults (models, isolation, merge strategy, quality gates, autonomy level). Keep the
defaults unless you have a reason not to.

> [!TIP]
> `rota setup` does the same and, on a terminal, asks the main choices (backlog backend, isolation, merge strategy, autonomy, review and QA gates); bare `rota` in an uninitialized project runs it.

Three settings worth a second of thought:

- **Backlog.** `file` (default) keeps it in `.rota/BACKLOG.md`. `issues` makes GitHub or GitLab issues the backlog. See the [issue backend](usage/issue-backend.md).

- **Isolation.** `branch` is fine for solo work. Switch to `worktree` if you want `main`
  untouched while agents run, or if you plan to run parallel `/rota-work` sessions.
- **Merge strategy.** `direct` for fast iteration. `pr` if your team requires GitHub review (the issue backend always opens a PR).

> [!IMPORTANT]
> Set `test.full` to your project's test command (`rota config set test.full "npm test"`). The solo loop below doesn't need it. A parallel round does: the gate refuses to merge while `test.full` is empty, unless you pass `--no-verify`.

> [!TIP]
> To change a setting later, run `rota config set <key> <value>` (`rota config show` lists the keys). See [config options](reference/config-options.md). Don't hand-edit the JSON files.

## 🧪 Worked examples

Two end-to-end walkthroughs carry one concrete project from brief to shipped milestone:

- [Greenfield: from a brief to a shipped milestone](walkthroughs/greenfield-from-brief.md): empty repo plus a one-page brief, walked through `/rota-vision`, `/rota-plan`, `/rota-work`, `/rota-debug`, `/rota-ship`, `/rota-learn`.
- [Brownfield: dropping rota into an existing project](walkthroughs/brownfield-existing-project.md): established codebase with open GitHub issues and a mental bug list, walked through `rota init`, `/rota-capture`, then a P0 cycle and a debug cycle.

Pick whichever matches where your project is today and follow it skill-by-skill.

## 🧭 Where to go next

**Scale to a round**
- [Your first round](first-round.md): the next step. Set up herdr, start the orchestrator and run a round on a few issues
- [Parallel rounds](usage/parallel-rounds.md): when you have several independent issues, let an orchestrator hand them to workers and merge what passes. Each round PR must say `Closes #N` (an issue labelled `partial-slice` is exempt) or the gate refuses it

**Capture and backlog**
- [Capturing work](usage/capturing-work.md)
- [Picking work](usage/picking-work.md)

**Execution**
- [Running work](usage/running-work.md)
- [Debugging](usage/debugging.md)
- [Pausing and resuming](usage/pausing-and-resuming.md)

**Shipping**
- [Review and ship](usage/review-and-ship.md)
- [Learning and KNOWLEDGE.md](usage/learning.md)
- [Decisions and DECISIONS.md](usage/decisions.md)

**Vision and planning**
- [Vision and plans](usage/vision-and-plans.md)
- [Spikes](usage/spikes.md)

**Reference**
- [Configuration](usage/configuration.md)
- [Autonomy levels](usage/autonomy.md)
