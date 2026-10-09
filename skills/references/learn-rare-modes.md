# rota-learn rare modes

`/rota-learn` loads this file only when the args carry one of the manual flags below, or at session end when the contradiction queue is non-empty. The default capture flow never needs it.

## Contents

- Manual flags
  - `--retro`
  - `--term <name>`
  - `--promote <topic> "<title>"`
  - `--deprecate <topic> "<title>"`
  - `--amend <topic> "<title>"`
- Process contradiction candidates

## Manual flags

Each flag skips discovery (Steps 2 to 7 of the skill) and exits after its one report line.

### `--retro`

Turns the session's mistakes into the right artifact, not a knowledge bullet by default. Mechanical mistakes become deterministic checks; judgement calls become written standards.

**1. Collect mistakes.** Locate the current session transcript and read it: Claude Code keeps JSONL files under `~/.claude/projects/<project-dir>/` (or `$CLAUDE_CONFIG_DIR/projects/` when set), Codex under `~/.codex/sessions/`. Pick the newest file whose cwd matches this worktree. If none is found, say so in one line (`No transcript found; retro runs from session memory and recent commits.`) and fall back to session memory plus recent commits. Scan it for: user corrections, reverted or redone work, failed gates or tests that a check would have caught, wrong-file or wrong-tree edits, and wasted tool calls. One line per mistake: what went wrong, what would have prevented it.

Every repeated-work, ignored-plan or skipped-step mistake must cite transcript evidence (file plus line or entry number) before it is classified or filed. A mistake with no citable evidence is dropped, or listed as `unverified` in the report and never filed as a guardrail. With no transcript, no mistake can cite one: mark those `unverified` too. No mistakes: say so and stop.

**2. Classify each mistake** into exactly one class:

| Class | Fits when | Destination |
|---|---|---|
| Guardrail | A machine can detect it (lint, test, hook, CI or smoke check) | A new item (below). Never implemented inline. |
| Written standard | Needs judgement; no mechanical check exists | `rota knowledge add` (Step 5 of the skill), or `/rota-decide` when it is a hard boundary |
| Navigation pointer | The agent looked in the wrong place or missed a file | A `rota map` entry or a line in `AGENTS.md`/`CLAUDE.md` |
| Tool-economy fix | A tool or command was used wastefully (full suite per task, broad search, repeated reads) | A skill or brief edit, or a config default; file an item when it spans files |

When a mistake is both mechanical and judgement-heavy, prefer the guardrail: a check cannot be forgotten.

**3. File guardrails.** One item per guardrail candidate:

```bash
printf '%s' "$BODY" | rota item create --kind tasks --title "<check> guards against <mistake>" --desc "<one line>" --body-file -
```

The body names the mistake, quotes its transcript citation (file and line or entry number), the check that would catch it, and where it would run (lint, test, hook, CI). Dedup against open items first (`rota backlog list`). Do not write the check in this run.

**4. Write the rest.** Written standards, navigation pointers and tool-economy fixes that need no new item go through their normal verbs. Apply the skill's *Skip* list from Step 2: no restating code, no transient state.

**5. Flag no-op bullets.** Run `rota knowledge tier list --json` and read `data.entries`. Candidates: entries with `hits: 0` and a `lastSeen` older than 30 days, and bullets whose body only restates code or docs (read the bullet via `rota knowledge query "<topic>"` to judge). List them as removal candidates with the reason. **Never delete, deprecate or edit them**: removal is the user's call (`/rota-learn --deprecate` once they agree).

**6. Report** one compact block:

```
Retro: 5 mistakes
  Guardrail (2): filed #301, #302
  Standard (1): Build & Tooling :: <title>
  Navigation (1): AGENTS.md line added
  Tool economy (1): filed #303
No-op bullet candidates (not touched): 2
  <topic> :: <title> — 0 hits since <date>
```


### `--term <name>`

Captures a domain term into the pinned `## Glossary` topic of `.rota/KNOWLEDGE.md`.

