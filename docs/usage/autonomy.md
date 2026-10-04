# Autonomy levels

`autonomy.level` controls whether each skill nudges you with a one-line suggestion at decision points or invokes the next skill directly. Three levels, mutually exclusive: `"off"`, `"auto"`, `"loop"`.

## The three levels

| Value | Behavior |
|-------|----------|
| `"off"` (default) | Skills surface a one-line suggestion at each decision point and stop. The user picks. Same hand-on-the-wheel feel as 1.5.x. |
| `"auto"` | One-hop chaining. After `/rota-work` finishes a cycle, `/rota-learn` is invoked automatically (when its threshold trips), and `/rota-refactor` is invoked when the refactor-age threshold trips. After `/rota-debug` commits a fix, `/rota-ship` is invoked automatically. After `/rota-ship` integrates, `/rota-learn` is invoked. After `rota update` reports `behind`, Step 4 asks once via `AskUserQuestion` and dispatches `rota init` on confirm so drift clears in one step. The chain stops after the chained step; the user picks the next item themselves. |
| `"loop"` | Auto chain plus loop continuation, plus auto-pick on routine routing. After each `/rota-work` or `/rota-ship` cycle, `/rota-work` (no argument) is invoked. `/rota-work` (no argument) (also reading `autonomy.level`) auto-selects the suggested item and dispatches `/rota-work` without asking. Routine routing/tagging questions that present a clear `(Recommended)` option (milestone tagging in `/rota-capture`, reconcile resolution in `/rota-work` (no argument), CONCERNS routing in `/rota-ship`, scope and candidate gates in `/rota-refactor`) are silently auto-picked without prompting. Design decisions, manual public-artifact gates, and config flips still surface for explicit user input. After `rota update` reports `behind`, Step 4 dispatches `rota init` unconditionally (no question); if the plugin wasn't actually updated, the STALE migration is a no-op. The loop sustains itself until the backlog drains, a guard fails, or the user interrupts. |

## Autonomy and rounds

`autonomy.level` is about skills chaining into each other. A [parallel round](parallel-rounds.md) is a
different axis: `round.scope` says which issues the round may take, and the `rota round` verbs do the
assigning, waiting and merging, whatever the level. What does not move with the level is the
[merge approval](parallel-rounds.md#merge-approval) policy: `ship.mergeApproval` binds the merge verbs
at `off`, `auto` and `loop` alike. For a round that runs with no one at the keyboard, see
[unattended rounds](unattended-rounds.md).

## What still gates the chain

Autonomy decides whether to invoke the next skill; the destination skill's own gates still decide whether it pauses. So:

- `learn.verify: true` (or `--strict`): `/rota-learn` still runs the Opus verifier even when invoked under autonomy.
- `ship.review: true`: `/rota-ship` still runs `/rota-review` and blocks on FAIL.
- `refactor.confirmBeforeExecute: true`: `/rota-refactor --fix` still confirms its candidate list.

## Stop conditions in loop mode

The loop stops cleanly on any of:

- `/rota-work` (no argument) reports an empty backlog (no items in active milestone, no items in general backlog).
- `/rota-work` Step 2 detects an ambiguous brief on a non-Major or untagged item. (Major + Milestone-tagged items defer to Step 4's auto-dispatch chain, which auto-resolves design via `/rota-brainstorm --auto-loop` and plan via `/rota-plan --auto-loop` instead of stopping.) Invisible defaults across a queue defeat the loop's point for cheap items. The user resolves and re-invokes `/rota-work` (no argument) to continue.
- A guard fails (dirty tree, `/rota-review` FAIL, missing brief).
- The user interrupts.

## What loop mode auto-picks vs. surfaces

In loop mode, AskUserQuestion calls fall into three buckets:

- **Auto-picked silently**: routine routing/tagging questions where the `(Recommended)` option is the obvious right answer. Examples: which milestone to tag captured items with, whether to ship/resume a paused branch, where to send review concerns, which sub-repos to refactor. The loop proceeds as if you'd picked the Recommended option.
- **Surfaced for design decisions**: when an `AskUserQuestion` covers a design pick with multiple plausible interpretations (a competing approach, a version-bump escalation), loop mode stops and asks. F32 (loop-mode auto-planning) extends this further with `[Auto:Loop]` decision logging when /rota-plan needs to resolve open questions, but until then design questions break the loop until you answer them.
- **Always manual regardless of autonomy**: public-artifact gates and committed-boundary gates (`rota gate list`). The tag push, release publish and, when `ship.mergeApproval` asks for it, merges are enforced by the verbs themselves: they exit 4 until a human answer is passed with `--confirm`. `/rota-decide` approvals, PR opening and upstream issue closing honor their `**Manual gate: ...**` callout no matter what `autonomy.level` says.

If a routine routing prompt does fire under loop mode, that's a sign the auto-pick branch is missing at that call site. File it as a bug.

**Loop-mode auto-dispatch chain.** For Major + Milestone-tagged items in loop mode, [`/rota-work`](running-work.md) Step 4 runs a three-step research → plan chain before any worker dispatches:

1. **Design pre-flight.** When `.rota/designs/<ID>.md` is absent, dispatch [`/rota-brainstorm`](#) `--auto-loop`. Auto-resolves design questions (local-first, then bounded web, then placeholder), logs `[Auto:Loop]` decisions for fresh picks, writes the design with `auto: true` frontmatter.
2. **Uncertainty pre-flight.** Run the structural-triple check (no detail file / 2+ question marks or `TBD`/`unclear` markers / no backticked identifiers). When uncertain, run [`/rota-work --preview`](picking-work.md) inline; the peek lands in the orchestrator session and informs the subsequent auto-plan.
3. **Plan dispatch.** Dispatch [`/rota-plan`](vision-and-plans.md) `--auto-loop`. Reads the design as soft input, auto-resolves open questions, writes the plan with `auto: true` frontmatter.

The chain runs only in loop mode; off and auto modes still let you invoke `/rota-brainstorm`, `/rota-work --preview`, and `/rota-plan` manually.

## When to flip it on

`"auto"` is good when you want the obvious follow-up step of each cycle (capture learnings, ship the fix) without typing the command yourself. `"loop"` is good when you have a known queue you want drained: milestone seed items, a pile of P2 bugs, a well-specified multi-day backlog. You'd rather inspect the result than steer each pick. Leave it `"off"` when you're exploring, when items in the backlog need different judgement calls, or when you'd rather not run a long session of model spend without checkpoints.

## Picking by phase

A rough phase mapping:

- `"off"` for exploring or steering. You're shaping the work, not draining a queue.
- `"auto"` once a milestone is in flight and a plan is sketched. The follow-up step of each cycle (learn, ship) gets handled; you still pick the next item.
- `"loop"` for a known, well-specified queue. `/rota-work` (no argument) picks and dispatches for you until the backlog is empty.

See [vision and plans](vision-and-plans.md) for milestone-driven planning and [running work](running-work.md) for the work-cycle endpoints where autonomy fires.
