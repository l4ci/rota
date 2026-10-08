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

`--kind` is `claude` or `codex`. Without it, the issue's `harness:` label applies (file backend: a `Harness:` field), then the project default `round.workerKind`, then the slot's recorded kind, else `claude`. A Codex-first project sets `rota config set round.workerKind codex` once; autopilot, `rota round architecture` and `rota round transfer` then start Codex workers too. `rota round status` shows each slot's kind and where it came from. To pick a model per issue, add a `model:<id>` label or pass `--model <id>`; see [assigning](parallel-rounds.md#assigning-an-issue).

A `best-of:2` issue gets one Claude and one Codex attempt when both `round.tiers.claude` and `round.tiers.codex` are set and neither `--kind` nor a `harness:` label pins a harness. See [best-of](parallel-rounds.md#best-of-two-workers-on-one-issue).

## What you need

- **herdr.** Codex workers run under `work.dispatch` set to `herdr` (or detected inside a herdr pane).
  Under tmux, `rota round assign --kind codex` exits 5 with `codex workers need work.dispatch=herdr`. Solo
  mode runs Claude subagents only.
- **A Codex CLI with rota's launch flags.** rota does not pin a Codex version. Before it marks anything,
  `assign` and `dispatch` check that `codex --help` lists every flag the launch line uses. A missing flag
  refuses with exit 4, `blockedBy: "codex flags"`, naming the flag. A `codex` that is not installed or will
  not run is exit 5. `--accept-codex-version` is deprecated: it warns and does nothing.
- **A Codex login.** See below.

## Codex home and login

Codex workers use your default Codex account, the way Claude workers use your configured accounts (or the
default config dir when none are configured). A slot's own worktree is separation enough, and several Codex
processes sharing one home is ordinary Codex use. rota sets no `CODEX_HOME`, so Codex reads `~/.codex`, or the
`$CODEX_HOME` you already export. Sessions and history land there like any other Codex session.

rota does not log in for you. Before it marks anything, `assign` runs `codex login status` against that home.
When it fails you get exit 5 and the plain command:

```sh
codex login
```

Log in once. There is no per-slot login.

rota writes nothing to `config.toml` or `auth.json` in the Codex home. The two dialogs that would stall an
unattended pane are answered on the launch line instead: `-c 'projects."<worktree>".trust_level="trusted"'`
trusts the slot's worktree, and `-c check_for_update_on_startup=false` turns off the update check. Under herdr,
`assign` also checks herdr's Codex integration in that home and installs it when it is missing; that install is
herdr's own and happens once.

Each slot keeps a small state directory at `<git-common-dir>/rota/codex/<slot>` holding its prompt key (see
below). Codex never reads it. It sits beside the round lease, not in the worktree, so it never dirties
`git status`. Per-slot homes that earlier versions created in the same place are left alone; rota just no
longer points Codex at them.

### Several Codex accounts

To spread Codex workers over more than one login, list named Codex homes in `work.codexAccounts`, the
counterpart of `work.accounts`:

```json
{ "work": { "codexAccounts": [
  { "name": "personal", "codexHome": "/home/me/.codex-personal" },
  { "name": "team",     "codexHome": "/home/me/.codex-team" }
] } }
```

Paths are machine-specific, so put the array in the gitignored `.rota/config.local.json`. A slot keeps its
account while it stays configured; a new Codex slot takes the account holding the fewest other Codex slots
(ties go in config order). `rota round status` and `rota worker pool list` show it as `codexAccount`. The
pane gets that account's `CODEX_HOME`, and login is checked in each home you configured:

```sh
CODEX_HOME=/home/me/.codex-team codex login
```

There is no usage meter for Codex, so the pick balances slots, not headroom. A per-slot home exists only if
you configure it as an account.

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

- On each task dispatch rota writes a fresh key to `rota-prompt.key` (mode 0600) in the slot's state
  directory. Every brief and relay it sends to that slot ends with a `--- ROTA-SIG <hmac> ---` line, an
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
key file can sign. What happens when the hook runs past its 30 s timeout is unknown: Codex's behaviour
there is untested.

To give a Codex worker anything else, send it through `rota worker dispatch --relay` or the assignment
brief.

## Limits

- **herdr only.** No tmux, no solo mode, no Codex subagents.
- **No usage meter.** `work.accounts` and its headroom meter are Anthropic's, and a Codex slot is skipped
  by them. `work.codexAccounts` spreads slots but cannot measure headroom. `rota limit watch` reads only the
  limit message in the pane (the wording, something like `You've hit your usage limit ... try again at 3:42 PM`, is recalled, not captured from a real Codex run), then parks the slot. It
  moves the issue to an idle slot on another Codex login that has no limit waiting, or sleeps until the
  reset when there is none. With every login in `work.codexAccounts` waiting, the round stops filling Codex
  slots (see [usage limits](unattended-rounds.md#-usage-limits)).
- **A Codex update can still break a flag.** If a release renames or drops a launch flag, `assign` refuses
  and names it. Fix `work.codexCommand`, or wait for rota to follow.
- **The prompt check was verified on 0.159.2.** rota does not gate on the version, so it cannot tell a Codex
  that ignores `-c features.hooks=true`. Unsigned pane text could then reach the worker.

## Check it

`rota doctor` has a `codex` check. It skips when Codex isn't installed and no `work.codexAccounts` are
configured. Otherwise it fails when Codex is missing, `codex --version` can't be run (there is no supported version range), or a
configured account isn't logged in or (under herdr) lacks the integration, each with the command that fixes
it. With no accounts it checks the default home and only notes a missing login or integration, since a project
that never runs Codex workers has no reason to log in. See
[doctor and reap](doctor-and-reap.md).
