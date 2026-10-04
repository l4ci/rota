# Learning

`/rota-learn` pulls durable knowledge out of a session and writes it to
`KNOWLEDGE.md` so future runs don't rediscover the same gotchas.

## /rota-learn

`/rota-learn` scans the current session, extracts non-obvious knowledge, groups
the entries by topic, and writes them to [`.rota/KNOWLEDGE.md`](../reference/rota-folder.md). After writing, it
updates the managed `rota-knowledge` block in `CLAUDE.md` so the topic list stays
in sync. [`/rota-work`](running-work.md) reads that index to decide when the current task should
consult `KNOWLEDGE.md`.

## What gets captured (and what doesn't)

**Captured:**

- Gotchas: non-obvious failure modes
- Conventions: project-specific patterns that aren't obvious from reading code
- Constraints: invariants, compatibility rules
- Debugging insights: root causes for bugs that took effort to track down
- Decisions with rationale
- Tool quirks

**Not captured:**

- Things already documented in code or README
- Transient session state
- Obvious facts derivable from the codebase
- Restatements of framework docs
- Personal preferences

## How `KNOWLEDGE.md` is organized

Entries are grouped under short topic headings such as `Build & Tooling`,
`Testing`, or `Networking`. Within each topic, the newest bullets sit at the
top. New entries carry an HTML-comment date stamp (`<!-- YYYY-MM-DD -->`) so
you can tell at a glance how fresh a piece of knowledge is.

In **umbrella mode**, `KNOWLEDGE.md` is hybrid: cross-repo learnings live in
the umbrella `.rota/KNOWLEDGE.md`, repo-local ones in
`.rota/knowledge/<name>/KNOWLEDGE.md`. `/rota-learn` (and `--term` Glossary
entries) routes to the scope resolved from cwd or an explicit `--repo`; at
the umbrella root it asks once whether a learning is umbrella-shared or
sub-repo-scoped. DECISIONS stays umbrella-only. Single-repo projects are
unaffected. Full model: [`references/persistence-skills.md`](../../references/persistence-skills.md#umbrella-scoping).

## Promotion lifecycle

`KNOWLEDGE.md` bullets carry a tier (`provisional`, `confirmed`, or `deprecated`) tracked in a sidecar (`.rota/knowledge-tier.json`) along with a hit counter. Tiers separate "we wrote this down once" from "we've validated this repeatedly in real cycles."

| Tier | Meaning |
|------|---------|
| `provisional` | Default for new bullets. Same advisory weight as before, but not yet load-bearing. |
| `confirmed` | Promoted after `learn.promoteThreshold` clean hits (default 3) or manually via `/rota-learn --promote`. |
| `deprecated` | Manually marked stale via `/rota-learn --deprecate`, or auto-flagged for review when a contradiction lands. |

### Hit tracking

Every time [`/rota-work`](running-work.md) or `/rota-review` consults a bullet and the consuming step actually uses it (not just reads past it), the bullet's hit counter increments via `rota knowledge hit`. Hits accumulate across sessions; the counter is durable.

At the `learn.promoteThreshold` mark, a `provisional` bullet auto-promotes to `confirmed`. Two skip conditions: the bullet already has a pending contradiction (auto-promotion stays blocked until you resolve it), or the threshold is `0` (in which case `provisional` is bypassed entirely on insert: a config choice, not a normal mode).

See [`learn.promoteThreshold`](configuration.md#learnpromotethreshold) for tuning.

### Contradiction queue

When `/rota-work` or `/rota-review` consumes a `confirmed` bullet and observes behavior that contradicts it (the rule said X, the code or test shows ¬X), the bullet lands in `.rota/knowledge-contradictions.json` rather than getting silently demoted. Mid-cycle is the wrong moment to commit to a demotion: the contradiction might be the bug under investigation, not a stale truth.

`/rota-learn` reads the queue at the start of a session and prompts for each pending entry:

1. *Demote (Recommended)*: flips the bullet to `deprecated`.
2. *Keep, false positive*: leaves the tier unchanged.

Either choice clears the entry from the queue.

### Manual lifecycle flags

Three explicit flags bypass the heuristic flow when you already know what you want:

- `/rota-learn --promote <topic> "<title>"`: sets the bullet to `confirmed` without waiting for hits. Use for a brand-new lesson you trust on the strength of the session that produced it.
- `/rota-learn --deprecate <topic> "<title>"`: marks `deprecated`. Manual deprecations do NOT touch the contradiction queue. That queue is for heuristic candidates only.
- `/rota-learn --amend <topic> "<title>"`: rewrites the body of one bullet while preserving its tier and hits. Use when you want sharper wording without resetting the validation history.

All three operate on `(topic, title)` pairs. The tier sidecar is the source of truth for the lifecycle state. `KNOWLEDGE.md` itself stays human-readable without tier annotations.

## When to invoke

Invoke `/rota-learn` after a session that surfaced discoveries: two or more
gotchas resolved in a cycle, a broad change touching many files, or a hard bug
whose root cause wasn't obvious. Skip it for single-item fixes and mechanical
changes where nothing worth re-using was learned.

Skills nudge or auto-invoke `/rota-learn` depending on your
[autonomy](autonomy.md) level. At lower autonomy levels you get a prompt; at
higher levels the skill runs automatically at the end of a work cycle.

## Verification

`learn.verify` in `.rota/config.json` controls a second-opinion pass, off by
default. Set it to `true`, or pass `--strict` for one run, and `/rota-learn` dispatches a fresh Opus subagent that reads only the updated
`KNOWLEDGE.md` diff (no session context) and judges each new bullet on four
criteria: durable (not ephemeral), sharp (concrete claim, not vague), correctly
topic'd, and non-duplicate. The verifier can demote weak entries, sharpen vague
wording, re-file wrong-topic bullets, or delete restatements of existing
knowledge.

| Value | Behavior |
|-------|----------|
| `true` or `--strict` | After writing, run the verifier. Catches weak, duplicate, or wrong-topic entries before they accrete. Adds one Opus roundtrip per `/rota-learn` call. |
| `false` (default) | Skip the verifier. `/rota-learn` writes and reports immediately. Faster and cheaper. |

See [configuration](configuration.md) for the full `learn.verify` setting.

## CLAUDE.md integration

`/rota-learn` keeps the managed `rota-knowledge` block in the project instructions file (`AGENTS.md` when present, else `CLAUDE.md`) in sync with
the topic headings in `KNOWLEDGE.md`. Each time `/rota-learn` runs it rewrites
that block to reflect the current topic list. `/rota-work` reads this index at the
start of a task to decide whether the task at hand warrants consulting
`KNOWLEDGE.md` before planning begins.

## Knowledge vs decisions

If you find yourself wanting to write *"we will never X"* or *"X is forbidden in
this codebase,"* that belongs in a [decision](decisions.md). Knowledge is
advisory ("remember this if relevant"). Decisions are hard boundaries that FAIL
review when violated. Use `/rota-decide` for the latter. It captures
rule + why + forbids + permits and is consulted as a constraint by `/rota-work`,
[`/rota-debug`](debugging.md), [`/rota-plan`](vision-and-plans.md), [`/rota-refactor`](../reference/slash-commands.md#rota-refactor), [`/rota-review`](review-and-ship.md), and [`/rota-vision`](vision-and-plans.md).

## See also

- Sibling persistence skill: [`docs/usage/decisions.md`](decisions.md)
