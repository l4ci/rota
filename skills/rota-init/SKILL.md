---
name: rota-init
description: Use only when the maintainer types "/rota-init" to have rota inspect the repo and suggest config values. Never starts on its own.
disable-model-invocation: true
---

# rota-init: Suggest Config Values From the Project

`rota init` and `rota setup` offer the schema defaults whatever the project is. This skill reads the repo first, proposes values with the evidence for each and asks the maintainer to confirm them one at a time. It writes nothing the maintainer did not confirm.

## When NOT to use

- The maintainer did not invoke it. Never start it from another skill.
- Changing a default, or installing a harness or terminal host. Both are out of scope: the skill suggests values for what is already installed.
- Recording a hard boundary → `/rota-decide`.

Copy this checklist and track your progress:
```
- [ ] Step 1 — Read signals
- [ ] Step 2 — Compare with current config
- [ ] Step 3 — Ask one at a time
- [ ] Step 4 — Write confirmed answers
- [ ] Step 5 — Report
```

## Step 1 — Read signals

Read the repo; do not ask what it can answer ([`references/authoring-conventions.md`](references/authoring-conventions.md), *Don't ask what the code can answer*). For each key below, note the value and the evidence (file, line or command output). A key with no signal is not proposed: it keeps the schema default, and the report says so.

| Key | Affects | Signals |
|---|---|---|
| `test.fast` | /rota-work and rounds | The narrowest test command that takes changed files: `go test`, `package.json` scripts (`test`, `lint`), `pyproject.toml` pytest or ruff config, `cargo test`, Makefile targets (`test`, `check`, `lint`) |
| `test.full` | /rota-work and rounds | The whole-suite command from the same files, plus what CI runs (`.github/workflows/*`, `.gitlab-ci.yml`) |
| `git.baseBranch` | both | `git symbolic-ref --short refs/remotes/origin/HEAD`, else the branch `git branch --list main master trunk` finds |
| `forge.provider` | both | The `origin` host from `git remote get-url origin`: github.com → `github`, a GitLab host → `gitlab`, no remote → `local` |
| `issues.source` | both | The forge above (`github` or `gitlab`) when `gh` or `glab` is installed and authenticated; `file` otherwise. A `.jira` or `.linear` config is a hint, not a proof: say so |
| `round.workerKind` | rounds | Harness CLIs on `PATH` (`command -v claude codex opencode`). Several installed: list them, recommend the one running this session |
| `work.isolation` | /rota-work (solo) | `worktree` when the repo already has linked worktrees (`git worktree list`) or `.worktrees/`; `branch` otherwise |
| `round.dispatch` | rounds | `herdr` when it is on `PATH`, else `tmux` when it is and `$TMUX` is set, else `subagent` |

`work.*` keys affect `/rota-work` and other solo cycles. `round.*` keys affect parallel rounds (`rota round`, `rota worker`). Say which one each proposal affects when you ask.

Never run a test command to prove it works: the evidence is where you read it.

## Step 2 — Compare with current config

- **No `.rota/`:** every proposal stands. Nothing is written yet.
- **Initialized:** `rota config show <key>` for each proposal. Drop a key whose current value already equals the suggestion and keep only the ones that differ, showing current and suggested value side by side. With nothing left, print `config already matches the project` and stop.

## Step 3 — Ask one at a time

One question per call or message, the recommended value first, labelled with its evidence. For example: `test.fast: go test ./... (go.mod; Makefile target "test")`. Offer the schema default or "leave unset" as an alternative, and a free-text answer.

- **Claude Code:** `AskUserQuestion`, one question per call. The recommendation first, its label ending `(Recommended)`.
- **Any other harness (Codex, OpenCode, ...):** one plain message per question, lettered `A)` to `E)`. `A)` is the recommendation. The last letter is "Write your own answer". Ask the next question only after the reply.

Silence, an unavailable picker or an unclear reply is never a yes. Ask once more, then leave the key unchanged.

## Step 4 — Write confirmed answers

Only confirmed values, only through rota verbs; never edit `.rota/config.json`.

- **No `.rota/` yet:** one `rota setup --set key=value` per answer to a key `rota setup --list` offers (`issues.source`, `work.isolation`, `round.dispatch`, `round.workerKind`, ...). If no answer is one of those, `rota init` first.
- **Everything else, and every key on an initialized project:** `rota config set <key> <value>`. List values are JSON (`rota config set test.fast '["go test ./..."]'`).

Run `rota config check` after the writes. Fix what it flags, re-run, and continue only when it is clean.

## Step 5 — Report

One line per key: `<key>: <value> (<evidence>)` for a confirmed write, `<key>: unchanged` for a declined one, `<key>: schema default (no signal)` for one not inferred.

## Thought → Reality

| Thought | Reality |
|---|---|
| "The evidence is obvious, I'll write it without asking" | Step 4 writes confirmed values only. |
| "I'll batch the questions" | One key per question; each answer is its own decision. |
| "I'll edit `.rota/config.json`, it's faster" | The write goes through `rota setup --set` or `rota config set`. |
| "No signal, so I'll guess a default" | Say the key keeps the schema default; do not propose. |

## Key Principles

- Evidence with every proposal, recommended value first.
- Nothing written unconfirmed, nothing written outside the rota verbs.
- Initialized project: show only the keys that differ.
- No new verb: the skill composes `rota setup`, `rota config` and plain repo reads.
