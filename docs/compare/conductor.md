# rota vs Conductor

[Conductor](https://www.conductor.build) is a Mac app for running Claude Code, Codex, Cursor and OpenCode agents in parallel, each in its own workspace (a git worktree and branch). You review diffs, track checks, open the PR and merge from the app. It is a commercial product with a free local tier, with no public source repository or issue tracker.

Checked 2026-10-26 against Conductor 0.92.0 (released 2026-10-23).

## The short version

- Pick Conductor when you want a native Mac interface for watching and steering several agents, with diff review and PR checks in one window.
- Pick rota when you work in a terminal, on Linux or macOS, and want agents to take issues from a tracker, verify on a merged tree and keep project knowledge in the repo.
- They overlap on worktrees per task. They differ on who drives: in Conductor you do; in a rota round an orchestrator does.

## Side by side

| | Conductor | rota |
|---|---|---|
| What it is | Mac app, closed source | Go binary plus `/rota-*` skills, MIT |
| Unit of work | Workspace (branch plus working tree) | Backlog item or tracker issue |
| Parallel agents | You start workspaces; agents can also share one workspace | Orchestrator assigns issues to standing workers |
| Isolation | Git worktree per workspace; "not a security boundary" | Git worktree per worker, plus per-slot ports and database suffix |
| Verification before merge | Checks tab (CI, status, review threads, todos); Conductor may block merge on failures | Gate runs `test.full` on a scratch merge of the PR before landing it |
| Who merges | You merge when the PR is approved and checks are green | The orchestrator, through the gate; workers never merge |
| Backlog / tracker | Create a workspace from a GitHub or Linear issue | `.rota/BACKLOG.md`, GitHub, GitLab, Jira, Linear, It's a Plan |
| Memory across sessions | Archived workspaces keep chat history; `.context` folder for notes | `.rota/KNOWLEDGE.md`, `DECISIONS.md`, handoff notes |
| Agents and models | Claude Code, Codex, Cursor, OpenCode | Claude Code, Codex, OpenCode |
| Interface | Native app; HTTP API and MCP server (beta) | Terminal; herdr or tmux tabs |
| Platforms | macOS (iOS companion app) | Linux and macOS; Windows through WSL2 |
| Install | Download the app | `brew install l4ci/tap/rota` or `install.sh` |
| License | Closed source; Free, Pro $50/mo, Teams $60/user/mo | MIT |

## Where Conductor is stronger

- **A real UI.** Diff viewer with inline comments sent back to the agent, a Checks tab, and an overview of every workspace. rota has no GUI.
- **Cursor support and cloud workspaces.** Cursor is a first-class agent, and paid plans add cloud workspaces, an HTTP API, routines triggered by webhook and a mobile app. rota has none of these.
- **Lower setup cost.** Install an app, create a workspace, prompt. A rota round needs a host (herdr or tmux), a configured backlog and `rota doctor` passing.
- **PR loop help.** Conductor can draft the PR description, answer review comments and follow Actions for the branch. In rota the worker or orchestrator does this through the skills.
- **Shared workspaces.** Several agents can work one branch together, which rota does not model.

## Where rota is stronger

- **An orchestrator.** `/rota-orchestrate` picks candidate issues, checks file overlap, assigns, relays decisions and merges, so a queue drains without you steering each agent ([parallel rounds](../usage/parallel-rounds.md)). Conductor's docs describe no coordination across workspaces.
- **Merge-time verification.** `rota worker gate` builds a scratch merge and runs your full suite there; `rota worker train` pays for that once for several PRs. Conductor surfaces CI results but does not say it runs your suite itself.
- **Usage-limit awareness.** `round.accounts` spreads workers over logins and `rota limit watch` reroutes a limited worker ([unattended rounds](../usage/unattended-rounds.md)). Conductor's docs do not mention accounts.
- **Durable project memory.** Knowledge and decisions are reviewed files in the repo, read by every session ([learning](../usage/learning.md), [decisions](../usage/decisions.md)).
- **Open and scriptable.** MIT license, a single binary, Linux support, and verbs you can run from scripts or CI.

## Using both

Possible but redundant. Conductor can open any git repo, so you could review a rota branch there, but both create worktrees and branches and would not share state. Pick one as the driver per repo.

## Sources

- [Conductor home](https://www.conductor.build) and [docs](https://www.conductor.build/docs/): agents, platform, overview.
- [Workspaces and branches](https://www.conductor.build/docs/concepts/workspaces-and-branches): worktrees, isolation.
- [Workflow](https://www.conductor.build/docs/concepts/workflow) and [Checks](https://www.conductor.build/docs/reference/checks): PR flow, GitHub and Linear issues, merge checks.
- [Parallel agents](https://www.conductor.build/docs/concepts/parallel-agents): shared versus separate workspaces.
- [API](https://www.conductor.build/docs/api), [pricing](https://www.conductor.build/pricing), [changelog](https://www.conductor.build/changelog): API, plans, 0.92.0.

More comparisons: [how rota compares](README.md).
