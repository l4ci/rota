# Planning dial

Used by `/rota-plan` (Step 2) and `/rota-work` (Step 4). It picks how much planning an item earns before code lands. The dial is keyed on what a wrong plan would cost, not on how big the item looks.

## Levels

| Level | What it adds | Artifact |
|---|---|---|
| 1. Pointers | Nothing beyond today's plan: goal, tasks, files, verify. | The plain plan (`/rota-plan`), or the item brief alone for a one-task change. |
| 2. Spec | Acceptance written first as observable outcomes, the chosen approach and its rejected alternative. | A design (`/rota-brainstorm`), then the plan mirrors it. |
| 3. Research round | Open technical questions answered by trying, before the plan commits to an answer. | A finding (`/rota-spike`), or a `light` subagent sweep for read-only questions. |
| 4. Adversarial pass | Someone argues against the finished plan: failure modes, rollback, what the tests would not catch. | A grilling pass (`references/grilling.md`), plus the `/rota-plan` critic (`references/plan-critic.md`), before the plan is confirmed. |

Levels stack: 4 includes 2, and includes 3 when a question is open. Level 1 is the default and changes nothing for items that rate lowest.

## Rubric

Rate the item on three questions about a **wrong plan**. Each is `low`, `mid` or `high`.

| Question | low | mid | high |
|---|---|---|---|
| **Expensive**: what does building the wrong thing cost? | One task, minutes to redo. | Several tasks or a re-review cycle. | A round of work, or other items built on top. |
| **Hard to undo**: can reverting the PR undo it? | Yes, a clean revert. | Revert works but leaves cleanup (renamed flags, stale docs). | No: a migration, a published format, a released API, data already written. |
| **Invisible**: would a mistake show before the user relies on it? | Tests or the diff show it at once. | Shows only in a specific path or config. | Passes every check and fails later (see `references/silent-failure-hunter.md`). |

Pick the level from the ratings:

- Any `high` on *hard to undo*, or two `high` anywhere: **4, adversarial pass**.
- Else a `high` on *invisible*, or an open technical question that changes the approach: **3, research round**.
- Else any `mid`, or a behavior change with more than one reasonable design: **2, spec**.
- Else all `low`: **1, pointers**.

When two levels fit, take the lower one and say what would push it up. The dial never blocks: the user can override the level in a word.

## Worked examples

Rota's own work:

- **1, pointers.** Fix a stale link in `docs/`. Cheap, reverts cleanly, `bash test/doclint.sh` shows it. A plain plan or no plan.
- **2, spec.** A new `rota` verb with flags and JSON output. Reverting is clean, but two designs are reasonable and the flag names stick once users type them. Write acceptance and the chosen shape first.
- **3, research round.** Moving gate checks to run concurrently. Whether it is safe depends on what the worktree guard and lockfiles tolerate, and nobody has measured it. Spike first, then plan on the finding.
- **4, adversarial pass.** A `rota migrate` step that rewrites tracked `.rota/` files. A revert does not restore migrated user repos, and a bad migration passes the smoke fixtures that never held old-format data. Argue against the plan (rollback, partial runs, re-runs) before confirming it.

## Stating the level

The skill that applies the dial states the result in one line to the user, then continues:

```
Planning dial: 2 (spec) — new verb, two reasonable shapes; revert is clean. Override: say "level 1" or "level 4".
```

Keep it to that line: no ratings table in the output.
