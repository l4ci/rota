# Parallel rounds

A round is one orchestrator session plus two to five workers. Each worker is a standing agent in
its own git worktree and terminal tab (herdr or tmux), or an in-harness subagent when there is no
terminal host ([solo mode](#solo-mode)). Each holds one backlog item at a time: a GitHub or GitLab issue, or a `.rota/BACKLOG.md` item on the file backend ([issue backend](issue-backend.md)). Workers implement,
verify and open a PR; they never merge. The orchestrator assigns issues, relays decisions, merges
PRs. The merge gate is the only full verification run.

The orchestrator runs the `rota-orchestrate` skill, which holds the judgment: which issues, how to
read a stuck worker, what to escalate, when to merge. The mechanics are `rota round` verbs, so a round
never polls inside the orchestrator's context. A worker reads
[`skills/references/worker-contract.md`](../../skills/references/worker-contract.md); `rota round assign` points it there.

Running a round on rota itself, with its gate and repo rules, is in
[contributing: rounds](../contributing/rounds.md).

## Round or `/rota-work`

| | [`/rota-work`](running-work.md) | A round |
|---|---|---|
| Sessions | one, with subagents it launches per task | an orchestrator plus standing workers |
| Isolation | a branch or one worktree per cycle | one worktree per worker, reused across issues |
| Unit of work | an item or a batch you pick | one issue at a time per worker |
| Who merges | the session, or a PR you open | the orchestrator, after its own gate |
| Runs unattended | until the context fills | for hours, across restarts ([unattended rounds](unattended-rounds.md)) |
| Setup | none | a host, optionally several accounts, `rota doctor` |

Use `/rota-work` for a handful of items you are watching. Use a round when you have a queue of
well-specified issues that don't touch the same files and you want them merged without you steering
each one. If you run several `/rota-work` sessions by hand today, see [parallel work](parallel-work.md);
a round is the same idea with the assignment, waiting and merging done by verbs.

## 🔧 Setup

1. **A host** for the workers' tabs: herdr (pinned to 0.9.x) or tmux. Leave
   `round.dispatch` at the default `rota init` writes (`subagent`) and `rota round start` detects the
   host from where it runs, falling back to [solo mode](#solo-mode). Set `herdr` or `tmux` only
   to force one.
2. **A backlog.** With `issues.source` set to `github` or `gitlab`, rounds take GitHub (`gh`) or GitLab (`glab`) issues, authenticated. On the file backend (the default) they take `.rota/BACKLOG.md` items and the orchestrator completes them at merge. Either way, workers open a PR against the base and the merge gate merges through the forge, so a project with an `origin` remote needs `gh` or `glab` installed and logged in on the file backend too (`rota doctor` checks it). With `forge.provider: local` (or no `origin` remote under `auto`) there is no PR: workers merge directly (`rota ship strategy` answers `direct`) and the gate never looks for one. See [issue backend](issue-backend.md).
3. **Accounts, if you have more than one.** `round.accounts` maps slots to separate
   config homes (`kind: "claude"` entries set `CLAUDE_CONFIG_DIR`), so workers draw on different usage limits and `rota round assign` can avoid a
   cooling account. The entries are paths on your machine, so they go in `.rota/config.local.json`
   (per developer, gitignored, merged over `.rota/config.json`), not in the tracked file.
   `rota config set` writes the tracked file, so edit this one by hand:

   ```json
   {
     "round": {
       "accounts": [
         { "kind": "claude", "name": "a", "dir": "/path/to/claude-a" },
         { "kind": "claude", "name": "b", "dir": "/path/to/claude-b" }
       ]
     }
   }
   ```

   `rota worker account list` then shows each Claude account (`kind` is always `claude`: only those have a usage meter) with its usage verdict.

4. **Issues a worker can pick up.** A candidate needs acceptance criteria or a design or plan note,
   closed dependencies and no file overlap with work in flight (see [Picking issues](#picking-issues)).
5. **Subagent definitions.** `rota init` runs `rota agents write`, which writes
   `.claude/agents/rota-*.md` (and `.codex/agents/rota-*.toml` when Codex is configured): three roles,
   `explorer`, `implementer` and `reasoner`, each on a model tier. Workers hand reading to the
   explorer, code to the implementer and hard thinking to the reasoner by name. Change a role's tier
   or effort with the `roles.*` keys (see [roles keys](configuration.md#roles-keys-and-rota-agents-write)),
   then run `rota agents write` again. `rota agents write --check` writes nothing and exits 1 when a
   file is missing or stale.
6. **Preflight.** `rota doctor` checks all of the above and names the fix for each failure. See
   [doctor and reap](doctor-and-reap.md).

For a round that must survive the orchestrator's context filling or a usage limit, also install the
hooks and run the orchestrator under `rota keepalive run`:
[unattended rounds](unattended-rounds.md). For Codex workers: [Codex workers](codex-workers.md).

## 🚀 Launching a round

```sh
rota orchestrate
```

Bare `rota` in a terminal opens a palette with Orchestrate preselected, so `rota` then Enter does the
same. Arrow keys or `j`/`k` move, a digit runs that entry, typing filters by name, `Esc` clears the
filter or quits, and `q` or Ctrl-C quits. The other entries are round status, a View toggle, doctor, skills update,
projects and config. Round status, doctor, projects and config open their screen inside the palette, and `q` or `Esc`
returns to the list. Skills update prints its output, then any key returns. The View toggle switches a herdr round
between split and tabs ([layout](#layout)). Its label marks the layout in force, and it is hidden under tmux or when no
round is running. Outside an initialized
project Setup is preselected and the project entries are hidden. A pipe, `--json` or any argument skips the
palette, so scripts and agents see no extra output. `NO_COLOR` and `TERM=dumb` are honored; a dumb
terminal gets a numbered `choice:` prompt.

That runs `rota doctor`, stops if a check fails, then opens an orchestrator session that has already
started `/rota-orchestrate`. Nothing is typed into a pane. The session runs under
`rota keepalive run`, so it restarts from a handoff when its context fills.

With the `claude` harness it starts under the account in your `CLAUDE_CONFIG_DIR` if that is set.
Otherwise, if `round.accounts` is configured, it takes the account with the most headroom (the first
one when no meter is readable). `rota orchestrate --dry-run` names it.

Where it opens depends on where you ran it:

| You are | The orchestrator opens |
|---|---|
| In a herdr pane | a new focused tab in that workspace |
| In tmux | a new window in that session |
| In neither, herdr installed | a herdr session `rota-<dir>` with the orchestrator in its first workspace (your terminal attaches to it) |
| In neither, only tmux installed | a tmux session `rota-<dir>` (your terminal becomes it) |
| In neither, no herdr or tmux | in this terminal; workers run as subagents ([solo mode](#solo-mode)) |

`round.dispatch: tmux` skips the herdr-session row and uses tmux. The herdr session is rota's own,
started with `herdr --session rota-<dir> server`; your default herdr session is never driven.
The agent comes from `orchestrator.harness` (`claude`, `codex`, `hermes` or `opencode`; see
[orchestrator harnesses](orchestrator-harnesses.md)). `rota orchestrate --dry-run`
shows what would start.

On herdr, each worker's sidebar line reads `<slot> · <task> · <state>` (for example `ben · #494 · working`),
and your workspace carries a `round` token with the round number and slot counts. Both are display-only
labels under the source `rota`, updated when rota records a state change and cleared when a slot is parked
or reaped. If herdr rejects one, rota prints a single note and carries on. tmux shows no labels.

The orchestrator then does the steps below. To drive a round by hand instead:

```sh
rota doctor                                  # fix every fail it reports
rota round start --slots 2                   # take the lease, make two slots, list candidates
rota round assign 59 --body-file notes.md    # first idle slot takes issue 59; repeat for a second issue
rota round wait                              # blocks until a worker is done, blocked or dead
rota worker gate ben --base main             # verify on the merged tree, merge on a pass
rota round wind-down                         # re-verify main, park the slots, release the lease
rota reap --apply                            # delete the merged branches and leftovers
```

`rota round wait` returns the slot that needs you, so loop `wait`, `assign` and `gate` until no work is
left. After every `wait`, give each free slot its next issue before you review: a slot whose worker
reported done with a PR is free, and `assign` moves its PR to the round's review list. Replace `ben` with the
slot `wait` named, or gate a PR in review by number (`rota worker gate 61 --base main`). Preview `rota reap` without
`--apply` first.

## The round flow

| Step | Verb | What it does |
|---|---|---|
| Check | `rota doctor` | git, jq, host, tracker auth, accounts, herdr hooks, `rota` version, Codex; each failure carries its fix |
| Start | `rota round start` | takes the repo's orchestrator lease, provisions slots, lists ready candidates |
| Pick | `rota round candidates` | the open items that pass the criteria, dependency and overlap checks |
| Review | `rota round architecture` | after `round.architectureEvery` closed non-refactor items, or when a slot is idle and nothing is assignable (at least `round.architectureIdleMinItems` closed and `round.architectureIdleMinHours` since the last review; not in a slate round), mints one architecture-review item per area and assigns them to idle slots |
| Assign | `rota round assign <ID>` | claims the item, marks it in progress, cuts `<agent>/<issue>-<slug>`, starts the worker with a signed pointer brief |
| Wait | `rota round wait` | blocks until one slot needs the orchestrator, then returns it; never poll |
| Watch | `rota round watch` | the background form of `wait`: run it as a background command and it exits when a slot, PR or escalation changes, or at a heartbeat |
| Look | `rota round status`, `rota round reconcile` | the round's rows and drift; `reconcile --apply` repairs what is safe |
| Cost | `rota round summary` | per-issue, per-slot and per-account totals for a round, from the ledger and the gate audit log |
| Ask | `rota round escalate send`, `rota round escalate check` | puts a question to the maintainer on the issue or PR thread and reads the answer |
| Adopt | `rota worker adopt <branch\|worktree> --issue <N>` | registers work another tool started as a hostless slot, after the same overlap check as `assign`; see [adopting](#adopting-work-another-tool-started) |
| Merge | `rota worker gate <slot\|#PR> --base <branch>` | verifies on the merged tree, merges on a pass; a [merge approval](#merge-approval) policy can require a human first |
| View | `rota layout split`, `rota layout tabs` | folds the workers' herdr panes and the orchestrator into one `rota` tab, or back into tabs; see [layout](#layout) |
| Clean | `rota reap` | lists, then with `--apply` removes, what no live slot owns; never kills a running agent |
| End | `rota round wind-down` | re-verifies the base, parks every slot, releases the lease, then prints the round's summary |

Each verb's arguments, data and exit codes are in `docs/contributing/contract/`. Every verb
takes `--json` for a machine-readable envelope.

## The status screen

`rota round status --ui` (also Round status in the palette) is a read-only overview of the round, so you can watch it
without keeping the issue tracker open. It opens at once on the slots in the registry, then loads the host, tracker and
quota in the background; keys, scrolling and `q` work while a read is slow, and leaving the screen cancels it. It
refreshes about every five seconds, one read at a time. The header says `loading…` until the first read finishes, then
`updated HH:MM:SS`, plus `refreshing…` while a pass runs. A pass that fails keeps the last good rows and their
time, and shows `refresh failed` beneath them.

One table groups the issues:

| Group | What is in it |
|---|---|
| Active | an issue a slot holds and is still working, idle or unavailable |
| PR review | a PR ready for you: its worker reported done and the PR is open, or the worker moved on (`moved on`). Merged and closed PRs leave |
| Support queue | issues offered to the support machine (`round.support` set): `waiting` until it claims one, `active` with its claim, `unknown` when the claim could not be read. Remote workers and models are `unknown`, never guessed from the local roster |
| Upcoming | the candidates, `ready` or `blocked` with the reason in the detail pane |

Above the table, `workers N known` counts this machine's slots (working, in review, idle, parked, unavailable) and the
`support:` line counts the queue. Remote workers are not counted. Columns are issue, title, worker, status,
harness/model and PR. Selection is the cyan `›` of the main menu, and the status text carries the state in green, yellow
or red; every state has a text label, so `NO_COLOR` loses nothing.

Issue and PR cells are separate terminal hyperlinks: use your terminal's usual Ctrl/Cmd-click. Without hyperlink
support, `i` opens the selected issue and `p` its PR in the browser, and the detail pane shows both full URLs. GitHub and
GitLab (including nested groups and self-hosted) work the same. The screen never assigns, edits issues, changes
workers or reconciles; opening a link changes nothing in the round.

## Starting a round

`rota round start` takes the orchestrator lease, provisions the roster slots and lists the
candidates; it starts no agent.

```sh
rota round start --slots 3                    # default round.slots (3); scope from round.scope (default milestone)
rota round start --scope slate --items 12,13  # only these issues
rota round start --scope open                 # every open issue, as it becomes ready
rota round candidates                         # re-read the board with readiness checks
```

- **One orchestrator per repo.** A second `start` is refused (exit 4) and names the holder. A lease
  whose holder is gone is stale: `rota round reconcile` reports it and `rota round start` reclaims it.
  How the lease is stored and who counts as the holder: [lease internals](#internals).
- **Slots** are the first `--slots` names of `round.roster` (default `ben`, `dana`, `nia`, `kit`),
  each `.worktrees/<agent>` on `park/<agent>`. Slots are provisioned once and reused; a healthy
  existing slot is left alone. The worktrees live in the project root under `.worktrees/`, which
  `rota init` adds to `.gitignore`.
- **Scope** is which issues the round may take: `slate` (only `--items`), `milestone` (the open
  items of the active milestones), `next` (the same, then the next planned milestone whose
  dependencies shipped) or `open` (every open item nobody outside the round has claimed or labelled in progress; readiness and
  overlap decide the order, so a
  round takes newly ready issues without a restart). With no unfinished milestone, `milestone` and `next` offer every open item too. An empty list says why and which command to run. Assign refuses anything outside it. Running
  `start` again in the same round keeps the recorded scope and slate unless you pass `--scope`;
  `--scope slate --items …` replaces the slate. See [round keys](configuration.md#round-keys).

### Picking issues

Candidates carry three checks:

- `criteria`: acceptance criteria, or a design or plan note. Acceptance criteria in the `assign --body-file` text count too.
- `dependencies`: every `## Depends on` reference is closed. One that cannot be looked up fails the
  check, so fix the issue text.
- `overlap`: no shared file with an in-flight slot, from a `## Files` section or the paths the issue
  text names plus the slot's real changes. A `## Touches` section (one bullet per symbol, endpoint,
  schema, migration or config key) adds a scope check: two issues naming the same entry (compared
  exactly, ignoring case and surrounding spaces) clash even in different files, and the overlap is
  listed with a `scopes` field. Without `## Touches` on either side, two issues with the same
  `Subsystem:` field clash on `subsystem:<name>`. By default a scope clash only warns and the item
  stays ready; `round.scopeOverlap: block` makes it fail like a shared file. `--accept-overlap` skips
  both. `assign` copies the issue's `## Touches` into the worker brief after `## Out of scope`.

Every candidate has one of four scope sources, shown as `scopeSource` in `rota round candidates --json`:

- `touches`: the issue has a `## Touches` section.
- `subsystem`: no `## Touches`, but a `Subsystem:` field, which gives the coarse scope `subsystem:<name>`.
- `inferred`: neither, but an explorer recorded entries with `rota round scope <ID> --body-file <path>` (at most ten bullets: symbols, endpoints, config keys, tests, then a last line `scoped.`; a reply ending `too-vague.` or `no-match.` is refused with exit 4). `/rota-orchestrate` dispatches one `light` explorer per `none` candidate and records the result before it assigns. The record is a comment of kind `scope` on the issue; a later `## Touches` or `Subsystem:` wins over it, and the declared section is never edited.
- `none`: nothing declared or recorded; only file paths are compared.

`candidates`, `assign`, `transfer` and `adopt` compare inferred entries like declared ones, but a clash that involves an inferred entry on either side only warns, even under `round.scopeOverlap: block`. The overlap detail says `inferred scopes`, and the JSON overlap carries `inferred: true`. A shared file path still blocks as before.

The overlap check cannot see files an issue will create, paths nobody wrote down, two issues editing
the same function, generated files every issue touches (list those in `round.sharedPaths`), renames,
or another machine's round. Read two issues' bodies before you run them side by side.

A directory in an issue's `## Pointers` (`src/app/ui/`) matches every file below it, so it overlaps almost any other issue. The overlap detail then says `from directory src/app/ui/: name exact files in the pointers`, and the JSON overlap lists the directories under `dirs`. Name the files in the issue, or add a file every issue mentions in a footer to `round.sharedPaths`.

## Assigning an issue

```sh
rota round assign 59 --check-only             # readiness only; exit 1 when not ready
rota round assign 59 --agent ben --body-file decisions.md --siblings 58,60,62 \
  --decision-topics "Architecture,Rounds: Orchestration"
```

Without `--agent` the first idle roster slot takes it, else the first slot whose worker is done with
a PR (see [PRs in review](#prs-in-review)). Assign refuses (exit 4, `blockedBy`)
with `no round`, `out of scope`, `not ready`, `overlap`, `claimed`, `open PR`, `slot busy`,
`no free slot`, `brief missing`, `no tier map` (`round.tiers.<kind>` has no model for the tier),
`codex flags` (the Codex launch line uses a flag `codex --help` doesn't list), `harness label`,
`model label` (a bad `harness:` or `model:` label), `best-of label` or `best-of-slots` (see
[best-of](#best-of-two-workers-on-one-issue)), and marks nothing in those cases. When it goes through it
claims the item (`<agent>@<round>`), sets it in progress with a comment, cuts the slot's
branch `<agent>/<issue>-<slug>`, picks the account and dispatches a short signed brief: a
pointer to the standing contract (`round.brief`, else `skills/references/worker-contract.md`), the
issue to read and dispute, the item's `## Out of scope` section when it has one, the siblings and the decisions from `--body-file`.

`--decision-topics` takes comma-separated DECISIONS.md topics, using the same matching rules as
`rota decisions query`. The brief includes each matching active entry in full, including its
Forbids and Permits; retired and superseded entries are excluded. Unknown topics are a usage error.
Without this flag, the brief lists the available topics and asks the worker to query those its issue
touches before implementing. A missing or empty DECISIONS.md adds no text. The assignment stores this
context so re-briefing, resuming and transfer keep the same entries even if DECISIONS.md later changes.
Workers escalate a conflict between the issue and a binding decision before implementing.

Concurrent assignments reserve the slot under the registry lock. If another manual assign or
autopilot tick has taken the slot since planning, the later assign exits 4 with
`blockedBy: "slot busy"` and releases its claim without changing the winner's binding.
The autopilot skips that refusal and continues to the next candidate.

- **Milestone base.** An issue tagged to an active milestone is cut from `origin/milestone/<id>`
  and its PR targets that branch; see [milestone branches](vision-and-plans.md#milestone-branches).
- **Tier.** `--tier light|standard|heavy` picks the worker's model tier (default `round.tier`); a
  tier above the default needs `--tier-reason`. The tier and its model are recorded on the slot,
  shown by `rota round status`, and named in the brief with the tier table for the worker's own
  subagents. See [round keys](configuration.md#round-keys).
- **Profile.** `round.profile` fills the Claude tier models in one setting; `rota config show`
  reports the effective model of every tier and where it came from. An explicit
  `round.tiers.claude.<tier>` beats the profile for that tier; with no profile set, the default tiers
  apply. Codex and OpenCode tiers are never touched.

  | Profile | light | standard | heavy |
  | --- | --- | --- | --- |
  | `quality` | sonnet | opus | opus |
  | `balanced` | haiku | sonnet | opus |
  | `budget` | haiku | haiku | sonnet |
  | `inherit` | empty | empty | empty |

  `inherit` launches workers without `--model`, so they run on whatever model the session's provider
  picks (a non-Anthropic provider, say).
- **Kind.** `--kind codex` starts a Codex worker instead of a Claude one; see
  [Codex workers](codex-workers.md). An issue can ask for its own harness and model: label it
  `harness:claude` or `harness:codex`, and optionally `model:<id>` (file backend: end the bullet with
  `Harness: codex Model: <id>`). Harness: `--kind`, then the label, then the first entry of `round.harnesses` (the project default), then the
  slot's kind, else `claude` (a slot resuming its own issue keeps its kind ahead of the project default); `kindSource` in `round status` and `assign --check-only` names the winner. Model: `--model <id>`, then the label, then the tier map, else the harness default.
  An unknown harness, two labels of one key, or a model id with anything beyond letters, digits and
  `. _ : / @ -` refuses with exit 4 (`blockedBy` `harness label` or `model label`). A model named by
  flag or label needs a `{model}` placeholder in the harness's command. `round candidates` and
  `round status` show the choice.
  **Quota fallback.** List the harnesses a round may use in `round.harnesses` (`[claude, codex]`);
  the first is the project default. When every account of an
  issue's harness is cooling down and a later entry has headroom, `assign` and the tick give the
  issue to that harness (`kindSource` `quota fallback`), label it `harness:<kind>` (file backend:
  the `Harness:` field) and record `fallbackFrom` in the ledger. The label makes the pick explicit
  for a later re-assign. An issue pinned by `--kind`, a `harness:` label, `--model` or a `model:`
  label never falls back and is refused with `blockedBy: quota`. `round status` and
  `round candidates` show `fallback` while it is active, with the first harness's reset time.
  Unset: only the default harness.
- **Overlap.** `--accept-overlap` skips the file-overlap check only. Say which PR merges first in
  the second worker's brief.
- **Open PR.** An issue an open PR already resolves (head branch `<agent>/<issue>-*` or `Closes #N`)
  is `not ready: waiting on open PR #M` in `round candidates`, and `assign` refuses it. `--accept-open-pr`
  assigns it again for a deliberate redo.
- **Deferred follow-up.** A follow-up item filed by `rota verdict add --file-deferred` is
  `not ready: depends on PR #N` while its source PR is open. It turns ready when that PR merges
  or is closed without merging.
- **Batch.** `round.batch` N (default `0`, no limit) caps the items open at once: assigned or
  queued as a PR, and not yet merged, closed or handed to the human. At N, `assign` exits 4 with
  `blockedBy: batch` until one lands or parks; `--accept-batch` overrides it for one call. Use it to
  land a round in small batches below the slot count.
- **Failure.** A failure before dispatch undoes the claim and state; one at or after dispatch keeps
  them, and repeating the call resumes.
- **Smoke section.** When the issue's text asks for a smoke section (it says "smoke section" or names
  `test/sections`) and the project has a `test/sections` directory, assign reserves the next free
  section number for that worker and names it in the brief ("Your smoke section ... is number N").
  Free means above every number on the base, on every open PR branch, on every in-flight slot
  branch and in every other reservation not yet pushed. Two workers in one round never pick the
  same number. A resumed assign keeps the slot's number.

## Waiting on workers

`rota round wait [<slot>...] [--timeout <s>]` blocks until a worker needs attention and prints
the slot and its state as JSON, so the orchestrator never polls in its own context. It
classifies with the same rules as `rota worker poll` (sentinels, `limited`, `dead`, then the
host's status). It records the slot it returns as `rota worker poll` would (its state, and the PR
from `ROTA-DONE`) and marks it seen: the next `wait` skips that slot until its state changes, so a
done slot whose PR you are holding does not come back on every call. A dispatch or relay re-arms it,
and a `ROTA-DONE` the worker printed before that relay no longer reads as done: only a new sentinel
or a `worker done` event after the relay does.
With no slot named it watches every slot that has a session and whose recorded state is not `idle`;
`rota worker dispatch` arms a slot.

Workers also report through verbs, not only through the pane. `rota worker done` and
`rota worker reply` record an event on the slot, and `rota worker send <slot> --blocked <question>`
(or `--question`) does the same for a question, so `round wait` returns it even when the pane
shows an old `ROTA-DONE` line. `rota worker send <slot> --heartbeat` refreshes the stall clock
during a long test run and changes nothing else. A slot whose host cannot be asked (herdr
unreachable, a stale tab handle after a layout split, a pane that fails to capture) reads
`unverifiable` with the reason, not `dead`: `wait` keeps waiting through it and returns it once
after `round.stallMinutes`, `rota reap` leaves it alone, and `round reclaim` refuses it without
`--force`. A host that answers "no agent in this tab" is still `dead`. A worker event (other than a
heartbeat) whose timestamp cannot be parsed marks a slot that is not busy, dead or limited as
`blocked`, with evidence that names the event and the error, instead of being ignored; the stall
clock keeps the poll time in that case.

- **herdr**: pinned to **0.9.x** (built against 0.9.3, socket protocol 22); another minor
  exits 5. One `events.subscribe` over `HERDR_SOCKET_PATH` carries a
  `pane.agent_status_changed` subscription per watched pane, so N slots cost one
  connection.
- **tmux**: no event stream and no agent status, so the verb re-captures the panes every
  `--settle` seconds (default 5) inside its own process.
- **Timeout** exits 1 with `data.timedOut: true` and every slot's state; it is an answer,
  not a fault. `--timeout` defaults to 0, which waits indefinitely.
- **Long waits in Claude Code** hit the Bash tool's timeout; see [internals](#internals).

When a returned slot is `done` with a PR and a candidate is ready, `round wait` and `round watch` add an
`assign before you gate` line with the candidate and its `rota round assign` command (`assignNudge` in
JSON), so the slot works while you review. When two or more reviewed PRs wait on one base they add a
`nudge train` line with the PR numbers and the `rota worker train` command (`trainNudge` in JSON). A PR
is left out when it has a recorded FAIL review or second-opinion verdict, when its review has a
blocking finding, when the gate sent it back at its current head (a push clears that), or when
it is already merged or closed. The autopilot assigns by itself, so
a watch running with `--autopilot` skips the assign line but still prints the train line.

`rota worker dispatch` and `--relay` check what is on the prompt line before they type. A typed human
draft is refused as a draft (exit 5, nothing sent). The autocompact settings picker is dismissed once
with Escape and the send is retried. Any other Claude Code dialog, or a picker that survives Escape, is
refused with exit 5 and `blockedBy: dialog`, and the message quotes the dialog's first line. This holds
on herdr and tmux.

`--request-id` receipts also cover `round return`, `round report`, `round tick` and `round escalate send`;
see the [verb reference](../reference/cli-helpers.md). Two calls with one id run the mutation once: the
second waits, then replays. Reuse an id for `round tick` and you get the first tick back, so use a fresh id
per tick.

## Staying reachable while you talk

`rota round wait` blocks the orchestrator's own session, so it cannot run while the orchestrator is
answering you. Run `rota round watch` as a background command instead: it exits with JSON when a
slot needs attention, a PR or an escalation changes, or after `--heartbeat` seconds (default 600) with
nothing to report, and the harness wakes the orchestrator when it exits. Keep exactly one running and
re-arm it after every wake; a second one is refused (exit 4). The registry is read every `--poll`
seconds and the forge every `--forge-poll`; `reason` in the result says which woke it.
The exit digest says the watch is exiting and must be re-armed; the prompt hook reports
whether a watch is currently armed.

With `rota hook install` the orchestrator is held to this. The Stop hook refuses to let a lease-holding
orchestrator go idle while workers are active and no watch is running, and the prompt hook adds a
one-line digest of the round (and a reminder when no watch is armed) to every message you send.
Neither applies to a solo round, which has no panes to watch.

### Worker guard

`rota hook install` also adds a `PreToolUse` hook on `Bash|Edit|Write` (`rota hook guard`). Edit and Write recall [path-scoped knowledge and decisions](learning.md#recall-before-editing) in any session. For Bash it reads `ROTA_SLOT`,
which every worker tab has and the orchestrator and your own sessions do not, and does nothing without
it. For a worker it refuses (exit 2, one-line reason) the commands the worker contract forbids:

- `gh pr merge`
- a force-push (`--force`, `-f`, `--force-with-lease`, `+ref`) to any branch but the worker's own,
  `git push --delete`, `:ref` and `--mirror`; pushing the current branch, with or without
  `--force-with-lease`, is fine
- `git reset --hard` outside the slot's own worktree
- `git branch -D` and `-d -f`
- `git clean -f`
- `git add -A`, `--all` and `.`

It parses the command line, so `git -C <dir> push -f`, `/usr/bin/git`, `sudo`, `env`, `timeout`, `xargs`,
`bash -c '…'`, `$(…)` and `a && b` chains are all seen; text inside an argument or a commit message is not.
When the line mentions `git` or `gh` and the guard cannot parse it, or cannot read the branch or worktree
it needs, it refuses and asks for the plain command.

Install it in `project` or `user` scope (`rota hook install --scope project`): a worker's worktree does
not read the untracked `.claude/settings.local.json`. `install` warns when the guard sits only there, and
`rota doctor` fails its `guard-hook` check.

Limits: this is a guardrail against a mistaken agent, not a security boundary. A script or `python -c`
that calls git, or a binary that shells out, is not seen, and `gh api …/merge` is not blocked. Codex
workers have no guard (`doctor` warns when Codex tiers are set); a Codex guard is a separate piece of work.

## Layout

On herdr, `round assign` opens each worker as a tab. That suits a phone or a narrow window; on a wide
screen you want the orchestrator and the workers side by side. `rota layout` switches between the two
without touching what runs in the panes:

```
rota layout split     # the desktop view: workers join the orchestrator's tab
rota layout tabs      # the mobile view (the default): one tab per worker
rota layout           # which one each project is in: split, tabs or mixed
```

The choice is remembered for the round: after `rota layout split`, every worker `round assign`,
`round transfer` or a re-dispatch spawns joins the grid instead of getting a tab, until
`rota layout tabs`. A new round starts in tabs again. A reclaimed worker's pane closes with its
session, and the next spawn re-lays the grid.

In the split view the tab is called `rota` and the orchestrator's pane `orchestrator`. The left
column takes 40% of the width: the orchestrator, under the pane that launched the round when rota
knows it (a clean CLI, a quarter of the column's height). Workers fill the other 60%, in columns two
panes high in slot order (`rota round status` order), column by column, all the same width. An odd
count leaves the last column as one tall pane.

```
n=3 (run from the CLI pane C)      n=4, CLI pane unknown
+-----+---+---+                    +-----+---+---+
|  C  | 1 |   |                    |     | 1 | 3 |
+-----+---+ 3 |                    |  O  +---+---+
|  O  | 2 |   |                    |     | 2 | 4 |
+-----+---+---+                    +-----+---+---+
```

`rota orchestrate` run from a plain herdr pane remembers that pane as the CLI, so a later
`rota layout split` from any pane, the orchestrator's included, puts it on top of the left column and
the split lives in its tab. Running `rota layout split` from a plain pane of the project records that
pane instead. With no pane known the orchestrator keeps the left column to itself. `rota layout tabs` puts the orchestrator
and the workers back in tabs of their own and leaves the CLI pane where it is.

The verb only moves panes: no process restarts, nothing is typed into a pane, and focus stays where
it was, so it is safe in the middle of a round.

- **All projects by default.** It arranges every rota project that has an orchestrator open in the herdr
  you run it from. `--project <dir>` narrows it to one.
- **Run `split` again to fold in a new worker.** A worker assigned later opens as a tab. `split` on a
  grid that is already right does nothing; otherwise it rebuilds the grid with the new worker in it.
- **`tabs` puts workers back** in slot order, each tab labelled with the slot name, after the
  orchestrator's tab.
- **Only rota's panes move.** A shell you opened in the orchestrator's tab stays where it is and is
  named in a warning. Parked and dead slots are skipped.
- **herdr only.** A round on tmux or solo is skipped with `host is tmux: layout needs herdr`.

A worker's recorded tab id is the tab it started in, and after a move that tab may be gone. `rota round
reconcile` and `rota round status` still find the live agent: they match it by its
herdr agent name (stable across moves), then the recorded tab, then its worktree, so a moved worker is not reported as a
`dead-tab`. The worker verbs address it by agent name. `round reconcile` reports a `tab-moved` finding naming the
recorded tab and the tab the pane is in now; `--apply` renames the agent and records the new handle: the pane
(`w2E:p57`) when its tab holds other panes, the tab when the pane is alone in it. `worker dispatch` prints the
handle it delivered to. Ending a worker recorded by pane closes only that pane, never the shared tab. Ending a
worker that sits in the split view stops its agent but leaves its pane open as a shell.

## Autopilot

Runs only under `autonomy.level: auto`; under `off` or `roundtrip` both verbs exit 4. With it on, `rota round watch --autopilot` (or one pass of
`rota round tick`) does the mechanical steps of a round, so you spend judgment on what is left:

1. repair the safe drift (`reconcile --apply`);
2. gate and merge every finished PR, as one `worker train` when several wait, at most
   `round.autopilotCap` (default 3) per tick, and only under `ship.mergeApproval: none`;
3. run the architecture-review trigger (`rota round architecture`): when the
   threshold is reached, or a slot is idle with nothing assignable, mint the review items
   (`round.architectureEvery` 0 never mints), one audit line per mint;
4. assign the first ready candidate of the round's scope to each idle slot, at most
   `round.autopilotCap`, at the default tier and never with `--accept-overlap`. Review items
   minted in the same tick go first, and count against the same cap. While every account is cooling
   down from a usage limit, steps 3 and 4 are skipped and the tick says why (`capped`). Likewise
   at `round.batch` open items: the tick assigns nothing and says `batch-full`; gating and merging
   go on, and the next tick assigns once an item has landed.

It never answers a worker, approves a permission, picks a higher tier, reclaims a slot or merges
without a passing gate. A blocked, limited or dead slot, a failed gate, drift it will not repair and
any merge a policy sends to a person come back in `needsYou`. The watch wakes you only for an item you
have not seen, at the heartbeat, or when the autopilot stops (a wind-down or a lost lease). A gate that
failed for a reason a person must clear is held, not re-run on every tick. Every action is one line in
`.rota/gate-audit.jsonl` with `"gate": "autopilot"`.

The scope you chose at `round start` bounds what it assigns; start a `slate` round to keep it to
issues you picked.

## PRs in review

A worker that opened its PR is finished with its slot. When `assign` (or `transfer --to`) needs a
slot and one holds a PR whose worker reported `done` (or went idle) with a clean worktree, it pushes
the branch, parks the slot and keeps the PR on the round's review list (`prs` in `.rota/workers.json`).
The issue keeps its claim and in-progress label: it is still taken, just not by a slot. A slot
whose worker is busy, blocked or has uncommitted files is never taken.

- **See it**: `rota round status` lists each PR in review (`review` in JSON).
- **Merge it**: `rota worker gate <PR number> --base <branch>` (or `'#<N>'`, or the PR URL). A pass drops it from the list, and so does
  a gate that left the PR merged with a red verify of the landed tree (`merged-remotely` included). A merged PR never stays under `review`.
  `rota worker gate <slot>` on a slot whose PR moved to review is refused with the PR to gate, so a
  habitual slot gate never merges the slot's next branch.
- **Bounce it**: `rota round transfer <issue> --to <free slot> --body-file <gap>` checks the
  branch out in that slot, moves the PR back onto it and dispatches the follow-up. Count it first with
  `rota round bounce <issue> --head <sha>`: it exits 4 once the item is at `round.maxBounces`, and
  `rota round status` shows each slot's count. At the cap, never bounce it again: audit the issue's acceptance criteria first (is each one
  checkable as written, with one reading; three lines per criterion at most). If any is not, hand the
  issue to the human with the audit as the note
  (`rota round transfer <issue> --to human --cap-reason ambiguous-criteria --note-file <audit>`). Only when
  every criterion passes does it go to a stronger worker
  (`rota round transfer <issue> --to <slot> --tier heavy --tier-reason <why> --cap-reason tier`).
  `rota round summary` counts the two `--cap-reason` values. Re-review the fix with `/rota-review --since <sha of the last review>`.
  A reviewer's comments on the PR take the same path without you retyping them: see [Review loop](#review-loop).
- A PR in review still counts as in flight: its issue is not a candidate, and its changes take part
  in the overlap check.

## Review loop

A worker that printed `ROTA-DONE` keeps its PR open for review. When a reviewer comments on it (you, a `/rota-review` FAIL verdict recorded for its branch, or a CI bot), the comments go back to the worker instead of waiting for you to retype them. rota watches each `done` slot's PR for new comments, reviews and inline diff comments, ignoring approvals and the worker's own replies.

- `round.reviewLoop: manual` (the default): `rota round watch` wakes you with a `review/<slot>` change and `rota round tick` lists a `review` item. You run `rota round review-relay <slot>`.
- `round.reviewLoop: auto`: `round watch` and `round tick` run it themselves, and a tick lists it as `did review-relay`.

`review-relay` counts one bounce on the item (the same counter as the gate and `round bounce`), sends the comments to the worker as a signed `REVIEW` relay and marks the slot `busy`. The worker checks each comment against the code, fixes what holds, pushes, answers on the PR with `rota worker reply`, and prints `ROTA-DONE` again. At `round.freshAt` (default 2) the relay is a transfer instead: the branch moves to a free slot at the same tier with a brief of the findings still open in the last review and the review link, so the second attempt does not inherit a context that already holds the wrong model. With no free slot it relays in place and says so. At `round.maxBounces` nothing is relayed: the slot stays `done`, rota escalates once on the PR, and under `auto` the tick does not merge it. Hand the item to a stronger worker or to the human, as in [Bounce it](#prs-in-review). The merge stays yours: the loop never gates or merges. Under `manual` the tick only lists the review item and merges as before; under `auto` it skips the merge for a slot it relayed or could not relay. The same text is written to the slot's durable inbox first: the pane copy can cross a worker's `ROTA-DONE`, so `rota worker inbox <slot>` shows the worker what it missed and `rota worker done` refuses while that mail is unread.

## Asking the maintainer

A worker or the orchestrator that needs a human decision does not guess. `rota round escalate send`
posts the question on the issue or PR thread and raises a host notification; `rota round escalate check`
reads the answers back.

When several decisions wait, type `/rota-answer`. The orchestrator then asks them one by one (the
`AskUserQuestion` picker in Claude Code; one message with an `A)` to `E)` list elsewhere, `A)` the
recommendation, the last entry "Write your own answer"), records each answer as a `decision` comment
and applies it before the next question. Otherwise rounds never use a blocking picker, and the
orchestrator never starts `/rota-answer` itself.

```sh
rota round escalate send 59 --slot ben --title "Keep the old flag?" --body-file question.md \
  --options "keep it" --options "drop it" --recommend 2 --cost-if-wrong "one more breaking release"
rota round escalate check
```

Every escalation is decision ready: at least two `--options` and a `--recommend` (the option's number),
or `send` exits 4 and posts nothing. The comment lists the options, marks the recommended one and
states its cost if wrong. A reply of `ok`, `yes` or an option number resolves to that option in
`escalate check`; anything longer is read as prose.

An escalation has a deadline when you pass `--timeout <s>`; past it the entry counts as timed out.
A slot waiting on an open escalation is never reported `stalled`. Under solo mode the comment is the
only channel, because there is no host to notify.

## Merge approval

After a worker opens its PR (pushed, worktree clean) it runs `rota worker done <slot>` as its last action. When `test.fast` is set, the verb
refuses (exit 4) unless the slot's item has a PASS proof row for it at the branch's current HEAD,
recorded with `rota proof record <ID> -- rota test run fast`. A later commit makes the row stale, so
the worker records again. With `test.fast` unset it warns and skips that check. The worker brief then tells the worker to run `rota test run full` and have it green first, and `rota round start` warns once that the proof rows will be missing (reviewers treat that as minor at most). On success the slot is
marked `done`.

Before reporting done, workers mark each verified `AC-n` on the lead and every open
bundle member with `rota plan pass <ID> AC-n --proof '<sha>:<check>'` (use an issue key
such as `'#2150'`). The sha and check must name a recorded PASS row from
`rota proof show <ID>`; proof rows alone do not mark acceptance. A plain done or
`--outcome done` refuses (exit 4) while explicit criteria are unmet, listing them and
their marking commands. Changed criterion text or withdrawn proof makes a mark unmet
again. Items without explicit `AC-n` ids keep their existing behavior.

For an unmet criterion, leave it unmarked and report `--outcome partial --summary-file <f>`
(or `failed` when nothing can be delivered). The file has three lines: what changed,
what was found, and each unmet id with its reason. These outcomes preserve the merge
gate's hold until the orchestrator handles the gap. File mode has no `plan pass`, so
workers disclose explicit unmet criteria through that summary.

`rota worker gate <slot> --base <branch>` is the one merge path in a round. It checks the branch is
fresh, the PR is the worker's, provenance holds and the PR body closes the slot's issue
(`Closes #N`, unless the issue is labelled `partial-slice`), then builds a scratch
merge of the PR into the base (a worktree under `$TMPDIR`), runs `test.full` there, then `test.e2e`
when set, and merges on a pass.

A worker's PR body carries four sections besides the closing keyword: `## Approvals` (the channel of each
decision it acted on), `## Rulings` (its own calls, each with a cost if wrong), `## Assumptions` (each
question the issue left open that it answered from the code, `None` if there were none) and the
`Door:` line. The gate reads only `## Approvals`; the orchestrator reads the rest before review.

The gate refuses before it merges anything (exit 4, nothing lands) in these cases:

- **A recorded FAIL verdict** on the slot's branch (`blockedBy: verdict`, verdict `verdict-blocked`). A newer
  PASS of the same kind clears it. A review record at the current head with a blocking finding
  refuses the same way, even when its verdict is CONCERNS; a newer record at a new head clears it.
- **No `Closes #N`** in the PR body (`blockedBy: closes`), unless the issue is labelled `partial-slice`.
- **An unpicked best-of attempt** (`blockedBy: best-of-unpicked`).
- **A missing review verdict.** The depth `ship.review` resolves for the branch (diff size, the issue's labels) decides what must be on record: `full` both the Spec and Standards verdicts, `light` the Spec verdict (the Standards verdict when the slot has no issue), `none` nothing (`blockedBy: review-missing`). Run `/rota-review`, which records them.
- **Nothing to verify with.** When `test.full` and `test.e2e` are both empty and `test.fullWhere` is
  `local`, the gate would merge a tree no command checked, so it stops (`blockedBy: no-verify`). Set
  one with `rota config set test.full <command>`, or pass `--no-verify` to merge unverified on purpose.
  The train takes the same flag.

A local gate verifies before it merges. For a PR it first asks the forge whether the PR merges
cleanly; a conflict the forge reports (git can miss one, for example after a file move) is
`merge-failed` at once, before the slow verify. If the forge is still computing or the question fails,
the gate carries on. If the scratch verify fails (`verify-failed`), nothing landed and the PR stays open: send the slot back to fix it, then
gate again. If the base moved while the scratch tree verified, the verdict is `base-moved` and nothing
landed; gate again. After the merge lands, the gate compares the landed tree with the verified one. When
they match, that is the pass. When the base moved between the last check and the merge, the gate prints
`VERIFY-AGAIN` and runs the checks once more on the landed base; a red result there is `verify-failed`
with `data.changed` true, and you fix forward with a new PR. With `test.fullWhere` set to `ci`, CI
checks the merge result first and nothing lands when it fails.

After `test.full` and `test.e2e` pass, the gate runs the `## Try it` steps of the slot's issue on the merged tree, in order, stopping at the first failure. A step fails on a non-zero exit or a missing `expect:` substring (`verify-failed`, nothing lands). The steps run only when the issue's author and last editor have write access on the forge (a file issue source runs unchecked); otherwise the gate notes `BEHAVIOUR-SKIP` and continues. The transcript goes to `.rota/qa-runs/behaviour/` and a `behaviour` verdict is recorded. `--check-only` and `--no-verify` skip the run.

A branch that is only behind the base is merged as is when the merge is clean, even when both sides
changed a file: the `STALE-MERGE` note names the shared files (`round.sharedPaths` aside) and the gate
verifies the merged tree. Only a conflict sends it back as `stale`. A `stale` or `provenance-fail` refusal is not a bounce: it is
counted nowhere and never parks the item, because the worker repairs it (merge the base, fix the PR body). Review bounces
still count per item; at `round.maxBounces` (default 3) the item is parked `needs-human` (a pass resets the count, and `0` turns the cap off).

### CI and `GITHUB_TOKEN`

Pushes and comments made with the workflow `GITHUB_TOKEN` do not trigger other workflows. If a CI check
should run on a commit rota pushes, or on a PR rota opens, push and open it with a personal access token or
a GitHub App token instead.

### Merge train

Before starting a gate or train, pull main and fill every free slot with a ready
candidate: `rota round candidates`, then `rota round assign <id> --agent <slot>`.
Train nudges name an `assign first` step when work is ready (JSON
`trainNudge[].assignFirst`). Both verbs also warn on stderr and in JSON `warnings`
at startup, naming the slot and assignment command. The warning is advisory;
`--no-assign-check` skips the lookup for scripted runs. Previews (`gate --check-only`
and `train --plan`) skip it.

With two or more reviewed PRs waiting on one base, `rota worker train <slot|PR>... --base <branch>` is the
default way to land them; a single `gate` is for exactly one waiting PR. The train gates them together and
pays for the verify once instead of once per PR. A gate or train takes 2 to 3 minutes: start it
as a background command, review the next PRs meanwhile, and read the verdict when it
exits. `round assign` and `round transfer` refuse (`gate-running`, naming the PR the gate holds the lock for) until it exits; `round watch` returns when the lock clears, so assign freed slots then. The autopilot tick reports `deferred` instead of erroring. Don't start a second gate or train beside it, and don't write tracked `.rota/` files on the base
checkout until it exits; the checkout guard fails the gate on a changed tracked file. It checks each member the way `gate --check-only` does,
merges them onto the base in a scratch worktree in the order it picks, runs `test.full` on that
tree, then `test.e2e` when set (the most expensive tier runs once per train, not once per PR), and on a pass lands every member through the gate in order. If the base or a member's head moved
while it verified, nothing lands (`base-moved`); the same verdict stops the train mid-way if the base changes between landings. Members must be all PRs or all slots without one.

After the tree verifies and before anything lands, the train also runs each member's `## Try it` steps in the scratch tree, member by member in merge order. A member whose steps fail is the `culprit`. `--no-verify` does not skip this.

The train orders the members to cut conflicts: PRs that share no path with another candidate go first (lowest PR number first), then the PRs that share paths, smallest diff first. It prints one line per PR with the reason (`no shared paths`, or `shares 2 paths with #12, #15`) before it pushes anything. `--order 12,15,9` replaces that order and must name every PR exactly once. If a shared-path member conflicts with the ones before it, the train names the pair, exits 4 with `blockedBy: order` and pushes nothing; a conflict under `--order` is `merge-failed`. It never rebases a PR for you.

Gates and trains on one repository queue on a single lock (`<git-common-dir>/rota/land.lock`), held from the scratch worktree through landing. A second gate or train waits instead of running beside the first, so neither sees the other's scratch worktree and neither lands inside the other's verify-to-land window. `--check-only` takes no lock.

`rota worker gate <PR number> --base <branch>` and `rota worker train <PR>... --base <branch>` also accept PRs that no slot or review record owns (an orchestrator fix-forward PR, a PR after wind-down), reading their branches from the forge. They run the same freshness, provenance, approval, verify and land steps. The closes check takes the issue from the branch name and skips a branch that names none.

With `test.fullWhere` set to `ci` the train pushes its merged tree as `rota/ci/train` and waits for CI instead of running `test.full` here (`test.e2e` still runs here, after CI is green); bisect pushes each prefix the same way, one CI run per step. `test.ciChecks` names the checks that must pass and must not be empty. If a listed check never appears or CI does not finish in time the verdict is `ci-not-run` or `verify-timeout`, and nothing lands; a member that changes the CI definition stops it as `ci-config-changed`. See [running the full tier on CI](configuration.md#running-the-full-tier-on-ci).

A red train, from either tier, bisects (up to ceil(log2 n) extra verifies, on the assumption that the base is green; a red base is reported as such): it verifies growing prefixes of the order and names the first member whose merge
breaks the tree as the `culprit` (the verdict is `verify-failed`). That can be an interaction with the members before it, not that PR alone.
Nothing lands unless you pass `--land-green`, which lands the verified members before the culprit. A
member that conflicts with the base plus the ones before it is `merge-failed` with that member named.
One merge approval covers the whole train. Bounces are not counted.

To find the members that can go together before a train refuses, run `rota worker train <members> --base <branch> --plan`. It runs no tests and pushes nothing. It lists the member pairs that conflict, names the largest conflict-free subset in merge order, lists the rest as order-blocked with the member each conflicts with, and prints the `rota worker train ... --order ...` line that lands that subset. It exits 0 with or without conflicts.

The train remembers what it has verified. Each passing `test.full` or `test.e2e` run is recorded in
`.rota/train-cache.json` (per developer, gitignored) under the tier, the base commit and the ordered
member heads. A re-run of the same train, or a bisect step that repeats a combination, reuses the pass
instead of paying for it again. A moved base or a pushed head misses the cache. Only passes are
cached: a red can come from the machine (a killed compiler, a load flake), so a failure always
re-verifies. The same file notes a member named as culprit at its current head, so a later train that
passes with that head clears the blame.

Whether a human also has to say yes is `ship.mergeApproval`:

| Value | Behavior |
|---|---|
| `none` (default) | The gate merges once its own checks pass. |
| `all` | Every merge needs a human. |
| `paths` | Only a merge that changes a file matching `ship.mergeApprovalPaths` does. |

```sh
rota config set ship.mergeApproval paths
rota config set ship.mergeApprovalPaths '["migrations", "*.lock"]'
```

When the policy covers a merge and no approval is on record, the gate exits 4 and merges nothing
(`verdict: approval-required`, with the matching files for `paths`). There are two ways to clear it:

- **Ask on the thread.** Add `--escalate`: the gate posts the request on the PR (or on the slot's
  issue when it has no PR) and exits 4 with the escalation id in `data`. Keep working other slots. Run
  `rota round escalate check`; once the reply is in, re-run the gate with `--approval <id>`. A reply
  approves when its first word is `approve`, `approved`, `yes` or `lgtm`, or its first two are `ship it`.
  Anything else holds the merge (`approval declined`) and the slot waits for you.
- **Answer at the keyboard.** Re-run with `--confirm --confirm-note "<the answer, quoted>"`.

`--approval`, `--escalate` and `--confirm` are mutually exclusive. An approval belongs to the thread,
not to a commit: pushes after the answer are covered, though freshness and provenance are re-checked
every time. Each approval is appended to `.rota/gate-audit.jsonl`. This policy holds at every
`autonomy.level`; see [manual gates](../../skills/references/manual-gates.md) and
[configuration](configuration.md#shipmergeapproval-and-shipmergeapprovalpaths).

`rota worker gate <slot> --check-only` judges freshness, PR identity and provenance and merges nothing.

## Best-of: two workers on one issue

For a hard issue where you want two independent attempts, label it `best-of:2`. It costs twice
the tokens, so use it only when choosing between attempts is worth that. `rota round assign <ID>`
then binds two free slots in one call, each with its own claim, branch and smoke section number.
With fewer than two free slots it refuses (`blockedBy: best-of-slots`) and changes nothing. When
`round.tiers` configures both `claude` and `codex`, and neither `--kind` nor a `harness:` label
pins one, the attempts get one of each. Each brief names the other slot and tells the worker not
to read its branch. Only `best-of:2` is supported; any other `best-of:` label is refused.

`rota round status` shows each attempt with its sibling (`404 (best-of: ben)`). Both PRs say
`Closes #<ID>`, and `rota worker gate` refuses either one (`blockedBy: best-of-unpicked`) until
you pick:

```sh
rota round pick 404 --pr 512 --reason-file why.md
```

`pick` closes the other PR with a comment naming the winner and your reason, drops it from the
review list, releases its claim and frees its slot. Its branch stays until `rota reap`. Then gate
the winner as usual. If one attempt was returned, reclaimed or died without a PR, `pick` takes
the remaining PR alone; while the other worker is still building, it refuses. The autopilot
assigns best-of issues but never picks: the pick is yours.

An architecture-review item ends with filed issues, not a PR. When its worker prints
`ROTA-DONE <slot> issues:#a,#b`, read the issues and run `rota round accept <ID>`. It closes the
item with a note listing them, releases its claim and frees the slot, so no `reclaim` is needed.
If the slot recorded no issues, pass `--issues #a,#b`. Running it again on a closed item changes
nothing.

A `decision:research` item (see decision milestones in `/rota-vision`) ends with a finding comment,
also with no PR. `round assign` starts it on the `light` tier (`--tier` overrides) and its worker
prints `ROTA-DONE <slot> finding:<comment-id>`. Read the finding, then run
`rota round accept <ID> --gist "<one line>"`: it closes the item and the gist lands in the
milestone's `## Decisions` list. A `decision:grilling` item is never assigned: `round assign` and
`round transfer` refuse it (`blockedBy: human decision`), `round candidates` lists it as not ready,
and the human resolves it with `/rota-brainstorm <ID>`.

For a no-PR item with unmarked AC ids, the filed issue list or accepted finding supplies a
recorded completion reason. The close comment lists those criteria and the reason. A read
or parse failure during acceptance preflight leaves the slot, feedback comments and
research decision log unchanged.

## Bundles: one worker, several small issues

Small related issues (a few follow-ups in the same two files) each cost a worker boot, a PR,
a review and a gate run when assigned one by one. Bundle them instead:

```sh
rota round assign 1559 --bundle 1563,1564   # lead 1559, members 1563 and 1564, one slot
```

The slot works on `<agent>/<lead>-<slug>`. Every member is checked for readiness like the lead,
and the overlap check runs against other slots for the whole bundle, so other slots see it as
one unit. Each member is claimed and marked in progress for the slot, and the brief quotes every
issue's Acceptance, Out of scope and Touches. `round.bundleMax` caps the members (default 3, `0`
means no limit). A `best-of:2` issue cannot be bundled, and a member another slot holds is refused.
rota does not judge whether the issues are related: that is your call.

The worker opens one PR with a `Closes #N` line for every issue and records proof per issue;
`rota worker done` refuses while any issue lacks its PASS row. `rota review depth` lists the
bundle and resolves to the highest depth any of its issues needs, so review each issue's
acceptance. The gate refuses (`blockedBy: closes`) a PR that does not close every one. A bundle
lands or bounces as a unit: `rota round bounce` counts it on the lead and refuses a member.

To drop one member, `rota round transfer <member> --to human`. Closing a member drops it too: `rota worker done` no longer wants its proof row, the gate no longer wants its `Closes` line, and the next `round assign` of the lead removes it from the slot's bundle.
The lead cannot be transferred while members remain, not even after `round.maxBounces`: transfer the members to the human first, or return or reclaim the slot. `round return`, `round reclaim` and
`round wind-down` release the lead and its members together. `rota round status` shows the slot
as `1559 (+#1563, +#1564)`.

## Moving an issue that is assigned

```sh
rota round return ben --reason "wrong premise" --note-file next.md   # the worker's own verb
rota round transfer 59 --to dana --note-file next.md                 # orchestrator: to a slot
rota round transfer 59 --to human                                    # orchestrator: to the human
rota round transfer 59 --to human --cap-reason ambiguous-criteria --note-file audit.md  # at the bounce cap, after the criteria audit
rota round transfer 59 --to kit --tier heavy --tier-reason "bounced 3x"  # orchestrator: to a stronger worker
rota round reclaim ben                                               # orchestrator: dead or stalled slot
```

All three free the slot the same way: dirty paths are committed by name as
`wip: parked from <slot> (rota round <verb>)`, the work branch is pushed to `origin` (no force)
and only then is the worktree switched to `park/<agent>`. A failed push or a rejected commit
leaves the slot as found (exit 5), so the branch is never the only copy of the work. Each
posts a handoff comment on the issue (branch, head, state, reason, your `--note-file`) ending
in `<!-- rota:handoff <slot>@<round> -->`.

- **return** releases the claim and the in-progress label, so `candidates` lists the issue
  again; the branch stays and an open PR stays open. Run it inside the slot's worktree, or as
  the lease holder. An `assign` after a return starts fresh; `transfer` is the verb that
  continues a pushed branch.
- **transfer to a slot** checks the pushed branch out in the receiver's worktree and
  dispatches it with a brief that names the handoff; the in-progress label stays on. **To
  `human`** labels the issue `needs-human` (`issues.labels.needsHuman`), claims nothing and
  dispatches nothing; `candidates` skips it until the human clears the label.
- **reclaim** works on a slot that is `dead` or `stalled` (no commit, edit or state change for
  `round.stallMinutes`, default 30, `0` is off). A healthy slot needs `--force`; a live pane is
  killed first, and with no host to ask it is refused as `live agent`. It does not reassign.
  `rota reap` reclaims `dead` slots only, never `stalled` ones: a worker in a long test run makes no
  commits and looks stalled, and an unattended `reap --apply` would kill it.

`rota round status` lists the round's slots with host, PR and drift. `rota round reconcile` reports
drift between the registry, the host, git and the forge, including `stalled` (never repaired),
`lease-stale`, `claim-mismatch`, `claim-label-drift` and `unregistered-branch`.
When a slot records a closed, unmerged PR and an open PR heads the same branch,
`pr-superseded` names both PRs. `reconcile --apply` records the open PR's URL;
the next reconcile reports no drift for that record. A merged PR record is kept.
Each won round claim has a visible `claimed:<agent>@<machine>` label: `claimed:ben@main` on main,
or `claimed:ben@mac` for support slot `mac-ben` when `round.support` is `mac`. Comment claims
decide ownership; labels follow them. `--apply` repairs missing or stale claim labels without
editing the comment claims. Release removes that claim's label; completion and the merge gate
remove all claim labels from closed issues. Reconcile also cleans closed issues that retain them.

After upgrading, run `rota round reconcile --apply` once to backfill the visible labels for
in-flight comment claims. It also repairs stray claim labels on open issues with no remaining
claim. Only `round reconcile` sweeps closed issues for claim labels; status, watch and the
autopilot tick all stay on filtered closed-issue reads. Reverting the change leaves `claimed:*` labels already written to
the tracker; remove those labels by hand.

A repair whose evidence could not be read this run (the host for a `dead-tab`, the
forge for a PR, merge or label finding) is skipped with a warning and stays in `drift`; run it again once
the host or forge answers. A slot whose PR has merged is parked, even one already marked `dead` because its
tab is gone; a slot whose worktree has uncommitted changes is left alone.

If `.rota/workers.json` exists but does not parse, every verb that writes it refuses (`fix or remove it,
rota will not overwrite it`) instead of starting from an empty pool. A verb that records a round event
(`round assign`, `round transfer`, `round report`) exits non-zero when its `.rota/ledger.jsonl` row cannot
be written, and the message says the assign, transfer or report itself did happen.

## Adopting work another tool started

A branch or worktree made by another tool (Codex's managed worktrees, a Claude Code agent team, a
cloud session) can join the round without rota driving the agent:

```sh
rota worker adopt codex/612-thing --issue 612
rota worker adopt ../thing-wt --issue 612 --name ext-1 --pr https://github.com/org/repo/pull/9
```

The argument is a local branch or a worktree of this repo. Adopt runs the overlap check `round assign`
runs. It exits 4 with `blockedBy` `overlap` (`--accept-overlap` skips only that), `registered` (a slot
holds the branch) or `held` (a slot holds the issue). The new slot is `ext-<n>` unless you pass
`--name`. Without `--pr`, adopt records the open PR the branch heads, so the gate and the train read it at
once; if the forge fails or no PR is open yet, the slot starts with none and `round reconcile --apply`
records it later. It is `external`: no session handle, so `worker dispatch` refuses it (`blockedBy: host`),
`round wait` and `worker poll` never read a pane, and `reconcile` reports no dead tab or stall for it.

`round status` shows it as `external/<state>`, worked out from the forge and git: `done` with an open
PR, `busy` when the branch is ahead of the base with no PR, `idle` otherwise, `unknown` when the forge
is down. The gate and the train treat its PR as usual and expect no relays. When the PR merges the slot
is released and its worktree and branch stay, since rota did not make them; `rota reap` lists the
merged branch. `worker gate --prune` or `round wind-down --prune` deletes both (a dirty worktree is
left). `wind-down` parks no adopted slot: it reports it `released` or `open`.

Set `round.adoptPattern` to a glob over branch names (`codex/*`; `*` stops at `/`; empty is off) and
`rota round reconcile` lists each matching branch that no slot holds and that is not merged as
`unregistered-branch`. `--apply`, and so the autopilot's first step, adopts those whose name yields an
issue number (`<agent>/<issue>-<slug>`, `issue-N` or `#N`). A blocking overlap becomes a warning and
the branch stays listed; a name with no number is listed as `needs --issue`. See
[orchestrator harnesses](orchestrator-harnesses.md#adopting-work-another-tool-started).

## What a round cost

Every round event is appended to `.rota/ledger.jsonl`: an assign, a worker's done or blocked
report, a bounce, a transfer, a best-of pick, a park, each gate verdict and merge, and a usage
limit hit (a reroute is the transfer it makes). `rota round summary` folds the file, with `.rota/gate-audit.jsonl`, into one
row per issue (slot, harness, account, assigned and done times, wall time, bounces, gate outcome,
merged), then totals per slot and per account, then an idle table (each stretch a slot sat `done` before
its next assign, with the median; `idle` in JSON), then the round's gate audit lines. A re-done after a
bounce is not counted as idle time.

```sh
rota round summary              # the current round, else the highest in the ledger
rota round summary --round 3
rota round summary --json
```

The quota share column is how many points of headroom the account lost between assign and done,
read from the account meter. No host reports tokens, so this is a share of the account's window,
not a token count, and it includes whatever other slots on the same account did in that time.
Codex slots and accounts with no meter reading show `n/a`: unknown is never printed as 0.
On a herdr or tmux round the done reading is taken when the pane reports done (`rota worker done`,
`worker poll` or `round wait`); under solo it is taken by `rota round report`.
`rota round status --ui` shows the quota each slot's account has lost since the slot's latest
assign in a `burn` column: the headroom the assign recorded minus the meter now.
It reads the meters once per refresh for all accounts, and shows `n/a` for codex slots and accounts
with no reading, or when headroom rose because the quota window reset.

## Two machines: main and support

With `round.support` set, main labels issues for the support machine (`rota round support offer`)
and the two orchestrators talk through the issue thread. The support machine hands back an issue
it cannot work with `rota round support return <ID> --reason-file <f>`. Both `round watch` runs
wake on the other machine's PR changes and comments (the support machine's also on main's comments on the issue of a PR parked for review, and on a PR of its own that turns conflicting with its base), and main's merge gate accepts a support PR's
approvals from relays the support orchestrator posted as issue comments. See
`docs/contributing/rounds.md` (Support role) for the rules.

## Winding down

```sh
rota round wind-down                # verify the base, park every slot, release the lease
rota round wind-down --no-verify
```

After the slot outcomes, wind-down prints the same table as `rota round summary` for the round it
closed (and `--json` adds it as `summary`). With no ledger it prints nothing extra.

Run it from the orchestrator that holds the lease, with the base checked out and clean in
the project root. It re-verifies the base (`test.full`), then parks every
roster slot on `park/<agent>` and releases the claims, then releases the lease. A red base
(`verify-failed`, exit 1) keeps the lease. A slot with uncommitted changes or commits not on
the base is reported as `retained` and left alone (`holds-work`, exit 4) and the lease is kept; the other slots are
parked anyway, so fix the slot and run it again. It deletes no branch and clears no label: the
`drift` count says what `rota round reconcile` and `rota reap` still have to do. After a round, run
`rota reap` ([doctor and reap](doctor-and-reap.md)).

## Solo mode

`rota round start` resolves the round's host once and records it in `.rota/workers.json`. With
`round.dispatch` at `subagent` (the default) or unset it is herdr inside a herdr pane (`HERDR_ENV=1`), tmux
inside tmux (`TMUX` set), and otherwise **solo**. An explicit `herdr` or `tmux` is used as
set and fails when unavailable; solo is never a fallback from it.

Under solo, each worker is a Claude `Agent` subagent the orchestrator launches in the slot's
`.worktrees/<agent>` checkout. `rota round assign` returns the brief and the worktree instead
of starting a pane, `rota round report <slot> --state ...` records what the subagent said (`--issues "#a,#b"` in place of `--pr` for an architecture-review item),
and `rota round wait` reads the registry without blocking. The pane verbs (`rota worker
dispatch`, `rota worker poll`, `rota worker session`) refuse. The registry holds what tab mode writes (minus the
pane fields), so `rota round reconcile`, the gate and the merge policy work unchanged.

**Every solo worker shares the orchestrator's session limit.** The subagents run on the
orchestrator's own account, rate window and context, so one usage limit stops every
worker and the orchestrator together, and every result lands in the orchestrator's
context. Keep solo rounds small (two or three slots) and the results short. Solo runs
Claude workers only: a Codex subagent cannot be given a working directory.

## Maintainer answers typed into a pane

A worker's question reaches you on the thread or in its pane. Prefix a direct answer with `m:` to make
it citable without a confirmation round-trip. The prefix is imitable, so a prefixed line that
contradicts the last signed orchestrator message still gets one confirmation.

## Internals

**The lease.** It is `<git-common-dir>/rota/round-lease.json`, so every worktree of the repo shares it.
The holder is the nearest non-shell ancestor of `rota` (in Claude Code, the `claude` process) plus its
start time. Pass `--holder-pid` where that cannot be read. A lease whose holder is gone is stale.

**Long waits in Claude Code.** The Bash tool kills a command at its `timeout`, which defaults to 2
minutes (`BASH_DEFAULT_TIMEOUT_MS`) and is capped at 10 minutes (`BASH_MAX_TIMEOUT_MS`). Either:

- raise the cap in `settings.json` under `env` (for example `"BASH_MAX_TIMEOUT_MS": "3600000"`) and
  pass a matching `timeout` on the Bash call; or
- loop on a finite `rota round wait --timeout` shorter than the cap.

## See also

- [A round cycle](../walkthroughs/round-cycle.md): shaping the slate, running a round, winding down and releasing, in order.
- [Unattended rounds](unattended-rounds.md): hooks, keepalive, usage limits and account switching.
- [Doctor and reap](doctor-and-reap.md): the preflight checks and what cleanup removes.
- [Codex workers](codex-workers.md): `--kind codex`.
- [Parallel work](parallel-work.md): several `/rota-work` sessions without an orchestrator.
- [Autonomy levels](autonomy.md): how far a run goes before it stops, solo or in a round; `auto` turns on the autopilot.
- [Contributing: rounds](../contributing/rounds.md): this repo's gate, repo rules and roster.

## Stale claims on another machine

`rota round status` reports a `stale-claim` finding when a remote holder has no recent
claim, PR update or origin branch activity. `rota round candidates` keeps that item
visible with `blockedBy: stale-claim`, `staleHolder` and `lastActivity`; `rota doctor`
adds a warning with the same holder and time. Local held items use the existing stalled
and claim-mismatch checks instead.

The default threshold is four hours for bugs, tasks and Minor/Cosmetic features, eight
hours for Major features. Configure `round.claimStaleMinutes` and
`round.claimStaleMinutesMajor`; `0` disables the corresponding class. PR comments and
reviews count through the forge's PR update time. Branch committer dates are clamped to
now. Claims without a known server timestamp are never stale. Automatic stale scans only read details
for items carrying `claimed:*` or the in-progress label. Explicit reclaim checks also
read unlabelled comment claims directly. Open-scope candidates still treat an open
comment claim as taken when its label is missing. Activity follows any resolving
PR and branch, including a transfer's original branch and a bundle lead's PR. Status,
candidates and doctor may fetch origin tips into local git objects and fetch metadata (including `FETCH_HEAD` and applicable remote-tracking refs);
they do not change project files or claims. An incomplete doctor scan keeps confirmed
findings and adds a warning with the reason.

Ask the maintainer before reclaiming: name the holder, last activity, and that the old
claim marker and claim label will be released and replaced. Record the yes as the
maintainer's own answer, then run:

```bash
rota round assign 7 --agent dana --reclaim-stale mac-ben@3
```

Assign rechecks activity before releasing anything. Recent activity, a different holder,
a disabled threshold or an unavailable activity read returns `blockedBy: not-stale`.
Existing readiness checks still apply; an open PR also needs an explicit
`--accept-open-pr` decision. Plain assign, the autopilot tick and `reconcile --apply`
never reclaim stale claims. The gate refuses an old holder's recorded `claimId` with
`claim-lost` while another claim holds the issue. PRs with no recorded claim are exempt.
Adopt refuses `claimed` only if the current holder is stale or authoritative claim history
shows that the branch's former claim was released and replaced. Fresh different holders
without that evidence do not prevent adoption; unreadable history is warned and skipped.
