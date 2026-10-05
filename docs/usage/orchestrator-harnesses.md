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

## Last verified (2026-10-04)

This is a dated snapshot, not a compatibility guarantee. Checked 2026-10-04 on Codex 0.159.2, Hermes v0.21.5 and opencode 1.17.9, in a scratch git repo with one probe skill per location.

- **Codex.** See [Codex skills](codex-skills.md). Tab-mode round end to end (herdr, Codex 0.159.2 orchestrator and Codex workers, file backend, no forge): two tasks assigned, built, merged with `rota worker gate` and wound down clean. It needed the worker contract found under `.agents/skills` (`rota round assign` read only Claude roots until this change), and it ran with `approval_policy=never` and `sandbox_mode=danger-full-access` because Codex's sandbox did not initialise on the test machine.
- **Hermes.** `hermes skills list` shows a `.agents/skills` probe only after `hermes skills trust`; a `.claude/skills` probe never loads. `hermes chat -s <skill> -Q -q <prompt>` resolved the trusted project skill and the model answered; an unknown name fails with `Unknown skill(s)`. Not verified: a full orchestrator round, and whether a literal `/rota-orchestrate` typed as the seed prompt dispatches as a slash command (the launcher avoids it by using `-s`).
- **opencode.** `opencode debug skill` listed probes in `.agents/skills`, `.claude/skills` and `.opencode/skills`. Not verified: a model session loading the skill (the account had no funds), and a full round. opencode has no slash command for skills; the model loads one through its skill tool, so the prompt names the skill in words and may need a nudge.
