# Getting started

Install rota and run your first capture → work → ship cycle in about five minutes.

## 📦 Install

From 0.9.0:

```bash
brew install l4ci/tap/rota   # or: curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh
rota skills install
```

The skills land in the Claude Code and Codex skill directories. Options, upgrading and removal are on the [install page](install.md).

## ⚙ Set up the project

Run `rota setup` once at the project root. It asks the main config questions, scaffolds `.rota/`
and writes your answers. Press Enter to keep the value it suggests. Where the repo gives a signal (your `go.mod` or `package.json`, CI files, the `origin` host, the harnesses on your `PATH`), setup suggests that value and says where it read it, for example `suggested: github (origin is github.com, gh is authenticated)`. A value you saved in your global config wins over the repo's signal, which is then shown as a note. Each question decides one thing:

- **Machine role** (`round.role`). `main` (default) sets the project up. `support` is for a second machine that builds issues pinned `machine:<round.support>` beside main: see the support machine tip below.
- **Issue source** (`issues.source`). Where the backlog lives. `file` (default) keeps it in `.rota/BACKLOG.md`. `github`, `gitlab`, `forge`, `jira`, `linear` or `itsaplan` make that tracker the backlog. See the [issue backend](usage/issue-backend.md).
- **Isolation** (`work.isolation`). `worktree` (default) keeps `main` untouched while agents run and allows parallel `/rota-work` sessions. `branch` is fine for solo work.
- **Merge strategy** (`work.mergeStrategy`). `direct` merges finished work straight into the base branch. `pr` opens a PR for review (the issue backend always opens a PR).
- **Round dispatch** (`round.dispatch`). Where parallel round workers run: `subagent`, `tmux` or `herdr`. Solo work ignores it.
- **Autonomy** (`autonomy.level`). How much rota may chain on its own: `off`, `roundtrip` or `auto`.
- **Review depth** (`ship.review`). How thorough the review before shipping is: `full`, `light` or `none`.
- **QA** (`ship.qa`). Whether `/rota-ship` runs QA first.
- **Worker harness** (`round.harnesses`). Which harnesses round workers run on, the first being the default: `claude`, `codex` or `opencode`.
- **Orchestrator harness** (`orchestrator.harness`). Which harness runs the round orchestrator.
- **Base branch** (`git.baseBranch`). The branch the skills merge into. Free text; leave it empty to auto-detect.
- **Quick test command** (`test.fast`). The per-task check, such as `go test ./...`. Type one command or a JSON list; empty leaves it unset.
- **Full test command** (`test.full`). The whole suite the merge gate runs. Same format.

`rota setup --list` prints the questions, how to answer them and each suggestion with its evidence (`--json` for scripts). Bare `rota` in an uninitialized project runs setup. In your home directory `rota setup` refuses (pass `--force`); bare `rota` opens the projects picker instead.

> [!TIP]
> For scripts and CI, `rota init` scaffolds `.rota/` with the Recommended defaults and asks nothing. `rota setup --yes` takes the suggested values (the defaults where there is no signal), and `--set key=value` answers one question.

> [!TIP]
> To add a support machine, run `rota round support enable <name>` on main and commit the change. On the second machine, pull the repo and run `rota setup` with role `support` (`--set round.role=support` in scripts). It asks only the machine-local questions, writes them to `.rota/config.local.json` and runs the doctor. See [Support role](contributing/rounds.md#support-role).

> [!IMPORTANT]
> Set `test.full` to your project's test command (`rota config set test.full "npm test"`). The solo loop below doesn't need it. A parallel round does: the gate refuses to merge while `test.full` is empty, unless you pass `--no-verify`.

> [!TIP]
> To change a setting later, run `rota config set <key> <value>` (`rota config show` lists the keys). See [config options](reference/config-options.md). Don't hand-edit the JSON files.

## 🧪 Worked examples

A short quickstart shows one item through the manual loop, and two end-to-end walkthroughs carry one concrete project from brief to shipped milestone:

- [Quickstart: one item in manual mode](walkthroughs/quickstart-manual-mode.md): one small bug through `/rota-capture`, `/rota-plan`, `/rota-work` and `/rota-ship`, with the proof and review gates marked.
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
