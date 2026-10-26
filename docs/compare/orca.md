# rota vs Orca

[Orca](https://github.com/stablyai/orca) is a desktop app (Electron) for running a fleet of coding agents. Each agent works in its own git worktree, and Orca tracks them in one window. It also has an embedded browser, SSH worktrees, GitHub and Linear task views, a mobile app and an `orca orchestration` CLI that lets one agent coordinate others. Checked 2026-10-26 against Orca v1.4.241 (released 2026-10-26).

## The short version

- Pick Orca when you want a GUI to watch and steer many agents, to compare several attempts at one prompt, or to run agents on a remote machine or from your phone.
- Pick rota when you want issues to land on `main` unattended, with a test gate on the merged tree and a durable record of what the project learned.
- Both use a worktree per agent. They differ on what happens after a worker says it is done.

## Side by side

| | Orca | rota |
|---|---|---|
| What it is | Desktop app for a fleet of agents, with a CLI | A single Go binary plus `/rota-*` skills inside Claude Code or Codex |
| Parallel agents | Fan one prompt across several agents, or a coordinator starts workers | An orchestrator plus two to five standing workers, one issue each |
| Isolation | One git worktree per agent; SSH worktrees too | One git worktree per worker slot, reused across issues |
| Verification before merge | Not documented as a gate. The orchestration guide has workers report `worker_done` with an explicit `succeeded` or `failed` outcome | `rota worker gate` runs `test.full` on a scratch merge of the PR and merges only on a pass |
| Who merges | You: "compare the results and merge the winner" | The orchestrator, through the gate. Workers never merge |
| Backlog / tracker | Browse GitHub and Linear issues and PRs in the app, and open a worktree from a task | `.rota/BACKLOG.md`, GitHub, GitLab, Jira, Linear or It's a Plan |
| Memory across sessions | Not documented | `.rota/KNOWLEDGE.md` and `DECISIONS.md`, consulted by the skills |
| Agents and models | Claude Code, Codex, OpenCode, Pi, Amp, Cursor and others; "any CLI agent" | Claude Code and Codex, with OpenCode as a worker |
| Interface | Desktop GUI, mobile app, CLI | Terminal and slash commands; workers in herdr or tmux tabs |
| Platforms | macOS, Windows, Linux; iOS and Android companion | Linux and macOS; Windows through WSL2 |
| Install | Installer, Homebrew cask, AUR | Homebrew, install script, release binary |
| License | MIT | MIT |

## Where Orca is stronger

- **A real interface.** Terminal splits, a diff view where you comment on lines and send the comments back to the agent, an embedded browser with a design mode, and a mobile app for notifications and follow-ups. rota has none of this; you watch tabs.
- **Any CLI agent.** Orca lists dozens of agents and says anything that runs in a terminal works. rota supports Claude Code and Codex, plus OpenCode workers.
- **Comparing attempts.** Fanning one prompt across five agents and picking the best result is a first-class flow. rota gives each issue to one worker (`best-of:2` exists, but is a narrow case).
- **Remote and cross-platform.** SSH worktrees with reconnect and port forwarding, native Windows builds, and a remote runtime. rota runs where its terminal host runs, and on Windows only through WSL2.
- **Typed coordinator protocol.** Workers push `worker_done`, heartbeats and blocking questions to a durable mailbox, and liveness is reported as `live`, `unverifiable` or `exited`. rota borrowed several ideas from a code read of it (see the [market review](../contributing/market-review-2026-10.md)).
- **Reach.** About 106.9k stars on 2026-10-26.

## Where rota is stronger

- **A gate before merge.** Orca's documented outcome is the worker's own report. rota's [gate](../usage/parallel-rounds.md) runs the full suite on a scratch merge and merges only on a pass, then re-verifies `main` at wind-down.
- **Issue to merged PR.** Orchestrator, worker, review, gate and merge are one pipeline driven by `rota round` verbs ([parallel rounds](../usage/parallel-rounds.md), [review and ship](../usage/review-and-ship.md)). Orca's README describes merging as your step.
- **Trackers as the backlog.** Issues are the spec and carry state through the round ([issue backend](../usage/issue-backend.md)). In Orca they are a view you start work from.
- **Durable project memory.** `/rota-learn` and `/rota-decide` write knowledge and hard boundaries that later skills read back ([learning](../usage/learning.md), [decisions](../usage/decisions.md)). The Orca docs checked describe none.
- **Unattended rounds.** `rota keepalive`, `rota limit watch` and `rota reap` keep a round going across context limits and usage resets ([unattended rounds](../usage/unattended-rounds.md)).

## Using both

They can sit side by side. Orca can host the terminals where Claude Code or Codex run rota's skills. Whether rota's herdr or tmux dispatch works inside Orca's terminals is untested. Run one coordinator at a time, or both would assign work to the same agents.

## Sources

- [stablyai/orca](https://github.com/stablyai/orca): README for features, agents, platforms, install, license and the "merge the winner" line; GitHub API for stars, release tag and date.
- [Orca orchestration skill guide](https://github.com/stablyai/orca/blob/main/skill-guides/orchestration.md): Run, Task and Dispatch model, `worker_done` outcome, liveness verdicts.
- [rota market review, October 2026](../contributing/market-review-2026-10.md): code read of Orca's orchestration layer on 2026-10-16.
