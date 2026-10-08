# Orchestrator harnesses

`rota orchestrate` starts the orchestrator in the agent named by `orchestrator.harness`: `claude` (default), `codex`, `hermes` or `opencode`. Workers are a separate choice (`--kind`); this page is only about the orchestrator.

```sh
rota config set orchestrator.harness hermes
rota orchestrate --dry-run     # shows the command and first prompt
```

| Harness | Command the launcher runs | First prompt | Skills load from |
|---|---|---|---|
| `claude` | `claude --model <models.orchestrator> --permission-mode auto` (or `work.operatorCommand`) | `/rota-orchestrate` | `.claude/skills`, `~/.claude/skills` |
| `codex` | `codex` | `$rota-orchestrate` | `.agents/skills`, `~/.agents/skills` |
| `hermes` | `hermes chat -s rota-orchestrate -q` | plain sentence naming the skill | `.agents/skills` and `.hermes/skills` after `hermes skills trust`; `~/.hermes/skills` |
| `opencode` | `opencode --prompt` | plain sentence naming the skill | `.agents/skills`, `.claude/skills`, `.opencode/skills` and their `~/` twins |

The prompt is the last argument, on the first start and on every keepalive restart, which is why the Hermes and opencode commands end in `-q` and `--prompt`.

## Setup for Hermes and opencode

Both read `.agents/skills`, so the Codex install covers them:

```sh
rota skills install --scope project --agent codex
hermes skills trust            # Hermes only: a repo's skills stay off until trusted
```

`rota` must be on `PATH` for the agent.

## Solo mode

The skill's solo mode needs a subagent that can be pinned to a worktree. Only Claude Code has one. In any other harness, run the round in tmux or herdr (`rota config set work.dispatch tmux`); the skill tells the orchestrator to refuse solo and say so.

## Asking the maintainer

The skill asks in plain language. Claude Code renders that as `AskUserQuestion`; elsewhere it is an ordinary message in the session.

## Adopting work another tool started

Some tools make their own branch and worktree: Codex's managed worktrees, Claude Code agent teams, a cloud session that pushes a branch. Rota does not launch these and cannot read their panes, but it can track the branch so the overlap check, status, gate, train and reap see it like round work.

```sh
rota worker adopt codex/612-thing --issue 612          # a branch
rota worker adopt ~/work/thing-wt --issue 612 --pr <url>  # a worktree of this repo
```

The slot is `external`: no host, no session. Rota runs the same file-overlap check as `round assign` and exits 4 with `blockedBy: overlap` on a blocking clash (`--accept-overlap` skips only that). It also refuses a branch or an issue that a slot already holds (`registered`, `held`). The path must be a worktree in `git worktree list` for this repo.

What changes for an adopted slot:

- `rota round status` shows `external` as its host, and a state worked out from the forge and git: `done` when its PR is open, `busy` when the branch is ahead of the base with no PR, `idle` otherwise, `unknown` while the forge cannot be reached. No `ROTA-DONE` is expected; the PR is the signal.
- `rota worker dispatch` refuses it (`blockedBy: host`). Talk to that agent in its own tool.
- `rota worker gate <slot>` and `rota worker train` gate its PR like any other. There are no relays to check, so the provenance step passes with `None` for approvals.
- After the merge the slot is released: the registry entry goes, the worktree and branch stay, because rota did not create them. `rota reap` then lists the merged branch for you to delete. `rota worker gate --prune` or `rota round wind-down --prune` removes the worktree and branch instead, and skips a dirty worktree.

To catch these branches without naming each one, set `round.adoptPattern` to a glob over the branch names:

```sh
rota config set round.adoptPattern 'codex/*'
```

`rota round reconcile` then reports an `unregistered-branch` for each matching branch that no slot holds and that is not merged. `--apply` adopts the ones whose name carries an issue number (`<agent>/<issue>-<slug>`, `issue-N`, `#N`); the others stay listed with `needs --issue`. See [parallel rounds](parallel-rounds.md#adopting-work-another-tool-started).

## Last verified (2026-10-04)

> [!NOTE]
> This is a dated snapshot, not a compatibility guarantee. Checked 2026-10-04 on Codex 0.159.2, Hermes v0.21.5 and opencode 1.17.9, in a scratch git repo with one probe skill per location.

- **Codex.** See [Codex skills](codex-skills.md). Tab-mode round end to end (herdr, Codex 0.159.2 orchestrator and Codex workers, file backend, no forge): two tasks assigned, built, merged with `rota worker gate` and wound down clean. It needed the worker contract found under `.agents/skills` (`rota round assign` read only Claude roots until this change), and it ran with `approval_policy=never` and `sandbox_mode=danger-full-access` because Codex's sandbox did not initialise on the test machine.
- **Hermes.** `hermes skills list` shows a `.agents/skills` probe only after `hermes skills trust`; a `.claude/skills` probe never loads. `hermes chat -s <skill> -Q -q <prompt>` resolved the trusted project skill and the model answered; an unknown name fails with `Unknown skill(s)`. Not verified: a full orchestrator round, and whether a literal `/rota-orchestrate` typed as the seed prompt dispatches as a slash command (the launcher avoids it by using `-s`).
- **opencode.** `opencode debug skill` listed probes in `.agents/skills`, `.claude/skills` and `.opencode/skills`. Not verified: a model session loading the skill (the account had no funds), and a full round. opencode has no slash command for skills; the model loads one through its skill tool, so the prompt names the skill in words and may need a nudge.
