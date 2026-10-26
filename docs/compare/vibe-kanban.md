# rota vs Vibe Kanban

[Vibe Kanban](https://github.com/BloopAI/vibe-kanban) is a kanban board for coding agents. You plan work as issues on a board, create a workspace per issue (a branch, a terminal and a dev server), run an agent in it, review the diff in the UI and open a PR. It is Apache-2.0 and starts with `npx vibe-kanban`. Checked 2026-10-26 against Vibe Kanban commit `a019cebf` (repo version 0.1.45; latest npm release 0.1.44, 2026-04-24).

Its README opens with "Vibe Kanban is sunsetting." The company behind it announced on 2026-04-10 that it is shutting down; the project continues as community-maintained open source and the hosted services were to go after 30 days. Check the repo's current state before you build a workflow on it.

## The short version

- Pick Vibe Kanban when you want a browser UI: a board, inline diff comments, a preview browser, and a choice among ten or more agents, with no setup beyond `npx`.
- Pick rota when you want a terminal workflow where the backlog is your tracker, a full test run on the merged result gates every merge, and the project keeps what it learned between sessions.
- Both put each agent in its own git worktree and branch. They differ in what sits above that: a UI for steering agents, or a process for queuing, verifying and merging their work.

## Side by side

| | Vibe Kanban | rota |
|---|---|---|
| What it is | Web UI (local app, self-hostable) for running coding agents in workspaces | A Go binary plus `/rota-*` skills that run inside Claude Code or Codex |
| Unit of work | A kanban issue, then a workspace for an agent to execute it | A backlog item (or tracker issue), one per worker at a time |
| Parallel agents | Several workspaces; limits not documented | Orchestrator plus two to five standing workers ([parallel rounds](../usage/parallel-rounds.md)) |
| Isolation | Each workspace has a branch, terminal and dev server; worktrees implied by the `DISABLE_WORKTREE_CLEANUP` setting | One git worktree per worker, reused across issues; per-slot ports and database suffix |
| Verification before merge | You review the diff and leave inline comments; test gating not documented | Merge gate runs `test.full` on a scratch merge of the PR, then merges on a pass ([review and ship](../usage/review-and-ship.md)) |
| Who merges | You, after review on GitHub | The orchestrator, through the gate. Workers open PRs and never merge |
| Backlog / tracker | Built-in kanban issues | `BACKLOG.md`, GitHub, GitLab, Jira, Linear or It's a Plan ([issue backend](../usage/issue-backend.md)) |
| Memory across sessions | Not documented | `KNOWLEDGE.md` and `DECISIONS.md` in the repo ([learning](../usage/learning.md)) |
| Agents and models | Claude Code, Codex, Gemini CLI, GitHub Copilot, Amp, Cursor, OpenCode, Droid, CCR, Qwen Code | Claude Code, Codex, OpenCode |
| Interface | Browser UI with preview, devtools and diff view | Terminal only |
| Platforms | npm package; a macOS source build and Windows notes appear in the README; no platform list | Linux and macOS; Windows through WSL2 |
| Install | `npx vibe-kanban` | `brew install l4ci/tap/rota` or the install script, then `rota skills install` |
| License | Apache-2.0 | MIT |

## Where Vibe Kanban is stronger

- **A visual surface.** The board, the diff view with inline comments sent back to the agent, and the built-in preview browser with devtools and device emulation have no terminal equivalent in rota.
- **Agent choice.** Ten listed agents against rota's three, including Copilot, Cursor and Gemini CLI. rota's market review listed more adapters as a gap.
- **Low setup.** One `npx` command and an authenticated agent. rota needs a binary, installed skills, `rota setup`, and for rounds a terminal host.
- **Team planning on a board.** Issues can be prioritised and assigned in the UI. rota leaves planning to your tracker.

The caveat is the project's status. Its maintainers announced a shutdown of the company and its hosted services, and the repo has no release newer than April. Commits were still landing on 2026-10-26.

## Where rota is stronger

- **A gate on the merged tree.** `rota worker gate` runs your full suite on a scratch merge before anything lands. Vibe Kanban documents diff review, not test gating.
- **Unattended rounds.** `rota round wait`, `rota keepalive run` and the hooks keep a round going for hours across restarts and usage limits ([unattended rounds](../usage/unattended-rounds.md)).
- **Several accounts.** `round.accounts` maps slots to separate Claude config homes, and `rota round assign` avoids an account that is cooling down ([parallel rounds](../usage/parallel-rounds.md)).
- **GitLab and your own tracker.** GitLab, Jira and Linear sources work today. The market review recorded self-hosted GitLab as the most upvoted open request on Vibe Kanban.
- **Durable knowledge.** `/rota-learn` writes gotchas to `KNOWLEDGE.md`, and `/rota-work` and `/rota-review` read them back. `/rota-decide` records hard boundaries the reviewers enforce ([learning](../usage/learning.md), [decisions](../usage/decisions.md)).

## Sources

- [Vibe Kanban repository and README](https://github.com/BloopAI/vibe-kanban): features, agents, install, license, commit history.
- [Shutdown announcement](https://www.vibekanban.com/blog/shutdown): 2026-04-10 company shutdown, community-maintained continuation, hosted services removal.
- [Supported coding agents](https://vibekanban.com/docs/supported-coding-agents): the agent list.
- [npm registry entry](https://www.npmjs.com/package/vibe-kanban): latest published version 0.1.44.
- [rota market review, October 2026](../contributing/market-review-2026-10.md): star count and top requests.
