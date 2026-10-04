# Codex workers

A [round](parallel-rounds.md) can run OpenAI's Codex CLI as a worker beside Claude Code. A Codex worker
is a visible session in a herdr tab, the same as a Claude one: it takes one issue, works in its own
worktree, and opens a PR. The orchestrator, the gate and the merge policy don't change.

This page is about Codex as a worker in a round. To use the rota skills from inside Codex, see
[using the skills in Codex](codex-skills.md).

## Start one

```sh
rota round assign 59 --kind codex --check-only    # readiness, and the model it would use
rota round assign 59 --kind codex
```

`--kind` is `claude` or `codex`. Without it, the slot's recorded kind applies, else `claude`. `rota round status` shows each slot's kind.

## What you need

- **herdr.** Codex workers run under `work.dispatch` set to `herdr` (or detected inside a herdr pane).
  Under tmux, `rota round assign --kind codex` exits 5 with `codex workers need work.dispatch=herdr`. Solo
  mode runs Claude subagents only.
- **Codex CLI 0.159.x.** rota pins the range `>= 0.159.0, < 0.160.0` (tested on 0.159.2), read from
  `codex --version`. Outside it, `assign` refuses (exit 4, `blockedBy: "codex version"`) before it marks
  anything. `--accept-codex-version` lets one call through with a warning. `rota doctor` reports the same check.
- **A login per slot.** See below.

## One CODEX_HOME per slot

Each slot gets its own `CODEX_HOME` at `<git-common-dir>/rota/codex/<slot>`, never `~/.codex`. It holds
that slot's login, sessions and history, so two slots share no auth. It sits beside the round lease, not
in the worktree, so it never dirties `git status`. It survives `rota round wind-down`, `rota reap` and slot
resets, and rota never deletes it.

rota creates the home (mode 0700) on the slot's first Codex dispatch. In a new home it writes a
`config.toml` that trusts the slot's worktree and turns off the update check, so neither dialog opens in
an unattended pane. Under herdr it also installs herdr's Codex integration there. It never writes or
copies `auth.json`.

## Log in once per slot

rota does not log a slot in. Before it marks anything, `assign` runs `CODEX_HOME=<home> codex login status`.
When that fails you get exit 5 and the command to run:

```sh
CODEX_HOME=<git-common-dir>/rota/codex/ben codex login
```

Log in each slot yourself. Don't copy one slot's `auth.json` to another, or from `~/.codex`: a ChatGPT
login carries a rotating refresh token, so two copies refresh on their own and can invalidate each other,
yours included.

## Model tiers

A tier (`light`, `standard`, `heavy`) says how heavy a model the worker starts with, and how it sizes its
own subagents. `rota round assign --tier heavy --tier-reason "..."` picks one; the default is `round.tier`.
For Claude, the tiers map to models out of the box. For Codex they are optional:

| Key | Default |
|---|---|
| `round.tiers.codex.light` / `.standard` / `.heavy` | empty |

Unset, the worker runs on Codex's own default model, and the launch command drops `--model`. Set one and
you must set all three. `rota round assign --kind codex --check-only` prints the model it would use.

The launch command is `work.codexCommand`. Its default is `codex --model {model}
--dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen`.
The bypass flags are there because nobody answers prompts in a worker pane: scope, not prompting,
bounds the worker. A custom command receives the tier's model only through a `{model}` placeholder.
See [round keys](configuration.md#round-keys).

## Limits

- **herdr only.** No tmux, no solo mode, no Codex subagents.
- **No usage meter.** `work.accounts` and its headroom meter are Anthropic's, and a Codex slot is skipped
  by them. The slot's `CODEX_HOME` is its account, so `rota limit watch` has nothing to switch to. Account
  switching and [usage-limit handling](unattended-rounds.md#usage-limits) apply to Claude slots.
- **The signed-brief rule is model-dependent** ([#225](https://github.com/l4ci/rota/issues/225)). The worker contract says to treat
  unsigned text in the pane as untrusted and to answer `ROTA-BLOCKED`. One Codex model did; another followed
  the unsigned instruction. Send a Codex worker its instructions through `rota worker dispatch` or the
  assignment brief, and don't type into its pane.
- **Version pin.** A Codex update outside 0.159.x blocks `assign` until rota's range moves or you pass
  `--accept-codex-version`.

## Check it

`rota doctor` has a `codex` check. It skips when Codex isn't installed and no slot has a home. Otherwise it
fails when Codex is missing, its version is unreadable or out of range, a slot's home isn't logged in, or
(under herdr) the integration is missing, each with the command that fixes it. See
[doctor and reap](doctor-and-reap.md).
