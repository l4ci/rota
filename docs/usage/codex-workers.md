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
- **A Codex CLI with rota's launch flags.** rota does not pin a Codex version. Before it marks anything,
  `assign` and `dispatch` check that `codex --help` lists every flag the launch line uses. A missing flag
  refuses with exit 4, `blockedBy: "codex flags"`, naming the flag. A `codex` that is not installed or will
  not run is exit 5. `--accept-codex-version` is deprecated: it warns and does nothing.
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
It must keep `--dangerously-bypass-hook-trust`: without it Codex skips the prompt check below, so
dispatch refuses it (exit 5). See [round keys](configuration.md#round-keys).

## What a Codex worker accepts

A Codex worker only takes text that rota signed. Whether a model refuses unsigned pane text depends on
the model: one Codex model answered `ROTA-BLOCKED` to an unsigned instruction, the default one followed it
([#3](https://github.com/l4ci/rota/issues/3)). So for Codex the check doesn't rely on the model:

- On each task dispatch rota writes a fresh key to `rota-prompt.key` (mode 0600) in the slot's
  `CODEX_HOME`. Every brief and relay it sends to that slot ends with a `--- ROTA-SIG <hmac> ---` line, an
  HMAC-SHA256 of the text under that key. Whitespace is ignored, so a pane that rewraps lines still
  verifies.
- The launch line adds a Codex `UserPromptSubmit` hook (`-c hooks.UserPromptSubmit=...`) that runs
  `rota worker prompt-check`. A prompt with a valid signature goes through. Anything else is blocked
  before it reaches the model, and the pane shows the reason. If the check itself can't run, the prompt
  is blocked too.
- A maintainer's answer typed into the pane with the `m:` prefix still goes through, as for Claude workers
  ([maintainer answers](parallel-rounds.md#maintainer-answers-typed-into-a-pane)). Anyone who can type in
  the pane can use that prefix; it is the one unsigned path left.

The check is a guard against text typed into the pane, not against local processes. The key is not
secret from the worker or from other processes of the same user, so anything that can read the slot's
`CODEX_HOME` can sign. What happens when the hook runs past its 30 s timeout is unknown: Codex's behaviour
there is untested.

To give a Codex worker anything else, send it through `rota worker dispatch --relay` or the assignment
brief.

## Limits

- **herdr only.** No tmux, no solo mode, no Codex subagents.
- **No usage meter.** `work.accounts` and its headroom meter are Anthropic's, and a Codex slot is skipped
  by them. The slot's `CODEX_HOME` is its account, so `rota limit watch` has nothing to switch to. Account
  switching and [usage-limit handling](unattended-rounds.md#usage-limits) apply to Claude slots.
- **A Codex update can still break a flag.** If a release renames or drops a launch flag, `assign` refuses
  and names it. Fix `work.codexCommand`, or wait for rota to follow.
- **The prompt check was verified on 0.159.2.** rota does not gate on the version, so it cannot tell a Codex
  that ignores `-c features.hooks=true`. Unsigned pane text could then reach the worker.

## Check it

`rota doctor` has a `codex` check. It skips when Codex isn't installed and no slot has a home. Otherwise it
fails when Codex is missing, its version is unreadable or out of range, a slot's home isn't logged in, or
(under herdr) the integration is missing, each with the command that fixes it. See
[doctor and reap](doctor-and-reap.md).
