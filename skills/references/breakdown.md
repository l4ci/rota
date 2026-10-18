# Breakdown

Used by `/rota-plan` (slice mode and the 3+ split), `/rota-vision` (Step 6 and the hand-off) and `/rota-capture` (milestone children). It defines how a milestone or a Major feature is broken into issues, so the children can be checked against the milestone with `rota milestone check`.

## When it applies

A milestone or a Major feature that splits into three or more issues, the same threshold as the confirm rule in `plan-edge-cases.md`. A split into one or two items, and any bug or task, keeps today's body shape.

## The milestone side

The milestone body carries two sections the children are checked against.

- `## Capabilities` lists one contract per bullet: a verb, endpoint, schema or table, config key or skill step. Tag each `(new)`, `(modified)` or `(removed)`. An untagged bullet counts as modified.
- `## Acceptance` lines are `- [ ]` checkboxes, so each gets an `AC-n` id in order.

Capability names are the vocabulary the children copy. Write each once, short, and never as a sentence. An old milestone body is migrated once with `rota migrate milestones --apply`.

## The child issue body

````
## Change
MODIFIED rota round assign
Serves: M08 AC-2
Before: ...            # MODIFIED: recommended
After: ...
Reason: ...            # REMOVED: required
Migration: ...         # REMOVED: required

## Acceptance
- [ ] WHEN <trigger> THEN <observable>

## Touches
- rota round assign    # copied from the milestone's Capabilities

## Out of scope
- the sibling issues, by number or title

## Depends on
````

The rules:

- The first line of `## Change` is `ADDED`, `MODIFIED`, `REMOVED` or `RENAMED`, then the capability name. A RENAMED line reads `RENAMED <old> to <new>`, and the old name is the listed capability.
- The capability named on the `## Change` line must be one of the milestone's Capabilities. The check fails otherwise.
- `Serves:` cites the milestone criterion as `<MNN> AC-n`.
- A REMOVED change needs both `Reason:` and `Migration:`. The check fails without them.
- `Before:`, `After:` and the WHEN/THEN acceptance form are recommended. The check does not enforce them.
- `## Touches` entries are the capability names. Two siblings on one capability clash in `rota round candidates` with no extra declaration.
- `## Out of scope` names the siblings, so a worker does not absorb their work.
- `## Depends on` is written by `--depends-on` or the batch file, never by hand (`references/dependent-items.md`).

## The confirm line

Before filing, show one line per item and ask a single confirm covering granularity and edges:

```
N. MODIFIED <capability>, serves M08 AC-n, depends on <numbers or none>, delivers <what works when it merges>
```

## Filing and checking

File through `rota item create --batch-file`. Records go in dependency order, `dependsOn` points at the ids just printed, and `milestone` is set.

Then run `rota milestone check <MNN>` and fix every gap it names: features with no valid `## Change`, a `## Change` on an unlisted capability, unknown `Serves:` ids, REMOVED items missing a field, and `## Touches` entries that are not a listed capability. Leave a criterion uncovered only when the milestone's `## Not yet specified` explains it. `--strict` fails on uncovered criteria, so run it once the milestone is meant to be fully specified.
