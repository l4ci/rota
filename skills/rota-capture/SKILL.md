---
name: rota-capture
description: Use when the user brain-dumps work, says "capture", "add to backlog", "note this bug", "/rota-capture", or describes a problem without asking for an immediate fix. Also use on "remove [B07]", "delete this entry", "drop this item" to delete captured items.
---

# rota-capture — Capture & Manage Work Items

Quick-capture bugs, features and tasks with just enough context to act on later. Items are created with `rota item create` on the configured backend (`backlog.backend`): a tracker issue (`#N`) or a `.rota/BACKLOG.md` entry (`[B07]`). Handles several items and mixed types in one pass. `--remove <ID>` deletes an item.

Copy this checklist and track your progress:
```
- [ ] Step 1 — Mode Dispatch
- [ ] Step 2 — Parse & Classify
- [ ] Step 2.5 — Audit Against Code State (milestone-spec capture only)
- [ ] Step 3 — Gather Context
- [ ] Step 4 — Assign Priority / Size
- [ ] Step 4.5 — Tag Active Milestone (when applicable)
- [ ] Step 4.6 — Tag Sub-Repo (when umbrella mode is on)
- [ ] Step 5 — Handle Large Input
- [ ] Step 6 — Create All Items
- [ ] Step 7 — Brainstorm Nudge
```

## Step 1 — Mode Dispatch

