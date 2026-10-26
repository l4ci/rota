# rota vs Claude Squad

[Claude Squad](https://github.com/smtg-ai/claude-squad) is a terminal app that manages several AI coding agents in separate workspaces. Each session gets a tmux session and a git worktree on its own branch, and a TUI lists them, previews their output and shows diffs. Checked 2026-10-26 against Claude Squad 1.0.20 (released 2026-08-20; it is also the latest commit).

## The short version

- Pick Claude Squad when you want to drive several agents by hand from one terminal window: start sessions, watch them, attach to reprompt, push a branch when you like the diff.
- Pick rota when you want the queue, the verification and the merge handled by a process: issues assigned to workers, a test run on the merged result, then a merge.
- Both give each agent a git worktree. Claude Squad stops at a branch you push. rota continues through review, gate and merge.

## Side by side

| | Claude Squad | rota |
|---|---|---|
| What it is | A TUI (`cs`) that runs agents in tmux sessions | A Go binary plus `/rota-*` skills that run inside Claude Code or Codex |
| Unit of work | A session, started with an optional prompt | A backlog item (or tracker issue), one per worker at a time |
| Parallel agents | Many sessions in one window; limits not documented | Orchestrator plus two to five standing workers ([parallel rounds](../usage/parallel-rounds.md)) |
| Isolation | tmux session plus git worktree and branch per session | One git worktree per worker, reused across issues; per-slot ports and database suffix |
| Verification before merge | You review changes in the diff tab before pushing; no test run documented | Merge gate runs `test.full` on a scratch merge of the PR ([review and ship](../usage/review-and-ship.md)) |
| Who merges | You. `s` commits and pushes the branch to GitHub; merge is not documented | The orchestrator, through the gate. Workers open PRs and never merge |
| Backlog / tracker | None documented | `BACKLOG.md`, GitHub, GitLab, Jira, Linear or It's a Plan ([issue backend](../usage/issue-backend.md)) |
| Memory across sessions | Not documented | `KNOWLEDGE.md` and `DECISIONS.md` in the repo ([learning](../usage/learning.md)) |
| Agents and models | Claude Code by default; Codex, Gemini and Aider named; any command via `-p` or a profile | Claude Code, Codex, OpenCode |
| Interface | Terminal UI | Terminal; skills inside the agent, optional herdr or tmux tabs |
| Platforms | Not documented; needs tmux and `gh` | Linux and macOS; Windows through WSL2 |
| Install | `brew install claude-squad` or an install script; binary is `cs` | `brew install l4ci/tap/rota` or the install script, then `rota skills install` |
| License | AGPL-3.0 | MIT |

## Where Claude Squad is stronger

- **Immediacy.** One command, no project setup, no backlog. Press `n`, type a prompt, and an agent is running in its own worktree.
- **A live overview.** The TUI shows every session with a preview and a diff tab. You attach with `Enter` to reprompt and detach with `ctrl-q`. rota's tabs and `rota round status` suit a queue more than a conversation.
- **Any agent.** `-p` or a profile launches any CLI program, so local and niche agents work. rota supports three harnesses.
- **Pause and resume.** `c` commits and pauses a session; `r` resumes it.
- **Auto-accept.** `--autoyes` (experimental) accepts prompts for Claude Code and Aider. rota's autonomy is set per level in config ([autonomy](../usage/autonomy.md)).

## Where rota is stronger

- **A gate on the merged tree.** `rota worker gate` runs your full suite on a scratch merge before anything lands. Claude Squad leaves checking and merging to you.
- **A queue.** `rota round assign` hands issues to idle workers, `rota round wait` reports who needs you, and file-overlap checks keep two workers off the same files ([parallel rounds](../usage/parallel-rounds.md)).
- **Runs without you.** `rota keepalive run` and the hooks carry a round across context limits and restarts ([unattended rounds](../usage/unattended-rounds.md)).
- **Several accounts.** `round.accounts` spreads workers over separate Claude config homes and avoids an account that is cooling down.
- **Trackers.** The backlog can live in GitHub, GitLab, Jira or Linear, and workers pick up issues that carry acceptance criteria.
- **Durable knowledge.** `/rota-learn` and `/rota-decide` keep gotchas and hard rules in the repo, and the reviewers read them back ([learning](../usage/learning.md), [decisions](../usage/decisions.md)).
- **Review.** `/rota-review` runs Spec and Standards reviewers against the issue before the gate.

## Sources

- [Claude Squad repository and README](https://github.com/smtg-ai/claude-squad): features, key bindings, install, prerequisites, license, "How it works".
- [Releases](https://github.com/smtg-ai/claude-squad/releases): v1.0.20, 2026-08-20.
- [rota market review, October 2026](../contributing/market-review-2026-10.md): star count and open requests.

More comparisons: [how rota compares](README.md).
