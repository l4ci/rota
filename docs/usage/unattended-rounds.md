# Unattended rounds

A round can run for hours with no one at the keyboard. Four pieces keep it going: hooks that make the
orchestrator hand off before its context fills, a supervisor that restarts it, a watcher that waits out
usage limits, and an opt-in switch that moves the orchestrator to another account before a limit hits.
All of it is opt-in and acts only on the orchestrator, the session that holds the
[round lease](parallel-rounds.md#starting-a-round). Workers are not touched.

What you need first is a working round: [parallel rounds](parallel-rounds.md). Without any of this,
`/rota-pause` and `/rota-work` (no argument) are the manual route; see [pausing and resuming](pausing-and-resuming.md).

| Piece | Verb | Stops | Config |
|---|---|---|---|
| Handoff hooks | `rota hook install` | an orchestrator whose context is at `handoffThreshold` | `orchestrator.handoff*` |
| Keepalive | `rota keepalive run` | nothing: it restarts the orchestrator after a handoff exit | `orchestrator.keepalive*` |
| Usage limits | `rota limit watch` | a session stalled on a 5-hour or weekly limit | `limits.*` |
| Account switch | `orchestrator.switchOnUsage` | an orchestrator close to a limit, before it hits | `orchestrator.usageThreshold` |

A typical unattended start: `rota hook install`, then in the orchestrator's pane
`rota keepalive run -- claude --model opus`. The keepalive supervisor runs the limit watcher beside the
command, so there is nothing more to start.

## Orchestrator handoff

An orchestrator's context fills over a long round. Two hooks make it hand off before that happens, with
no one at the keyboard. They act only on the orchestrator, the session that holds the
[round lease](parallel-rounds.md#starting-a-round).

### Install the hooks

Once per project:

```sh
rota hook install                      # .claude/settings.local.json, per developer, not committed
rota hook install --wrap-statusline    # when you already have a statusLine
```

`install` merges into the settings file and never replaces anything it did not write. It adds:

- a `Stop` hook (`rota hook stop`),
- a `SessionStart` hook (`rota hook session-start`),
- a statusline (`rota statusline dump`).

Hook entries end in `# rota-hook`, so a second run updates them instead of stacking copies.
`--scope project` writes `.claude/settings.json` (committed); `--scope user` writes your Claude config dir.

### Your own statusline keeps working

With a statusline already set, plain `install` refuses (exit 4). `--wrap-statusline` handles it:

- It rewrites the statusline to `rota statusline dump --then '<your command>'` and keeps the original
  beside it as `rotaWrapped`.
- The dump records the session state, then runs your command with the same input and output, so your
  bar looks the same.
- `rota hook uninstall` removes the hooks and puts your command back.
- The file is rewritten as two-space JSON. If it already is, the round trip is byte for byte.

### What happens at the threshold

1. Every statusline refresh writes the session's state (context percentage, rate limits) under the git
   common dir, as `rota/session/<session_id>.json`.
2. When the orchestrator tries to stop and the state shows `orchestrator.handoffThreshold` percent
   (default 75) or more, the Stop hook blocks. It tells the orchestrator to write
   `.rota/handoff/<base>.md` and run `/exit`.
3. If it was told twice and still wrote nothing, the hook gives up and records `handoffFailed` in the
   state, so a session that cannot write a handoff is not held forever.

The percentage comes from `context_window.used_percentage`, else from `current_usage` over
`context_window_size`.

### The next session

The SessionStart hook fires on `startup` and `clear`.

- When the new session holds the lease and the handoff exists, the hook injects the file as context and
  moves it to `<base>.md.consumed`.
- A restarted orchestrator has not run `rota round start` yet, so it holds no lease. A fresh handoff
  written by the Stop hook (first line `<!-- rota-handoff: orchestrator -->`) is injected anyway. Its
  first act is `rota round start`, then it reads the handoff.
- `resume` and `compact` keep the file.

### Checking it

`rota doctor` reports whether the statusline runs the dump and the hooks are in place (`statusline`,
`stop-hook`). The hooks are opt-in:

- Until `rota hook install` has written something, both checks skip.
- After that they fail on a partial or broken install: one hook missing, a statusline without the
  dump, or a hook command that no longer resolves.

Restarting the orchestrator after the exit is the next section; usage limits follow it. Without the
hooks, `/rota-pause` and `/rota-work` (no argument) are the manual route
([pausing and resuming](pausing-and-resuming.md)).

## Keepalive

The hooks end an orchestrator session cleanly. `rota keepalive run` starts the next one. Start the
orchestrator under it, in the pane it will own:

```sh
rota keepalive run -- claude --model opus       # everything after -- is the command
```

`rota keepalive run` is the pane's foreground process and `claude` is its child, so it learns the exit from
the child's own status and needs neither herdr nor tmux to notice it. It is a supervisor, not a watcher
of the pane. The cost: an orchestrator not started under `run` is not restarted.

### When it restarts

On every exit it looks for a fresh handoff, `.rota/handoff/<base>.md` no older than
`orchestrator.handoffMaxAgeSeconds`.

- **With one:** it waits `orchestrator.keepaliveBackoffSeconds` (5) and starts the command again with
  `orchestrator.restartPrompt` appended as the last argument. The first start never gets the prompt.
- **Without one:** it stops. That is how `/exit` from you ends the loop.

### The lease

`run` takes the round lease and holds it across restarts, and tells its child through
`ROTA_ROUND_HOLDER_PID`. So:

- `rota round start` and the hooks inside the orchestrator see the supervisor as the holder.
- The round number survives a restart.
- The SessionStart hook injects the handoff at once, without `rota round start` first.
- A second `run`, or an orchestrator started by hand, is refused while it lives (`rota round start` exits 4).

`rota keepalive status` shows the supervisor and the lease. A supervisor killed with SIGKILL leaves a stale
lease, which the next `run` reclaims.

### The breaker

A restarted orchestrator that dies before it reads the handoff (bad auth, a hook error) leaves the same
file behind. `run` counts a restart as progress only when the handoff changed: a new write with different
content.

- After `orchestrator.keepaliveBreaker` (3) restarts in a row without progress, it stops.
- After `orchestrator.keepaliveMaxRestarts` (10) restarts in total, it stops regardless.

Both stops post an escalation comment on issue `orchestrator.escalateIssue` through
`rota round escalate send` and raise the herdr notification. With `escalateIssue` unset, which is the
default, you get the notification and a warning only. The handoff is kept; fix the cause and run it again.

### How it stops

The supervisor stops:

- when the orchestrator leaves no fresh handoff (`no-handoff`),
- on the breaker or the restart limit (exit 1),
- when you interrupt it: SIGINT and SIGTERM go to the child, the supervisor waits for it and does not
  restart (`interrupted`).

It then releases the lease and records `status: stopped` in `<git-common-dir>/rota/keepalive.json`. It
never deletes the handoff; only the SessionStart hook consumes it.

### Flags

`--max-restarts`, `--breaker`, `--backoff` and `--prompt` override the config for one run.
`--no-limits` leaves out the usage-limit watcher the supervisor otherwise runs beside the command (see
[usage limits](#usage-limits)). `--json` prints one envelope when the loop ends, not before.

## Usage limits

A 5-hour or weekly usage limit stops a session until the window resets. `rota limit watch` keeps a round
from stalling on that: it notices the limit, waits for the reset, and types a resume prompt into the pane.

Under `rota keepalive run` the same loop runs inside the supervisor, so there is nothing more to start. For
an orchestrator not started under `run`, start the verb in the background or in a pane of its own:

```sh
rota limit watch            # blocks for the life of the round
rota limit status           # the log, and whether anything is watching
```

`watch` needs the round lease, because moving work between accounts is the orchestrator's act. It
refuses to start (exit 4) without it, under a live `rota keepalive run` (which already watches), or beside
another watcher.

### How it notices

- **The orchestrator:** it reads the rate limits the statusline dump stores. A window at 100 percent with
  its reset still ahead is a limit; the later reset wins if both are.
- **A worker slot:** it reads the account meter (`rota worker account list`), but only after the slot's
  pane shows a limit message, never on a timer.
- **The message as fallback:** on herdr 0.9.x the loop subscribes to `pane.output_matched` with the
  phrases `rota worker poll` already uses for LIMITED. On tmux, or if herdr refuses the subscription, it
  captures the panes every `--settle` seconds.
- **No data behind a message:** the reset time comes from the text (`resets at 3pm` reads as the next 3pm
  in your time zone, within 8 days). Otherwise it sleeps `limits.fallbackSleepSeconds`.

"Approaching your usage limit" is a warning and starts nothing.

### Sleep or switch

`limits.mode` is `switch` (default) or `sleep`.

- **`sleep`** waits for the reset.
- **`switch`** applies to a worker slot only. It keeps the slot's account unless it is cooling, otherwise
  takes the account with the most headroom (`rota worker account pick --exclude <account>`), the rule
  `rota round assign` uses. With such an account and an idle slot on it, the slot's issue moves there with
  `rota round transfer`, so the work continues from its pushed branch and a handoff comment. With no usable
  account, or no idle slot on it, the limit sleeps instead and the entry says why.
- **The orchestrator only sleeps.** A limited session cannot write a handoff, and a restarted one with no
  handoff has nothing to continue from. Moving it to another account is opt-in and happens before the
  limit: see [switching the orchestrator's account](#switching-the-orchestrators-account).

### Resuming

At the reset plus `limits.resumeMarginSeconds` (60) the loop types `limits.resumePrompt` into the pane and
keeps watching it. A pane still limited after the prompt starts another cycle, up to `limits.maxResumes`
(3) for one limit. Past that the entry is `failed` and the loop posts an escalation on issue
`orchestrator.escalateIssue`, or raises a host notification when that is unset.

Stop the watcher with Ctrl-C or SIGTERM. The waiting entries stay waiting, and the next watcher resumes
any whose reset has already passed.

### The log

The `limits` list in `.rota/workers.json`, beside `slots` and `escalations`. Each entry (`l1`, `l2`, ...)
records:

- the session (`orchestrator` or a slot) and the window,
- whether the reset came from data or text, and when it resets,
- the action, its status (`waiting`, `resumed`, `switched` or `failed`) and a note.

`rota limit status` reads it back. `rota round status` and `rota round reconcile` list the entries still waiting.
Nothing prunes resolved ones.

### Config

Five keys under `limits`, all silent defaults: `mode` (`switch`), `resumeMarginSeconds` (60),
`fallbackSleepSeconds` (1800), `maxResumes` (3) and `resumePrompt` (`The usage limit has reset. Continue
where you left off.`). See [configuration](configuration.md#limits-keys).

## Switching the orchestrator's account

Off by default. With `orchestrator.switchOnUsage` set to `true`, an orchestrator that is close to its usage
limit hands off and restarts under another account, instead of running into the limit and sleeping. The
usage-limit sleep stays the behavior when the key is off, and for anything the switch does not cover.

```sh
rota config set orchestrator.switchOnUsage true
rota config set orchestrator.usageThreshold 90      # percent, the default
```

### What it needs

- The Stop hook installed (`rota hook install`).
- The orchestrator started under `rota keepalive run`. Without a supervisor nothing would restart it, so
  the hook does not ask.
- At least two accounts with a `configDir` in `work.accounts`.
- The statusline dump on the account it moves to, which the `statusline` check covers.

`rota doctor` has a `switch` check, but it covers only the Stop hook and two accounts with a `configDir`. It
cannot tell whether the orchestrator runs under `rota keepalive run`.

### At the threshold

When the larger of the 5-hour and weekly `used_percentage` reaches `orchestrator.usageThreshold`, the Stop
hook blocks the way it does for context. It asks for the handoff and `/exit`, and records which window
tripped. A session with no rate-limit reading (API billing) is never asked.

### The restart

When the orchestrator exits with that handoff, the supervisor picks the account. It never keeps the current
one. The target is the account that:

- has the most headroom and a `free` meter (an `unknown` meter is not taken),
- has a `configDir`,
- has headroom above `100 - usageThreshold`.

The restarted `claude` runs with `CLAUDE_CONFIG_DIR` set to that account, and every later restart of the same
run keeps it. It reads its handoff through the SessionStart hook as after any restart. The switch counts as a
restart against `orchestrator.keepaliveMaxRestarts`.

### With no usable account

The supervisor restarts at once on the same account and holds the switch until the window's reset
(`switchHold` in `keepalive.json`; `limits.fallbackSleepSeconds` when there is no reset time).

- While the hold lasts, the hook passes on usage, so the session works on and the in-session limit sleep
  handles the real limit at 100 percent.
- A context handoff is not held.
- A hold with no reset time lasts `limits.fallbackSleepSeconds` (30 minutes by default). While usage stays
  at or above the threshold, handoffs repeat after each hold until `orchestrator.keepaliveMaxRestarts` stops
  the supervisor.

### The figure is not a limit

The threshold reads the session file's `used_percentage`, which carries no `extra_usage` information. A
weekly window at the threshold counts even when extra usage is enabled and would keep the account going. For
an opt-in this is the simple choice; set `usageThreshold` to 100 to wait for the window to fill.

### The log

Each decision is an entry in the `limits` list:

- `action` `switch` (`status` `switched`): the account left is in `account`, the account switched to in `note`.
- `action` `restart` (`status` `resumed`): no account was usable; `note` holds why and the hold's end.

`rota limit status` shows them. `keepalive.json` gains `account`, `switches` and `switchHold`.

The herdr agent integration is per account (`herdr integration install claude` with that
`CLAUDE_CONFIG_DIR`); `rota doctor`'s `hook` check covers it. The project-local hooks apply to every account.
