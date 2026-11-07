# Autonomy levels

Used by every skill that reads `autonomy.level` (`rota config show autonomy.level`), and by `/rota-orchestrate`. It states what each level chains, where a run stops and how a stop is raised. One key covers solo skills and rounds. The default is `off`.

## Levels

| Level | Solo skills | Rounds (`/rota-orchestrate`) |
|---|---|---|
| `off` | Each skill suggests the next step in one line and stops. | Process calls (premise, too-vague scope, an `ambiguous.` brief, brainstorm or plan first) go to the maintainer as today. No autopilot tick. |
| `roundtrip` | One item, start to finish: brainstorm (when the planning dial calls for it) → plan → work → review → ship, with no process questions. A design or plan the chain wrote is approved by the chain, and stays on the item for later review. The run stops after that item ships; the human starts the next one. | The orchestrator settles process calls itself and runs the current slate to wind-down, then stops. No autopilot tick: `rota round tick` and `watch --autopilot` refuse. |
| `auto` | As `roundtrip`, then `/rota-work` with no argument picks the next ready item, and so on until one of the stops below. | As `roundtrip`, and the round runs the autopilot tick (`rota round watch --autopilot`), which assigns, gates and merges mechanically. |

Under `roundtrip` and `auto`, report every call you made yourself in one line where you made it, for example `Planning dial: 2 (spec), running /rota-brainstorm`, `Premise: still holds, #812 changed nothing it relies on`, `Tier: heavy, migration`.

## Where a run stops

At every level, these stop and wait for the human:

- Manual gates: tag push, release publish, PR open, public filing and closing (`references/manual-gates.md`). The verbs enforce them whatever the level says.
- `ship.mergeApproval`: a merge it leaves to a person, including in the autopilot tick.
- `/rota-decide`: never auto-invoked.
- The escalation rule's human calls: product behavior, public names, breaking changes, scope cuts.

`auto` also stops, and names which, when:

- a decision above is reached;
- you have no recommendation of your own (a choice you cannot rank is the human's);
- no ready item is left;
- a session or usage limit is near: the `work.contextGuard` / `orchestrator.handoffThreshold` path runs `/rota-pause`.

`roundtrip` stops at the same points, and after its one item ships (or, in a round, at wind-down).

## How a stop is raised

Every stop carries options and a recommendation.

- **Solo.** A blocking question picker, one question, the recommended option first.
- **Round.** `rota round escalate send` with `--options` and `--recommend` (`skills/rota-orchestrate/escalations-and-provenance.md`). The other slots keep working; never a blocking picker.

## What auto does not add

`auto` is not loop autonomy (#70): it adds no loop machinery. Solo `auto` is `/rota-work` with no argument after each ship; rounds reuse the autopilot tick; limits go through `/rota-pause`. It relaxes no verb-enforced gate and no `ship.mergeApproval`.
