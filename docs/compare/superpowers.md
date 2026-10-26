# rota vs Superpowers

[Superpowers](https://github.com/obra/superpowers) is "a complete software development methodology for your coding agents, built on top of a set of composable skills." It is a skills library: brainstorming, writing-plans, subagent-driven-development, test-driven-development, systematic-debugging, code review and a few more, which trigger on their own as you work. Checked 2026-10-26 against Superpowers v7.0.1 (released 2026-10-21).

## The short version

- Pick Superpowers when you want a disciplined way of working inside one session, on many agent harnesses, with nothing to install but skills.
- Pick rota when work comes from a backlog, several issues should run side by side, and a merge gate should verify the result before anything lands on `main`.
- Both are skills, so they overlap in the solo loop (design, plan, build, review). They differ most once you have more than one agent.

## Side by side

| | Superpowers | rota |
|---|---|---|
| What it is | Skills methodology | Single Go binary plus `/rota-*` skills |
| Unit of work | A feature or task in the current conversation, broken into plan tasks | A backlog item or tracker issue |
| Parallel agents | A fresh subagent per plan task, run inside one session; `dispatching-parallel-agents` for independent problems | An orchestrator plus standing workers, one issue each, in separate sessions |
| Isolation | `using-git-worktrees` sets up a worktree for the work | One git worktree per worker, reused across issues |
| Verification before merge | `verification-before-completion` (run the command, read the output), per-task review, final branch review, full suite before the finish menu | Targeted checks per worker, then a gate that runs `test.full` on a scratch merge before merging |
| Who merges | You pick from a menu: merge locally, open a PR, or keep the branch | The orchestrator, through the gate; workers open PRs and never merge |
| Backlog / tracker | Not documented | `.rota/BACKLOG.md`, GitHub, GitLab, Jira, Linear and others |
| Memory across sessions | Plan files in the repo; no persistent memory layer documented | `KNOWLEDGE.md`, `DECISIONS.md`, handoff notes in `.rota/` |
| Agents and models | 16 harnesses listed, including Claude Code, Codex, Cursor, Gemini CLI, Copilot CLI, OpenCode | Claude Code and Codex; OpenCode workers |
| Interface | Skills inside your agent; no CLI | Skills plus the `rota` CLI, terminal only |
| Install | Plugin install, separately per harness | `brew install l4ci/tap/rota` or the install script, then `rota skills install`; also a Claude Code plugin |
| License | MIT | MIT |

## Where Superpowers is stronger

- **Reach.** It lists 16 harnesses with per-harness install steps. rota supports Claude Code and Codex, with OpenCode as a worker harness.
- **Nothing to run.** It is skills only. rota needs a binary on your `PATH`, a `.rota/` folder in the project and, for rounds, `gh` or `glab` and a terminal host.
- **Skills fire on their own.** `using-superpowers` makes the agent look for a matching skill before it answers. You do not type a command. rota skills are mostly invoked by name.
- **Within-task discipline.** Fresh subagent per task, a spec and quality review after each, and a whole-branch review at the end. `subagent-driven-development` also tells the agent to record rulings instead of stalling, so a plan can run for a long stretch without you.

## Where rota is stronger

- **Several issues at once.** A round runs two to five standing workers in parallel, each in its own worktree and terminal tab, with the orchestrator assigning, relaying answers and merging. See [parallel rounds](../usage/parallel-rounds.md). Superpowers' parallelism is subagents inside one session.
- **A merge gate.** `rota worker gate` runs the full suite on a scratch merge and merges only on a pass, so a red result does not reach `main`. See [how it works](../how-it-works.md).
- **A real backlog.** Items live in a file or in GitHub, GitLab, Jira or Linear issues, and workers pick them up by ID. See [issue backend](../usage/issue-backend.md).
- **Usage limits.** `round.accounts` spreads workers over several accounts, and `rota limit watch` moves an issue to a free slot when one hits a limit. See [parallel rounds](../usage/parallel-rounds.md).
- **Durable project memory.** `/rota-learn` and `/rota-decide` write to `KNOWLEDGE.md` and `DECISIONS.md`; `/rota-pause` leaves a handoff note. See [learning](../usage/learning.md) and [decisions](../usage/decisions.md).
- **Recorded proof.** `/rota-work` requires a red run, then a green one, through `rota proof record`. See [running work](../usage/running-work.md).

## Using both

They are both skills, so you can install both. The combination is untested. The practical question is overlap. Each ships a brainstorming skill, a planning skill, TDD, debugging and review, and both descriptions tell the agent to use them early. If both fire, the agent has two competing playbooks for the same step.

A split that avoids that:

- Use rota for the backlog, rounds, the gate and project memory.
- Borrow Superpowers' within-task skills where rota has no equivalent step, for example `verification-before-completion` or `systematic-debugging`, and leave its planning and branch-finishing skills off so they do not compete with `/rota-plan` and `/rota-ship`.

`finishing-a-development-branch` ends by asking you to merge, open a PR or keep the branch. In a round, workers must not merge, so that skill conflicts with the worker contract.

## Sources

- [Superpowers README](https://github.com/obra/superpowers): description, workflow, install per harness, harness list, MIT license.
- [Superpowers skills](https://github.com/obra/superpowers/tree/main/skills): `subagent-driven-development`, `using-git-worktrees`, `verification-before-completion`, `finishing-a-development-branch`, `dispatching-parallel-agents`, `writing-plans`.
- [Superpowers releases](https://github.com/obra/superpowers/releases): v7.0.1 and its date. Stars (305.5k) from the GitHub API on 2026-10-26.
- rota: [README](../../README.md), [how it works](../how-it-works.md), [parallel rounds](../usage/parallel-rounds.md), [install](../install.md).

More comparisons: [how rota compares](README.md).
