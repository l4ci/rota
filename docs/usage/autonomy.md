# Autonomy levels

`autonomy.level` controls how far a run goes before it stops for you. One key covers solo skills and [parallel rounds](parallel-rounds.md). Three levels: `"off"`, `"roundtrip"`, `"auto"`. Any other value fails `rota config check`, and `rota config set autonomy.level <value>` refuses it. `"loop"` was removed. For unattended work see [unattended rounds](unattended-rounds.md).

## The three levels

| Value | Solo skills | Rounds |
|-------|-------------|--------|
| `"off"` (default) | Each skill suggests the next step in one line and stops. You pick. | The orchestrator puts process calls to you (premise, too-vague scope, an `ambiguous.` brief, brainstorm or plan first). No autopilot tick. |
| `"roundtrip"` | One item, start to finish: brainstorm (when the planning dial calls for it), plan, work, review, ship, with no process questions. A design or plan the chain wrote is approved by the chain and stays on the item for later review. The run stops after that item ships. You start the next one. | The orchestrator settles process calls itself and runs the current slate to wind-down, then stops. No autopilot tick. |
| `"auto"` | As `roundtrip`, then `/rota-work` with no argument picks the next ready item, and so on until one of the stops below. | As `roundtrip`, and the round runs the [autopilot](parallel-rounds.md#autopilot) tick, which assigns, gates and merges mechanically. |

Under `roundtrip` and `auto`, a skill reports every call it made itself in one line where it made it, for example `Planning dial: 2 (spec), running /rota-brainstorm`.

The policy the skills follow is in [`skills/references/autonomy.md`](../../skills/references/autonomy.md).

## Where a run stops

At every level these stop and wait for you:

- Manual gates: tag push, release publish, PR open, public filing and closing (`rota gate list`). The verbs enforce them whatever the level says.
- `ship.mergeApproval`: a merge it leaves to a person, including inside the autopilot tick.
- `/rota-decide`: never invoked automatically.
- Product behavior, public names, breaking changes and scope cuts.

`auto` also stops, and says why, when it has no recommendation of its own, when no ready item is left, or when a session or usage limit is near. The last one runs the `work.contextGuard` / `orchestrator.handoffThreshold` path, which is `/rota-pause`. `roundtrip` stops at the same points, and after its one item ships (in a round, at wind-down).

Every stop carries options and a recommendation. Solo skills ask one question in a blocking picker, the recommended option first. A round sends `rota round escalate send` with `--options` and `--recommend`; the other slots keep working.

## Autonomy and rounds

`auto` turns on the round autopilot. `rota round tick` and `rota round watch --autopilot` run only under `autonomy.level: auto`; under `off` or `roundtrip` they exit 4 with "the autopilot runs only under autonomy.level auto" and the hint `rota config set autonomy.level auto`. The key `round.autopilot` is gone. `round.scope` still says which issues a round may take, and `round.autopilotCap` still bounds each tick.

`ship.mergeApproval` binds the merge verbs at every level; see [merge approval](parallel-rounds.md#merge-approval).

## Upgrading from the old `auto`

Before 0.21.0, `auto` meant one-hop chaining. `rota config fill` (which `rota init` and `rota update` run) rewrites an `auto` in a config whose `rota.version` stamp is older than 0.21.0 (or empty or unparseable) to `roundtrip`, in `.rota/config.json` and `.rota/config.local.json`. It prints `rewrote: autonomy.level auto -> roundtrip in ...` and raises the stamp to 0.21.0, so an `auto` you set afterwards stays.

Fill also deletes `round.autopilot` from both files. `rota config check` lists it under REMOVED. When it was `true`, fill prints a line pointing at `rota config set autonomy.level auto`; it does not switch the autopilot on for you.

To roll back after downgrading the binary, run `rota config set autonomy.level auto`: an older binary's `config check` rejects `roundtrip`.

## What auto does not add

`auto` is not the loop autonomy that #70 removed. It adds no loop machinery. Solo `auto` is `/rota-work` after each ship, rounds reuse the autopilot tick, and limits go through `/rota-pause`. It relaxes no verb-enforced gate and no `ship.mergeApproval`.

## What still gates the chain

The destination skill's own gates still decide whether it pauses:

- `learn.verify: true` (or `--strict`): `/rota-learn` still runs the Opus verifier.
- `ship.review: true`: `/rota-ship` still runs `/rota-review` and blocks on FAIL.
- `refactor.confirmBeforeExecute: true`: `/rota-refactor --fix` still confirms its candidate list.

The tag push, release publish and, when `ship.mergeApproval` asks for it, merges are enforced by the verbs: they exit 4 until a human answer is passed with `--confirm`. `/rota-decide` approvals, PR opening and upstream issue closing honor their `**Manual gate: ...**` callout at every level.

## Picking a level

Leave `"off"` when you are exploring, when items need different judgement calls, or when you want a checkpoint after every step. Use `"roundtrip"` when you want one item taken from idea to shipped without questions. Use `"auto"` when you want the next ready item picked up too, or the round autopilot running.

See [vision and plans](vision-and-plans.md) for milestone-driven planning and [running work](running-work.md) for the work-cycle endpoints where autonomy fires.
