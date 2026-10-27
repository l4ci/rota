# Support machine

A round can run on two machines at once: **main**, which holds the round and merges, and a
**support** machine, which works issues in its own slots and opens PRs. The machines share no
connection. GitHub is the only shared state: claims, pins, comments and PRs on the tracker.

What you need first is a working round on main: [parallel rounds](parallel-rounds.md). The
support machine needs an issue tracker (GitHub); with GitLab, `round start` refuses
`blockedBy: forge` while `round.support` is set.

## Setting it up

Two steps, no hand edits:

1. **On main**, name the support machine and push the config:

   ```sh
   rota round support enable mac     # writes round.support to the tracked .rota/config.json
   git add .rota/config.json && git commit -m "Enable the mac support machine" && git push
   ```

   `rota round support disable` removes it again. Change it between rounds, not during one:
   bounce counts are not carried over.

2. **On the second machine**, clone or pull the repo and run `rota setup`. Answer `support` to
   the first question (or pass `--set round.role=support`). Setup checks that the tracked config
   names a support machine and a tracker, asks only the machine-local questions (dispatch,
   autonomy, worker and orchestrator harness) and writes them with `round.role: support` to
   `.rota/config.local.json`. It ends with the doctor's first failing check, or `doctor: pass`.

Then start a round on each machine as usual. The support machine's slots are named after it:
with `round.support` at `mac`, the slot for `ben` is `mac-ben`.

Run `rota update` on either machine to compare the installed version with the latest release.

## Which machine takes which issue

| Issue | Who works it |
|---|---|
| labelled `machine:mac` | only the support machine; main's candidates show it as blocked |
| labelled `machine:main` | only main |
| no `machine:` label | either machine, within its `round.scope` |

The support machine fills free slots with issues pinned to it first, then pulls unpinned issues
in its `round.scope`. Neither machine reserves its slate against the other: the tracker claim
decides. When support wins a claim on an unpinned issue, it pins it `machine:mac` so main can
follow it.

On main, pin issues for support with:

```sh
rota round support offer 412 418          # adds machine:mac
rota round support offer 418 --withdraw   # takes the pin off again
```

## Seeing who holds what

Every won claim gets a visible label: `claimed:ben@main` for a slot on main,
`claimed:ben@mac` for support slot `mac-ben`. The comment claim decides ownership and the label
follows it; `rota round reconcile --apply` repairs missing or stale labels. After upgrading from
a version before v0.26, run it once to backfill labels for claims already in flight.

`rota round status` (and `--ui`) lists the support queue: `waiting` until support claims an
offered issue, `active` with its claim, `unknown` when the claim could not be read. Main never
guesses which worker or model runs on the other machine.

## Talking between the machines

The issue thread is the channel. Each orchestrator posts notes for the other as issue comments,
stamped with the machine they came from:

```sh
rota item comment add 412 --kind feedback --body-file note.md   # a plain note
rota item comment add 412 --kind relay --body-file relay.md     # a relayed instruction
```

Both `round watch` runs wake on the other side:

- **Main** wakes on a new PR or a changed PR head for an offered or support-held issue, and on a
  new comment from the support machine on those issues.
- **Support** wakes on new comments from main on the issues its slots hold, on main's comments
  on the issue of a PR parked for review, and when one of its PRs conflicts with its base.

## Handing an issue back

When support cannot work an offered issue (not ready, wrong premise, it collides with main's
work, or you want to keep it), it hands it back right away instead of idling:

```sh
rota round support return 418 --reason-file why.md
```

The reason goes on the issue as a comment, support's claim is released and the `machine:` pin
comes off, so main's next `round candidates` can pick it up. It is refused on main
(`blockedBy: role`) and while a support slot still holds the issue (`blockedBy: claimed`).

## Merging support PRs

Support never merges. Its PRs land through main's gate like any other. A relay the support
orchestrator sends its worker is also posted on the issue as a `rota:relay` comment, and main's
gate accepts the PR's approvals citing it. The `support offer` comment counts as the assignment
approval. A cited relay that no trusted comment holds still fails `PROVENANCE-FAIL`. Only
comments by authors with write access count.

## What support does not do

The support machine never merges and never writes tracked `.rota/` content. Those verbs exit 4
with `blockedBy: role`. Its workers post learnings as `learning` comments on the issue; main's
`/rota-learn` folds them into `KNOWLEDGE.md`.

## When a machine goes quiet

If a claim on the other machine shows no activity (claim, PR update or branch push) for four
hours, or eight for Major features, `rota round status`, `candidates` and `doctor` mark it
`stale-claim`. Set the thresholds with `round.claimStaleMinutes` and
`round.claimStaleMinutesMajor` (`0` disables). After you approve, main can take the issue over:

```sh
rota round assign 418 --agent dana --reclaim-stale mac-ben@3
```

Assign rechecks the activity first. The old holder's PR is then refused `claim-lost` at the
gate. See [stale claims on another machine](parallel-rounds.md#stale-claims-on-another-machine).

## Config

| Key | Where | What it does |
|---|---|---|
| `round.support` | tracked | Names the support machine and its slot prefix; set it with `rota round support enable` |
| `round.role` | `.rota/config.local.json` | `main` (default) or `support` on this machine; set by `rota setup` |
| `round.scope` | tracked | Which unpinned issues either machine may pull |
| `round.claimStaleMinutes`, `round.claimStaleMinutesMajor` | tracked | When a quiet remote claim counts as stale |

The full rules, for contributors, are in [Support role](../contributing/rounds.md#support-role).
