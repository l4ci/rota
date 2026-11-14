# rota vs Spec Kit

[Spec Kit](https://github.com/github/spec-kit) is GitHub's toolkit for spec-driven development. A Python CLI, `specify`, installs `/speckit.*` commands into a coding agent, and the agent then runs a feature from constitution to spec, plan, tasks and implementation, with each step writing a Markdown file into the repo. Checked 2026-11-13 against Spec Kit v1.4.0 (released 2026-11-10) and rota 0.34.2.

## The short version

- Pick Spec Kit when the hard part is pinning down what a feature should do before any code exists, and you want the spec, plan and task list kept as files next to the code.
- Pick rota when you already have issues and want them built in parallel, each in its own worktree, and merged only after the full suite passes on the merged tree.
- They meet at the task list. Spec Kit's implement step works through `tasks.md` in one agent session and leaves review and the PR to you; rota starts from an issue and ends at a merge.

## Side by side

| | Spec Kit | rota |
|---|---|---|
| What it is | `specify` CLI (Python) plus `/speckit.*` commands or skills installed into the agent | A Go binary plus `/rota-*` skills |
| Unit of work | A feature: `spec.md`, `plan.md`, `tasks.md` and supporting files under `specs/` | A backlog item or tracker issue |
| Core loop | `constitution`, `specify`, `clarify`, `plan`, `checklist`, `tasks`, `analyze`, `implement`, `converge`; only `specify` is required before `plan` | `/rota-capture`, `/rota-work`, `/rota-ship`; rounds for parallel work |
| Parallel agents | Tasks that can run in parallel are marked `[P]`; the docs do not say whether implement runs them in parallel or in subagents | An orchestrator plus two to five standing workers, one issue each |
| Isolation | Not documented; no worktree handling. Numbered feature branches come from the opt-in `git` extension | One git worktree per worker, reused across issues |
| Verification before merge | `analyze` cross-checks spec, plan and tasks before implementing; `converge` compares code with the artifacts and appends gap tasks until it reports "Converged". Tests are generated when requested and gate nothing | Gate runs `test.full` on a scratch merge before merging; `/rota-review` runs Spec and Standards reviewers |
| Who merges | You. The docs end at "review and open a PR" | The orchestrator, through the gate. Workers never merge |
| Backlog / tracker | `tasks.md` per feature; the bundled `github` extension files tasks as GitHub issues. Linear, Jira and Azure DevOps extensions are community-only | `.rota/BACKLOG.md`, GitHub, GitLab, Jira, Linear or It's a Plan |
| Memory across sessions | `.specify/memory/constitution.md` plus the spec folders | `KNOWLEDGE.md`, `DECISIONS.md` and handoff notes in `.rota/` |
| Agents and models | 41 agent integrations, plus `generic` and `none` | Claude Code, Codex, OpenCode; several accounts with usage-limit awareness |
| Platforms | Linux, macOS and Windows; PowerShell scripts without WSL | Linux and macOS; Windows through WSL2 |
| Install | `uv tool install specify-cli` (Python 3.11+), or pipx; then `specify init` | `brew install l4ci/tap/rota` or `install.sh`, then `rota skills install` and `rota setup` |
| License | MIT | MIT |

## Where Spec Kit is stronger

- **Specification as a process.** `specify` writes the what and why, `clarify` asks up to five questions and writes the answers back into the spec, and `checklist` writes "unit tests for requirements" that a human ticks off. rota has `/rota-brainstorm` and `/rota-plan`, but per item and only when the planning dial calls for it.
- **Consistency checks across artifacts.** `analyze` reads spec, plan and tasks together before implementation and reports conflicts and gaps. rota's [plan critic](../../skills/references/plan-critic.md) reads only the item and the proposed plan.
- **A loop back from code to spec.** `converge` keeps appending gap tasks until the code matches the artifacts. rota's acceptance marks block an unmet criterion but do not write the follow-up work for you.
- **Reach and customization.** It installs into more than 40 agents, runs natively on Windows, and has extensions, presets, workflows and a community catalog. rota supports three harnesses and is configured, not extended.

## Where rota is stronger

- **Parallelism across issues.** A [rota round](../usage/parallel-rounds.md) runs separate issues side by side in separate worktrees. Spec Kit runs one feature's task list in one session.
- **A merge gate.** `rota worker gate` runs the full suite on a scratch merge of each PR and merges only on a pass. Spec Kit leaves review and merging to you. See [how it works](../how-it-works.md).
- **Trackers.** Items are GitHub, GitLab, Jira or Linear issues ([issue backend](../usage/issue-backend.md)). In Spec Kit only the GitHub extension is maintained in the repo.
- **Recorded proof.** `/rota-work` records a red run and a green run with `rota proof record`, and acceptance criteria are marked against that proof. See [running work](../usage/running-work.md).
- **Usage limits.** `round.accounts` and `rota limit watch` move work to another account when one hits a limit.

## Using both

They meet at `tasks.md`. Running the two together is untested. One way to combine them: write the spec, plan and tasks in Spec Kit, file the tasks as GitHub issues with the `github` extension's `taskstoissues` command, and let a rota round build them. Use `[P]` markers and the story phases to decide which issues can share a round. The `.specify/` and `.rota/` folders do not collide. The cost is two places that describe the same work: close the issues and update `tasks.md` as PRs merge.

## Sources

- [Spec Kit README](https://github.com/github/spec-kit): description, install, license, star count (about 150k on 2026-11-13).
- [Spec Kit docs](https://github.github.io/spec-kit/): [installation](https://github.github.io/spec-kit/installation.html), [quickstart](https://github.github.io/spec-kit/quickstart.html), [integrations](https://github.github.io/spec-kit/integrations/index.html), [command reference](https://github.github.io/spec-kit/reference/agentic-sdd.html), [extensions](https://github.github.io/spec-kit/extensions/overview.html), [community extensions](https://github.github.io/spec-kit/community/extensions.html).
- [`github` extension](https://github.com/github/spec-kit/blob/main/extensions/github/README.md) and [`git` extension](https://github.com/github/spec-kit/blob/main/extensions/git/README.md): issue filing and feature branches.
- [Spec Kit releases](https://github.com/github/spec-kit/releases): v1.4.0; v1.3.0 moved `taskstoissues` out of the core.
- rota: [README](../../README.md), [how it works](../how-it-works.md), [market review](../contributing/market-review-2026-10.md).

More comparisons: [how rota compares](README.md).
