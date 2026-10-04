# rota-learn rare modes

Loaded by `/rota-learn` only when the args carry one of the manual flags below, or at session end when the contradiction queue is non-empty. The default capture flow never needs this file.

## Manual flags

Each flag skips discovery (Steps 2 to 7 of the skill) and exits after its one report line.

### `--term <name>`

Captures a domain term into the pinned `## Glossary` topic of `.rota/KNOWLEDGE.md`.

**Required:** `--def "<text>"` — one-paragraph canonical definition (single paragraph, no nested headings).
**Optional:** `--alias "a,b,c"` (comma-separated synonyms), `--not "x,y"` (near-miss disambiguators), `--touch` (force-bump the date stamp on an existing-term update).

Shell command shape:
```bash
rota glossary write "<name>" --def "<text>" [--alias "a,b"] [--not "x,y"] [--touch]
```

Reads the existing Glossary topic, performs cross-term alias-collision uniqueness check, inserts (alphabetically) or updates the entry, regenerates the managed `<!-- rota-knowledge-start -->` block in the instructions file. Exit 4 on alias collision (`blockedBy: alias-collision`: an alias matches one already attached to a different term in Glossary); on collision, surface the error and stop without writing.

Definitional-signal autowrite — when the user phrases something like *"by X I mean Y"*, *"let's call this X"*, or *"X means Y"* during a normal session (not via the explicit `--term` flag), the orchestrator may run this same verb inline without going through `/rota-learn`. The flag form is the user-facing entry point; the inline form keeps the trio's old conversational-write behavior alive.

Report one line:

```
Captured term: <name> in .rota/KNOWLEDGE.md ## Glossary
```

Then exit (skip remaining steps).

### `--promote <topic> "<title>"`

Sets the bullet's tier to `confirmed`, bypassing the hit-threshold path.

Shell command shape:
```bash
rota knowledge tier set --topic "<topic>" --title "<title>" --tier confirmed
```

`tier set` always writes the new tier (idempotent — promoting an already-confirmed bullet is a no-op in effect). Report one line:

```
Promoted: <topic> :: <title> → confirmed
```

Then exit (skip remaining steps).

### `--deprecate <topic> "<title>"`

Sets the bullet's tier to `deprecated`.

Shell command shape:
```bash
rota knowledge tier set --topic "<topic>" --title "<title>" --tier deprecated
```

Report one line:

```
Deprecated: <topic> :: <title> → deprecated
```

**Important:** manual deprecations do NOT touch the contradictions queue. Do NOT call `rota knowledge contradiction clear` here — the queue is for heuristic candidates only, not for manually declared deprecations.

Then exit (skip remaining steps).

### `--amend <topic> "<title>"`

Rewrites the body of one bullet while preserving its tier and hits in the sidecar.

`rota knowledge amend` only APPENDS to the bullet body. To correct stale wording in place, use `rota knowledge replace --topic "<topic>" --old "<text>" --new "<text>"`: it swaps an exact substring inside the one bullet that contains it, refuses (exit 4) when the text sits in several bullets, and re-keys the tier entry if the bold title changes. This flow stays append-only.

Flow:
1. Prompt the user via `AskUserQuestion` for the new body suffix:
   - Header: `"Amend bullet"`
   - Question: *"Enter the text to append to `<topic> :: <title>` (V1: appends to existing body):"*
   - Free-text field (single-line or multi-line).
   - In loop-mode (`autonomy.level: loop`), this is an error — `--amend` requires explicit body input from the user; print `"Error: --amend requires user-provided body — cannot auto-pick in loop mode."` and exit 1.
2. Call:
   ```bash
   printf '%s' "<new body suffix>" | rota knowledge amend --topic "<topic>" --fragment "<unique fragment from existing title>" --mode append --body-file -
   ```
   The `--fragment` can be the title text itself (it is unique by (topic, title)).
3. The sidecar entry is left untouched — tier and hits are preserved.
4. Read back the current tier and hits via `rota knowledge tier get --topic "<topic>" --title "<title>"` and report:

```
Amended: <topic> :: <title> (tier=<tier>, hits=<hits> preserved)
```

Then exit (skip remaining steps).

## Process contradiction candidates

Run after the Confirm step of a normal capture.

Read pending contradictions:

```bash
rota knowledge contradiction list --json
```

Read `data.items`. If empty, skip this step silently.

For each candidate `{topic, title, correctionText, loggedAt}`, surface via `AskUserQuestion`:

- **Header:** `"Demote?"`
- **Question:** *"This learning was implicated by user feedback during the session: `<correctionText>`. Demote `<topic> :: <title>` to `deprecated`?"*
- **Options** (single-select):
  1. *"Demote (Recommended)"* — call `rota knowledge tier set --topic <T> --title <S> --tier deprecated`
  2. *"Keep — false positive"* — leave tier unchanged
  3. *"Defer to next session"* — keep candidate in the queue

**Loop-mode auto-pick:** *"Defer to next session"* — per the manual-gate rule that demotions need user confirmation.

**V1 simplification:** after processing ALL candidates (regardless of per-candidate choice), call:

```bash
rota knowledge contradiction clear
```

This clears the entire queue. Fine-grained deferral (keeping only deferred items) is a V2 polish.

Track results in the confirm output as:

```
Cleared N contradictions: <demoted-count> demoted, <skipped-count> skipped
```

(Where "skipped" covers both "Keep — false positive" and "Defer to next session" choices.)