**Required:** `--def "<text>"` — one-paragraph canonical definition (single paragraph, no nested headings).
**Optional:** `--alias "a,b,c"` (comma-separated synonyms), `--not "x,y"` (near-miss disambiguators), `--touch` (force-bump the date stamp on an existing-term update).

Shell command shape:
```bash
rota glossary write "<name>" --def "<text>" [--alias "a,b"] [--not "x,y"] [--touch]
```

The verb reads the Glossary topic, checks aliases for collisions with other terms, inserts (alphabetically) or updates the entry, and regenerates the managed `<!-- rota-knowledge-start -->` block in the instructions file. Exit 4 on alias collision (`blockedBy: alias-collision`: an alias matches one already attached to a different term in Glossary); on collision, surface the error and stop without writing.

Definitional-signal autowrite: when the user says something like *"by X I mean Y"*, *"let's call this X"*, or *"X means Y"* in a normal session (no `--term` flag), the orchestrator may run this verb inline without `/rota-learn`. The flag is the user-facing entry point; the inline form covers conversational writes.

Report one line:

```
Captured term: <name> in .rota/KNOWLEDGE.md ## Glossary
```


### `--promote <topic> "<title>"`

Sets the bullet's tier to `confirmed`, bypassing the hit-threshold path.

Shell command shape:
```bash
rota knowledge tier set --topic "<topic>" --title "<title>" --tier confirmed
```

`tier set` always writes the new tier (idempotent: promoting an already-confirmed bullet changes nothing). Report one line:

```
Promoted: <topic> :: <title> → confirmed
```


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

**Important:** manual deprecations do NOT touch the contradictions queue. Do NOT call `rota knowledge contradiction clear` here: the queue holds heuristic candidates only.


### `--amend <topic> "<title>"`

Rewrites the body of one bullet while preserving its tier and hits in the sidecar.

`rota knowledge amend` only APPENDS to the bullet body. To correct stale wording in place, use `rota knowledge replace --topic "<topic>" --old "<text>" --new "<text>"`: it swaps an exact substring inside the one bullet that contains it, refuses (exit 4) when the text sits in several bullets, and re-keys the tier entry if the bold title changes. This flow stays append-only.

Flow:
1. Ask the user for the new body suffix:
   - Header: `"Amend bullet"`
   - Question: *"Enter the text to append to `<topic> :: <title>` (appended to the existing body):"*
   - Free-text field (single-line or multi-line).
2. Call:
   ```bash
   printf '%s' "<new body suffix>" | rota knowledge amend --topic "<topic>" --fragment "<unique fragment from existing title>" --mode append --body-file -
   ```
   `--fragment` can be the title text itself (unique by (topic, title)).
3. The sidecar entry stays untouched, so tier and hits are preserved.
4. Read back the current tier and hits via `rota knowledge tier get --topic "<topic>" --title "<title>"` and report:

```
Amended: <topic> :: <title> (tier=<tier>, hits=<hits> preserved)
```


## Process contradiction candidates

Run after the Confirm step of a normal capture.

Read pending contradictions:

```bash
rota knowledge contradiction list --json
```

Read `data.items`. If empty, skip this step silently.

For each candidate `{topic, title, correctionText, loggedAt}`, ask:

- **Header:** `"Demote?"`
- **Question:** *"This learning was implicated by user feedback during the session: `<correctionText>`. Demote `<topic> :: <title>` to `deprecated`?"*
- **Options** (single-select):
  1. *"Demote (Recommended)"* — call `rota knowledge tier set --topic <T> --title <S> --tier deprecated`
  2. *"Keep — false positive"* — leave tier unchanged
  3. *"Defer to next session"* — keep candidate in the queue

Clear each candidate as soon as it is resolved (Demote or Keep), never a deferred one:

```bash
rota knowledge contradiction clear --topic <T> --title <S>
```

A deferred candidate stays in the queue, so `rota knowledge contradiction list` shows it next session. Never run a bare `rota knowledge contradiction clear` here: it empties the whole queue.

Report results in the confirm output as:

```
Cleared N contradictions: <demoted-count> demoted, <kept-count> kept, <deferred-count> deferred (still queued)
```
