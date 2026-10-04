# Parallel rounds

A round is one orchestrator session plus two to five workers. Each worker is a standing agent in
its own git worktree and terminal tab (herdr or tmux), or an in-harness subagent when there is no
terminal host ([solo mode](#solo-mode)). Each holds one GitHub issue at a time. Workers implement,
verify and open a PR; they never merge. The orchestrator assigns issues, relays decisions, merges
PRs; the merge gate is the only full verification run.

The orchestrator runs the `rota-orchestrate` skill, which holds the judgment: which issues, how to
read a stuck worker, what to escalate, when to merge. The mechanics are `rota round` verbs, so a round
never polls inside the orchestrator's context. A worker reads
[`references/worker-contract.md`](../../references/worker-contract.md); `rota round assign` points it there.

This page covers what a round does and how to run one. Running a round on rota itself, with its
gate and repo rules, is in [contributing: rounds](../contributing/rounds.md).

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

## Setup

1. **A host** for the workers' tabs: herdr (pinned to 0.9.x) or tmux. Leave
   `work.dispatch` at the default `rota init` writes (`subagent`) and `rota round start` detects the
   host from where it runs (herdr inside a herdr pane, tmux inside tmux). Set `herdr` or `tmux` only
   to force one. With neither it falls back to
   [solo mode](#solo-mode).
2. **A tracker.** Rounds take GitHub issues (`gh`) or GitLab issues (`glab`), authenticated.
3. **Accounts, if you have more than one.** `work.accounts` maps slots to separate
   `CLAUDE_CONFIG_DIR`s, so workers draw on different usage limits and `rota round assign` can avoid a
   cooling account. The entries are paths on your machine, so they go in `.rota/config.local.json`
   (per developer, gitignored, merged over `.rota/config.json`), not in the tracked file.
   `rota config set` writes the tracked file, so edit this one by hand:

   ```json
   {
     "work": {
       "accounts": [
         { "name": "a", "configDir": "/path/to/claude-a" },
         { "name": "b", "configDir": "/path/to/claude-b" }
       ]
     }
   }
   ```

   `rota worker account list` then shows each account with its usage verdict.

4. **Issues a worker can pick up.** A candidate needs acceptance criteria or a design or plan note,
   closed dependencies and no file overlap with work in flight (see [Picking issues](#picking-issues)).
5. **Preflight.** `rota doctor` checks all of the above and names the fix for each failure. See
   [doctor and reap](doctor-and-reap.md).

For a round that must survive the orchestrator's context filling or a usage limit, also install the
hooks and run the orchestrator under `rota keepalive run`:
[unattended rounds](unattended-rounds.md). For Codex workers: [Codex workers](codex-workers.md).

## Your first round

```sh
rota orchestrate        # in an initialized project, `rota` alone does the same
```

That runs `rota doctor`, stops if a check fails, then opens an orchestrator session that has already
started `/rota-orchestrate`. Nothing is typed into a pane. The session runs under
`rota keepalive run`, so it restarts from a handoff when its context fills.

Where it opens depends on where you ran it:

| You are | The orchestrator opens |
|---|---|
| In a herdr pane | a new focused tab in that workspace |
| In tmux | a new window in that session |
| In neither, herdr installed | a herdr session `rota-<dir>` with the orchestrator in its first workspace (your terminal attaches to it) |
| In neither, only tmux installed | a tmux session `rota-<dir>` (your terminal becomes it) |
| In neither, no herdr or tmux | in this terminal; workers run as subagents ([solo mode](#solo-mode)) |

`work.dispatch: tmux` skips the herdr-session row and uses tmux. The herdr session is rota's own,
started with `herdr --session rota-<dir> server`; your default herdr session is never driven.
The agent comes from `orchestrator.harness` (`claude`, `codex`, `hermes` or `opencode`; see
[orchestrator harnesses](orchestrator-harnesses.md)). `rota orchestrate --dry-run`
shows what would start.

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
`--apply` first. The sections below cover each step.

## The round flow

| Step | Verb | What it does |
|---|---|---|
| Check | `rota doctor` | git, host, tracker auth, accounts, herdr hooks, `rota` version, Codex; each failure carries its fix |
| Start | `rota round start` | takes the repo's orchestrator lease, provisions slots, lists ready candidates |
| Pick | `rota round candidates` | the open items that pass the criteria, dependency and overlap checks |
| Review | `rota round architecture` | after `round.architectureEvery` closed non-refactor items, or when a slot is idle and nothing is assignable, mints one architecture-review item per area and assigns them to idle slots |
| Assign | `rota round assign <ID>` | claims the item, marks it in progress, cuts `<agent>/<issue>-<slug>`, starts the worker with a signed pointer brief |
| Wait | `rota round wait` | blocks until one slot needs the orchestrator, then returns it; never poll |
| Watch | `rota round watch` | the background form of `wait`: run it as a background command and it exits when a slot, PR or escalation changes, or at a heartbeat |
| Look | `rota round status`, `rota round reconcile` | the round's rows and drift; `reconcile --apply` repairs what is safe |
| Ask | `rota round escalate send`, `rota round escalate check` | puts a question to the maintainer on the issue or PR thread and reads the answer |
| Merge | `rota worker gate <slot\|#PR> --base <branch>` | verifies on the merged tree, merges on a pass; a [merge approval](#merge-approval) policy can require a human first |
| Clean | `rota reap` | lists, then with `--apply` removes, what no live slot owns; never kills a running agent |
| End | `rota round wind-down` | re-verifies the base, parks every slot, releases the lease |

Each verb's arguments, data and exit codes are in `docs/design/contract/`. Every verb
takes `--json` for a machine-readable envelope.

## Starting a round

`rota round start` takes the orchestrator lease, provisions the roster slots and lists the
candidates; it starts no agent.

```sh
rota round start --slots 3                    # scope from round.scope (default milestone)
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

- `criteria`: acceptance criteria, or a design or plan note.
- `dependencies`: every `## Depends on` reference is closed. One that cannot be looked up fails the
  check, so fix the issue text.
- `overlap`: no shared file with an in-flight slot, from a `## Files` section or the paths the issue
  text names plus the slot's real changes.

The overlap check cannot see files an issue will create, paths nobody wrote down, two issues editing
the same function, generated files every issue touches (list those in `round.sharedPaths`), renames,
or another machine's round. Read two issues' bodies before you run them side by side.

## Assigning an issue

```sh
rota round assign 59 --check-only             # readiness only; exit 1 when not ready
rota round assign 59 --agent ben --body-file decisions.md --siblings 58,60,62
```

Without `--agent` the first idle roster slot takes it, else the first slot whose worker is done with
a PR (see [PRs in review](#prs-in-review)). Assign refuses (exit 4, `blockedBy`)
with `no round`, `out of scope`, `not ready`, `overlap`, `claimed`, `slot busy`,
`no free slot` or `brief missing`, and marks nothing in those cases. When it goes through it
claims the item (`<agent>@<round>`), sets it in progress with a comment, cuts the slot's
branch `<agent>/<issue>-<slug>`, picks the account and dispatches a short signed brief: a
pointer to the standing contract (`round.brief`, else `references/worker-contract.md`), the
issue to read and dispute, the siblings and the decisions from `--body-file`.

- **Tier.** `--tier light|standard|heavy` picks the worker's model tier (default `round.tier`); a
  tier above the default needs `--tier-reason`. The tier and its model are recorded on the slot,
  shown by `rota round status`, and named in the brief with the tier table for the worker's own
  subagents. See [round keys](configuration.md#round-keys).
- **Kind.** `--kind codex` starts a Codex worker instead of a Claude one; see
  [Codex workers](codex-workers.md).
- **Overlap.** `--accept-overlap` skips the file-overlap check only. Say which PR merges first in
  the second worker's brief.
- **Failure.** A failure before dispatch undoes the claim and state; one at or after dispatch keeps
  them, and repeating the call resumes.

## Waiting on workers

`rota round wait [<slot>...] [--timeout <s>]` blocks until a worker needs attention and prints
the slot and its state as JSON, so the orchestrator never polls in its own context. It
classifies with the same rules as `rota worker poll` (sentinels, `limited`, `dead`, then the
host's status). It records the slot it returns as `rota worker poll` would (its state, and the PR
from `ROTA-DONE`) and marks it seen: the next `wait` skips that slot until its state changes, so a
done slot whose PR you are holding does not come back on every call. A dispatch or relay re-arms it.
With no slot named it watches every slot that has a session and whose recorded state is not `idle`;
`rota worker dispatch` arms a slot.

- **herdr**: pinned to **0.9.x** (built against 0.9.3, socket protocol 22); another minor
  exits 5. One `events.subscribe` over `HERDR_SOCKET_PATH` carries a
  `pane.agent_status_changed` subscription per watched pane, so N slots cost one
  connection.
- **tmux**: no event stream and no agent status, so the verb re-captures the panes every
  `--settle` seconds (default 5) inside its own process.
- **Timeout** exits 1 with `data.timedOut: true` and every slot's state; it is an answer,
  not a fault. `--timeout` defaults to 0, which waits indefinitely.
- **Long waits in Claude Code** hit the Bash tool's timeout; see [internals](#internals).

## Staying reachable while you talk

`rota round wait` blocks the orchestrator's own session, so it cannot run while the orchestrator is
answering you. Run `rota round watch` as a background command instead: it exits with JSON when a
slot needs attention, a PR or an escalation changes, or after `--heartbeat` seconds (default 600) with
nothing to report, and the harness wakes the orchestrator when it exits. Keep exactly one running and
re-arm it after every wake; a second one is refused (exit 4). The registry is read every `--poll`
seconds and the forge every `--forge-poll`; `reason` in the result says which woke it.

With `rota hook install` the orchestrator is held to this. The Stop hook refuses to let a lease-holding
orchestrator go idle while workers are active and no watch is running, and the prompt hook adds a
one-line digest of the round (and a reminder when no watch is armed) to every message you send.
Neither applies to a solo round, which has no panes to watch.

## Autopilot

Off by default. With `round.autopilot` on, `rota round watch --autopilot` (or one pass of
`rota round tick`) does the mechanical steps of a round, so you spend judgment on what is left:

1. repair the safe drift (`reconcile --apply`);
2. gate and merge every finished PR, as one `worker train` when several wait, at most
   `round.autopilotCap` (default 3) per tick, and only under `ship.mergeApproval: none`;
3. run the architecture-review trigger (`rota round architecture`): when the
   threshold is reached, or a slot is idle with nothing assignable, mint the review items
   (`round.architectureEvery` 0 never mints), one audit line per mint;
4. assign the first ready candidate of the round's scope to each idle slot, at most
   `round.autopilotCap`, at the default tier and never with `--accept-overlap`. Review items
   minted in the same tick go first, and count against the same cap.

It never answers a worker, approves a permission, picks a higher tier, reclaims a slot or merges
without a passing gate. A blocked, limited or dead slot, a failed gate, drift it will not repair and
any merge a policy sends to a person come back in `needsYou`. The watch wakes you only for an item you
have not seen, at the heartbeat, or when the autopilot stops (a wind-down or a lost lease). A gate that
failed for a reason a person must clear is held, not re-run on every tick. Every action is one line in
`.rota/gate-audit.jsonl` with `"gate": "autopilot"`.

The watch is the autopilot's heartbeat: keep exactly one running, as above. The scope you chose at
`round start` bounds what it assigns; start a `slate` round to keep it to issues you picked.

## PRs in review

A worker that opened its PR is finished with its slot. When `assign` (or `transfer --to`) needs a
slot and one holds a PR whose worker reported `done` (or went idle) with a clean worktree, it pushes
the branch, parks the slot and keeps the PR on the round's review list (`prs` in `.rota/workers.json`).
The issue keeps its claim and in-progress label: it is still taken, just not by a slot. A slot
whose worker is busy, blocked or has uncommitted files is never taken.

- **See it**: `rota round status` lists each PR in review (`review` in JSON).
- **Merge it**: `rota worker gate <PR number> --base <branch>` (or `'#<N>'`, or the PR URL). A pass drops it from the list.
  `rota worker gate <slot>` on a slot whose PR moved to review is refused with the PR to gate, so a
  habitual slot gate never merges the slot's next branch.
- **Bounce it**: `rota round transfer <issue> --to <free slot> --body-file <gap>` checks the
  branch out in that slot, moves the PR back onto it and dispatches the follow-up.
- A PR in review still counts as in flight: its issue is not a candidate, and its changes take part
  in the overlap check.

## Asking the maintainer

A worker or the orchestrator that needs a human decision does not guess. `rota round escalate send`
posts the question on the issue or PR thread and raises a host notification; `rota round escalate check`
reads the answers back.

```sh
rota round escalate send 59 --slot ben --title "Keep the old flag?" --body-file question.md
rota round escalate send 61 --pr --title "Merge approval: PR #61" --body-file ask.md
rota round escalate check
```

An escalation has a deadline when you pass `--timeout <s>`; past it the entry counts as timed out.
A slot waiting on an open escalation is never reported `stalled`. Under solo mode the comment is the
only channel, because there is no host to notify.

## Merge approval

`rota worker gate <slot> --base <branch>` is the one merge path in a round. It checks the branch is
fresh, the PR is the worker's and provenance holds, then re-runs
`refactor.verifyCommands` on the merged tree and merges on a pass.

A branch that is only behind the base is merged as is when the merge is clean and the base did not
change any file the branch changed (`round.sharedPaths` aside). A conflict, or a file changed on both
sides, sends it back as `stale`. Each such bounce is counted per item; at `round.maxBounces` (default
3) the gate parks the item `needs-human` with a comment instead of sending it back again.

### Merge train

When several PRs wait in review, `rota worker train <slot|PR>... --base <branch>` gates them together and
pays for the verify once instead of once per PR. It checks each member the way `gate --check-only` does,
merges them in the order given onto the base in a scratch worktree, runs `refactor.verifyCommands` on that
tree, and on a pass lands every member through the gate in order. If the base or a member's head moved
while it verified, nothing lands (`base-moved`); the same verdict stops the train mid-way if the base changes between landings. Members must be all PRs or all slots without one.

A red train bisects (up to ceil(log2 n) extra verifies, on the assumption that the base is green; a red base is reported as such): it verifies growing prefixes of the order and names the first member whose merge
breaks the tree as `culprit`. That can be an interaction with the members before it, not that PR alone.
Nothing lands unless you pass `--land-green`, which lands the verified members before the culprit. A
member that conflicts with the base plus the ones before it is `merge-failed` with that member named.
One merge approval covers the whole train. Bounces are not counted.

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
`autonomy.level`; see [manual gates](../../references/manual-gates.md) and
[configuration](configuration.md#shipmergeapproval-and-shipmergeapprovalpaths).

`rota worker gate <slot> --check-only` judges freshness, PR identity and provenance and merges nothing.

## Moving an issue that is assigned

```sh
rota round return ben --reason "wrong premise" --note-file next.md   # the worker's own verb
rota round transfer 59 --to dana --note-file next.md                 # orchestrator: to a slot
rota round transfer 59 --to human                                    # orchestrator: to the human
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
`lease-stale` and `claim-mismatch`; `--apply` makes the safe repairs, and never edits the tracker.

## Winding down

```sh
rota round wind-down                # verify the base, park every slot, release the lease
rota round wind-down --no-verify
```

Run it from the orchestrator that holds the lease, with the base checked out and clean in
the project root. It re-verifies the base (`refactor.verifyCommands`), then parks every
roster slot on `park/<agent>` and releases the claims, then releases the lease. A red base
(`verify-failed`, exit 1) keeps the lease. A slot with uncommitted changes or commits not on
the base is reported as `retained` and left alone (`holds-work`, exit 4); the other slots are
parked anyway, so fix the slot and run it again. It deletes no branch and clears no label: the
`drift` count says what `rota round reconcile` and `rota reap` still have to do. After a round, run
`rota reap` ([doctor and reap](doctor-and-reap.md)).

## Solo mode

`rota round start` resolves the round's host once and records it in `.rota/workers.json`. With
`work.dispatch` at `subagent` (the default) or unset it is herdr inside a herdr pane (`HERDR_ENV=1`), tmux
inside tmux (`TMUX` set), and otherwise **solo**. An explicit `herdr` or `tmux` is used as
set and fails when unavailable; solo is never a fallback from it.

Under solo, each worker is a Claude `Agent` subagent the orchestrator launches in the slot's
`.worktrees/<agent>` checkout. `rota round assign` returns the brief and the worktree instead
of starting a pane, `rota round report <slot> --state ...` records what the subagent said,
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

- [Unattended rounds](unattended-rounds.md): hooks, keepalive, usage limits and account switching.
- [Doctor and reap](doctor-and-reap.md): the preflight checks and what cleanup removes.
- [Codex workers](codex-workers.md): `--kind codex`.
- [Parallel work](parallel-work.md): several `/rota-work` sessions without an orchestrator.
- [Autonomy levels](autonomy.md): how far skills chain on their own, a different axis from `round.scope`.
- [Contributing: rounds](../contributing/rounds.md): this repo's gate, repo rules and roster.
