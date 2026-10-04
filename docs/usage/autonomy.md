# Autonomy levels

`autonomy.level` controls whether each skill nudges you with a one-line suggestion at decision points or invokes the next skill directly. Two levels, mutually exclusive: `"off"`, `"auto"`. `"loop"` was removed; `rota config check` fails on a config that still sets it. For unattended work use [parallel rounds](parallel-rounds.md) and [unattended rounds](unattended-rounds.md).

## The two levels

| Value | Behavior |
|-------|----------|
| `"off"` (default) | Skills surface a one-line suggestion at each decision point and stop. The user picks. Same hand-on-the-wheel feel as 1.5.x. |
| `"auto"` | One-hop chaining. `/rota-work` ends with a one-line `/rota-learn` nudge at either level and never chains. After `/rota-debug` commits a fix, `/rota-ship` is invoked automatically. After `/rota-ship` integrates, `/rota-learn` is invoked. After `rota update` reports `behind`, Step 4 asks once via `AskUserQuestion` and dispatches `rota init` on confirm so drift clears in one step. The chain stops after the chained step; the user picks the next item themselves. |

## Autonomy and rounds

`autonomy.level` is about skills chaining into each other. A [parallel round](parallel-rounds.md) is a
different axis: `round.scope` says which issues the round may take, and the `rota round` verbs do the
assigning, waiting and merging, whatever the level. What does not move with the level is the
[merge approval](parallel-rounds.md#merge-approval) policy: `ship.mergeApproval` binds the merge verbs
at `off` and `auto` alike. For a round that runs with no one at the keyboard, see
[unattended rounds](unattended-rounds.md).

## What still gates the chain

Autonomy decides whether to invoke the next skill; the destination skill's own gates still decide whether it pauses. So:

- `learn.verify: true` (or `--strict`): `/rota-learn` still runs the Opus verifier even when invoked under autonomy.
- `ship.review: true`: `/rota-ship` still runs `/rota-review` and blocks on FAIL.
- `refactor.confirmBeforeExecute: true`: `/rota-refactor --fix` still confirms its candidate list.

Gates that stay manual at every level: public-artifact gates and committed-boundary gates (`rota gate list`). The tag push, release publish and, when `ship.mergeApproval` asks for it, merges are enforced by the verbs themselves: they exit 4 until a human answer is passed with `--confirm`. `/rota-decide` approvals, PR opening and upstream issue closing honor their `**Manual gate: ...**` callout no matter what `autonomy.level` says.

## When to flip it on

`"auto"` is good when you want the obvious follow-up step of each cycle (capture learnings, ship the fix) without typing the command yourself. Leave it `"off"` when you are exploring, when items need different judgement calls, or when you want a checkpoint after every step.

See [vision and plans](vision-and-plans.md) for milestone-driven planning and [running work](running-work.md) for the work-cycle endpoints where autonomy fires.