| First arg | Mode |
|-----------|------|
| `--remove <ID>[,<ID>...]` | [Remove Mode](#remove-mode) |
| anything else / nothing | Step 2 onward |

Skip Steps 2 to 7 in Remove Mode.

## Step 2 — Parse & Classify

**Split the input into distinct items**, one per concern that would get its own ID (separate sentences, "also…", lists, mixed bug/feature/chore language).

| `--kind` | When the item describes… |
|----------|--------------------------|
| `bugs` | Broken behavior: worked and stopped, or doesn't work as expected |
| `features` | New or enhanced behavior that doesn't exist yet |
| `tasks` | Chores and maintenance: refactoring, dependency updates, docs, CI, cleanup |

(File backend: these land under `## Bugs`, `## Features`, `## Tasks` of `.rota/BACKLOG.md`.)

## Step 2.5 — Audit Against Code State (milestone-spec capture only)

When the input captures from a milestone spec, read [`milestone-spec-audit.md`](milestone-spec-audit.md) and run it: it drops items already shipped.

## Step 3 — Gather Context

**Code first.** Before asking, grep the filenames, commands and skill names the user mentioned and run `rota map query <name>` for a matching subsystem. Pre-fill component, current behavior and error text; never ask the user for them. Capture is not an investigation: read only enough to answer.

**Then ask** 2 to 4 questions total across all items, not per item, only for what the code can't settle (intent, expected behavior, trigger, urgency). Skip what the user already answered; detailed input may need none. Every question carries a recommended answer: mark one option `(Recommended)` or state the default. Pick from:

- **Bugs:** expected vs. actual, trigger steps, every time or intermittent, which view/component, error output.
- **Features:** user-facing behavior, which part of the app, existing workaround, what triggers the need.
- **Tasks:** goal, area of the codebase, deadline or dependency, relevant context (error output, PR link).

Code reads supply recommended answers; they never add questions.

## Step 4 — Assign Priority / Size

| Bugs | Meaning |
|------|---------|
| `[P0]` | Blocks usage: crash, data loss, can't complete core workflow, security issue |
| `[P1]` | Degrades experience: wrong behavior, broken feature, workaround exists |
| `[P2]` | Minor annoyance: cosmetic glitch, edge case, user unlikely to notice |

| Features | Meaning |
|----------|---------|
| `[Major]` | New screens, significant rework, breaks existing patterns, multi-day |
| `[Minor]` | Contained change: new option, small UI addition, 1 to 3 files, hours |
| `[Cosmetic]` | Visual polish: spacing, color, label tweak, minutes |

Tasks get no priority or size tag.

## Step 4.5 — Tag Active Milestone (when applicable)

When the user named a milestone (`--milestone M01`, *"for M02"*) or exactly one milestone is active (`rota milestone active --json`), read [`milestone-tagging.md`](milestone-tagging.md) and run it: it yields the `--milestone` value for Step 6. Otherwise skip and leave untagged.

## Step 4.6 — Tag Sub-Repo (when umbrella mode is on)

When `rota repo umbrella` exits 0 and `.rota/repos.json` registers at least one sub-repo (the registry is the truth, not the config flag), read [`umbrella-tagging.md`](umbrella-tagging.md) and run it: it yields the `--repos` value for Step 6. Otherwise skip silently.

## Step 5 — Handle Large Input

When input exceeds about 3 sentences (stack traces, logs, specs, long repro), use `references/detail-files.md` and pass the file as `--body-file`.

## Step 6 — Create All Items

When the user's phrasing may map to a canonical glossary term (`## Glossary` in `.rota/KNOWLEDGE.md`), read [`glossary-terms.md`](glossary-terms.md) before creating.

Create each item in one command; it prints the new ID:

```bash
ID=$(rota item create --json --kind bugs --title "Short title" --tag P1 --desc "Description." --related "[F02]" | jq -r .data.id)
```

Flags: `--kind bugs|features|tasks`, `--tag` (`P0`-`P3` for bugs, `Major`/`Minor`/`Cosmetic` for features, none for tasks), `--desc`, `--related`, `--milestone`, `--repos`, `--subsystem`, `--body-file`, `--depends-on`. See `rota item create --help` and `docs/design/contract/backlog.md` (*rota item create*) for ID minting, field order, the `Since:` stamp, detail-file placement and the issue-backend mapping.

Judgment calls:

- **`--related`:** link only clearly related items. Scan `rota backlog list` (file backend: also `.rota/ARCHIVE.md`); batch items can reference each other.
- **`--depends-on`:** when an item clearly needs another open item done first, including one earlier in the batch (`references/dependent-items.md`). Create prerequisites first and use the IDs just printed. Independent items stay `--related`.
- **`--subsystem`:** match filenames and skill names in the user's text against `.rota/map/` (or the `## Project Map` block in CLAUDE.md). Pass `Subsystem: <name>` only on a confident match; never delay capture for it.
- **`--desc`:** what happens, when, what should happen instead (bugs); what, where, why it matters (features); what and why (tasks). One to three sentences.
- **Behavior, not paths:** descriptions and acceptance criteria state observable behavior. File paths and line numbers go in a `## Pointers` section of the body (`--body-file`), never in the criteria.
- **`## Out of scope`:** features and Major items get this section in the body (`--body-file`): one to three bullets naming what a worker must not take on, or the line "nothing noted". `rota round assign` copies it into the worker brief, so write it as the contract's boundary. Bugs and tasks may omit it.

## Step 7 — Brainstorm Nudge

Fires when the batch includes a `[Major]` feature or a `[P0]` bug; skip otherwise. In every autonomy mode, add one line per qualifying ID to the report, after other post-capture nudges and a blank line:

> *"Run `/rota-brainstorm [ID]` before `/rota-plan` to negotiate the design."*

**Never grill and never invoke `/rota-brainstorm` from here** (`references/grilling.md` is not loaded by capture). Capture is pure intake; `/rota-work` with no argument suggests the next item.

Print every new ID with its title, then stop. Capture ends here; do not offer to start work.

---

## Remove Mode

Read [`remove-mode.md`](remove-mode.md) when the first arg is `--remove` and follow it (Steps R1 to R5: resolve, dry-run, de-tag upstream, confirm, apply).

## Rules

- Capture only creates items: never remove or reorder existing ones, never investigate now.
- Print every new ID with its title.
- Remove: preview is the default; the de-tag gate (Step R3) and apply gate (Step R4) always ask.

## References

| Reference | Purpose |
|-----------|---------|
| [`milestone-spec-audit.md`](milestone-spec-audit.md) | Step 2.5: drop items already shipped when capturing from a milestone spec. |
| [`milestone-tagging.md`](milestone-tagging.md) | Step 4.5: tag the active or named milestone. |
| [`umbrella-tagging.md`](umbrella-tagging.md) | Step 4.6: pick sub-repos for `--repos`. |
| [`glossary-terms.md`](glossary-terms.md) | Step 6: write canonical glossary terms. |
| [`remove-mode.md`](remove-mode.md) | `--remove` flow, Steps R1 to R5. |
| [`authoring-conventions.md`](references/authoring-conventions.md) | Destructive and manual gates. |
| [`dependent-items.md`](references/dependent-items.md) | When to declare `--depends-on` and in what order to create. |
| [`detail-files.md`](references/detail-files.md) | Detail-file template for bulky input. |
| [`issue-mode.md`](references/issue-mode.md) | Issue-backend umbrella rules (Step 4.6). |
| [`umbrella-mode.md`](references/umbrella-mode.md) | Umbrella-mode verbs, registry shape, `Repos:` semantics. |
