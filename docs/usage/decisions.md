# Decisions

`.rota/DECISIONS.md` records hard boundaries the project has committed to. It sits alongside `.rota/KNOWLEDGE.md`, but the two play different roles. Knowledge is passive: gotchas and conventions to remember if relevant. Decisions are commitments with forbids and permits that future work must respect.

## Decisions vs learnings

| | Knowledge (`/rota-learn`) | Decisions (`/rota-decide`) |
|---|---|---|
| Voice | "remember this if relevant" | "this is committed, do not violate" |
| Structure | one-liner per bullet | rule + *why* + **forbids** + **permits** |
| Capture | auto in `auto` mode | always manual, always confirmation-gated |
| Reaction at consult | advisory; informs the approach | hard constraint; violations FAIL |
| When to use | "we discovered that the API returns 200 on auth failure" | "we will never store session tokens client-side" |

When unsure, try articulating **forbids** and **permits**. If you can't, it's a learning. If you can, it's a decision.

## Capturing a decision

Run `/rota-decide` when you've reached a commitment. The skill drafts a four-part entry (rule, why, forbids, permits) from conversation context, classifies it by topic, and asks for confirmation before writing. Nothing is written without your "Write it" answer, even in `autonomy.level: auto`.

If you can't articulate forbids or permits, the skill suggests [`/rota-learn`](learning.md) instead and stops. It does not auto-invoke `/rota-learn`; you re-run it yourself.

The skill also runs a three-gate pre-write check: a candidate must be (a) hard to reverse, where undoing it would mean coordinated edits across many files, retraining habits, or migrating data; (b) surprising without context, where a future contributor wouldn't infer the rule from existing patterns alone; and (c) the result of a real trade-off, where genuine alternatives existed and the project deliberately didn't pick them. If any gate fails, the skill suggests `/rota-learn` (or "leave it inline at the call site") and stops without writing. The gates apply across the default, `--from-learning`, and `--from-spike` modes; all routes through `/rota-decide` go through the same filter.

## Promoting a learning or spike into a decision

When a `KNOWLEDGE.md` learning has hardened into a commitment, or a [`/rota-spike`](spikes.md) concluded with a verdict the project is committing to, you can seed the decision draft from the source artifact instead of retyping:

| Flag | Source | Pre-fills |
|------|--------|-----------|
| `/rota-decide --from-learning <topic>` | A bullet under `<topic>` in `.rota/KNOWLEDGE.md` (the skill picks the bullet: auto when there's only one, picker when there are several) | Rule from the bullet; Why cites the topic + date stamp |
| `/rota-decide --from-spike <name>` | `.rota/spikes/<name>.md`: question, decision, recommended approach | Rule keyed off the verdict (`viable`: "use it"; `not viable`: "do not use it"; `depends-on-X`: "use only when X"); Why summarizes the question + findings |

Both flags only seed `Rule` and `Why`. You still articulate `Forbids` and `Permits`; that's what makes the entry a decision rather than a learning. The Step 5 confirmation gate still runs; nothing is written until you approve.

`inconclusive` spikes can't be promoted, since the verdict isn't a commitment yet. Add findings on the spike branch, re-run `/rota-spike done <name>`, then come back.

`/rota-spike`'s Finish mode also nudges this flow: when a spike concludes `viable`, `not viable`, or `depends-on-X`, it prints a one-line pointer to `/rota-decide --from-spike <name>`. It does not ask or dispatch it. See [spikes](spikes.md) for the full Finish-mode flow.

## Where decisions are consulted

| Skill | When |
|---|---|
| `/rota-work` | Step 4 plan phase. Workers receive matching entries as a `**Hard boundaries:**` block; the orchestrator stops and surfaces a task that would violate one before dispatching. |
| `/rota-debug` | Pre-hypothesis. Boundaries can rule out fix directions before cycles are wasted. |
| `/rota-plan` | Plan-write phase. Boundaries become hard constraints in the plan's design. |
| `/rota-refactor` | Orient phase. Recorded decisions are respected; one is reopened only on real friction, and the finding says so. |
| `/rota-review` | Review checklist. Reviewer FAILs on any forbidden pattern in the diff. |
| `/rota-vision` | Milestone planning. Boundaries constrain what milestones can promise. |

`/rota-spike` and [`/rota-ship`](review-and-ship.md) do not consult. Spikes are throwaway by definition, and ship only bundles (review already covers it).

## Suggest nudges

[`/rota-work`](running-work.md) and [`/rota-debug`](debugging.md) can end with a one-line pointer to `/rota-decide` (`/rota-work` when the post-cycle trigger gate fires, `/rota-debug` when the fix locked in a boundary). Neither ever invokes it, whatever [`autonomy.level`](autonomy.md) says, since decisions are your call.

## File location

`.rota/DECISIONS.md` is tracked by default, so decisions travel with the repo alongside `KNOWLEDGE.md`. To keep decisions private, add `.rota/DECISIONS.md` to `.gitignore`.

## See also

- [`/rota-decide` skill](../../skills/rota-decide/SKILL.md) for the capture flow itself
- [Knowledge verbs](../reference/cli-helpers.md#rota-knowledge) for the parallel pattern used by `/rota-learn`
- Sibling persistence skill: [`docs/usage/learning.md`](learning.md) covers both topic-bullet learnings and `--term <name>` Glossary capture
