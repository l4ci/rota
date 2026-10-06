---
name: rota-capture
description: Use when the user brain-dumps work, says "capture", "add to backlog", "note this bug", "/rota-capture", or describes a problem without asking for an immediate fix. Also use on "remove [B07]", "delete this entry", "drop this item" to delete captured items.
---

# rota-capture — Capture & Manage Work Items

Quick-capture bugs, features, and tasks with just enough context to act on them later. Items are created with `rota item create` on the configured backlog backend (`backlog.backend`): a tracker issue (`#N`) or a `.rota/BACKLOG.md` entry (`[B07]`). Handles multiple items and mixed types in one pass. `--remove <ID>` strips an item (the local inverse of capture).

## Step 1 — Task list

Track the steps below with the host's task tool if it has one.

## Step 1.5 — Mode Dispatch

| First arg | Mode |
|-----------|------|
| `--remove <ID>[,<ID>...]` | [Remove Mode](#remove-mode) |
| anything else / nothing | Step 2 onward |

Skip Steps 2 to 7 in Remove Mode.

## Step 2 — Parse & Classify

The user gives a keyword, phrase or longer description, possibly several issues of mixed types. **Split it into distinct items**, each a separate concern that would get its own ID. Clues: separate sentences about unrelated problems, "also…", "plus…", a list, mixed bug/feature/chore language.

| `--kind` | When the item describes… |
|----------|--------------------------|
| `bugs` | Broken behavior: worked and stopped, or doesn't work as expected |
| `features` | New or enhanced behavior that doesn't exist yet |
| `tasks` | Chores and maintenance: refactoring, dependency updates, docs, CI, cleanup |

(File backend: these land under `## Bugs`, `## Features`, `## Tasks` of `.rota/BACKLOG.md`.)


## Step 2.5 — Audit Against Code State (milestone-spec capture only)

Fires only when the input captures *from a milestone spec*: it names an `M<NN>` tag or a `milestones/M<NN>.md` path. Otherwise skip.

Milestone specs drift behind code. Capturing criteria that already shipped under other IDs creates duplicate work and wastes a run on finished work. Run `rota item shipped "<title 1>" "<title 2>" …` with the parsed titles. Exit 0 means ship evidence was found (stdout lists hits per title, `--json` has `data.titles[].hits`); exit 1 means none, continue silently.

On exit 0, print the report verbatim, then `AskUserQuestion`, up to 4 flagged titles per call. Header `"Item N"`. Question: *"`<short-title>` looks shipped — `<hash>` `<subject>`. What now?"* Options:
  1. *"Skip this item (Recommended)"* — drop it from this run.
  2. *"Capture anyway"* — the user reviewed the matches and the item is genuinely distinct.
  3. *"Stop the whole capture"* — print *"Capture aborted — reconcile the milestone spec before retrying."* and write nothing.

Filtered titles never reach the backlog.

## Step 3 — Gather Context

Gather **just enough context** to make each item actionable later. Answer from the code first, then ask what it can't settle.

**Code first.** Before asking anything, resolve what the code can answer: grep the filenames, commands and skill names the user mentioned, and run `rota map query <name>` for a matching subsystem. That settles the component or area, current behavior, and the error path or message text. Pre-fill those facts into the item; never ask the user for them. Read only enough to answer; capture is not an investigation.

**Then ask**, 2 to 4 quick questions total across all items, not per item, only for what the code can't settle (intent, expected behavior, trigger, urgency). Skip anything the user already answered; a detailed input may need none. Every question carries a recommended answer: mark one option `(Recommended)` or state the default in the question, so the user only confirms or redirects. Pick from:

- **Bugs:** expected vs. actual, trigger steps, every time or intermittent, which view/component, error output.
- **Features:** user-facing behavior, which part of the app, existing workaround, what triggers the need.
- **Tasks:** goal, area of the codebase, deadline or dependency, relevant context (error output, PR link).

The cap stays at 4. Code reads supply the recommended answers and never add questions.

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

Tagging is optional: an untagged item is fully workable, plannable and shippable.

Tag only when the user named a milestone (`--milestone M01`, *"for M02"*), or when exactly one milestone is active (`rota milestone active --json`, `data.ids`) and the items plainly belong to it. In that case ask one question, `AskUserQuestion`, single-select: *"Tag these with `<MID> — <title>`?"* — *"Yes — tag all"* / *"No — leave untagged (Recommended)"*. With no active milestone, or several and none named, skip the step and leave the items untagged. An ambiguous reply means untagged; under-tagging is recoverable, mis-tagging clutters the milestone view.

Carry the choice (`"M01"` or `"M01, M03"`) as `--milestone` into Step 6. Omit it when untagged.

## Step 4.6 — Tag Sub-Repo (when umbrella mode is on)

When `rota repo umbrella` exits 0 and `.rota/repos.json` registers at least one sub-repo (the registry is the truth, not the config flag), read [`umbrella-tagging.md`](umbrella-tagging.md) and run it: it asks which sub-repo(s) the item belongs to and yields the `--repos` value for Step 6. Otherwise skip silently.

## Step 5 — Handle Large Input

When an item's input would bloat the entry beyond about 3 sentences (stack traces, logs, specs, long repro), use `references/detail-files.md` and pass the file as `--body-file`. Skip this for items that fit in 1 to 3 sentences.

## Step 6 — Create All Items

**Consult the Glossary.** Scan the `## Glossary` topic of `.rota/KNOWLEDGE.md` (`rota glossary read <term>`). If the user's phrasing maps to a canonical term or alias, write the canonical name silently (no question) and add one line to the report: `Used canonical term "<term>" for "<phrasing>".` Spend a question only when one phrase maps to two glossary entries; ask which, with the likelier entry `(Recommended)`, and count it against the Step 3 cap. If the capture introduces a new domain concept the user names, suggest `/rota-learn --term <name>` afterwards; never auto-invoke.

Create each item in one command; it prints the new ID:

```bash
ID=$(rota item create --json --kind bugs --title "Short title" --tag P1 --desc "Description." --related "[F02]" | jq -r .data.id)
```

Flags: `--kind bugs|features|tasks`, `--tag` (`P0`-`P3` for bugs, `Major`/`Minor`/`Cosmetic` for features, none for tasks), `--desc`, `--related`, `--milestone`, `--repos`, `--subsystem`, `--body-file`, `--depends-on`. See `rota item create --help` and `docs/design/contract/backlog.md` (*rota item create*) for ID minting, field order, the `Since:` stamp, detail-file placement and the issue-backend mapping; none of that is the skill's job.

Judgment the skill does own:

- **`--related`:** link only items that clearly relate. Scan open items with `rota backlog list` for connections (file backend: also `.rota/ARCHIVE.md`); items in the same batch can reference each other. Don't force links.
- **`--depends-on`:** when an item clearly needs another open item done first, including one earlier in the same batch, pass it (`references/dependent-items.md`). Create prerequisites first and use the IDs just printed. Related-but-independent items stay `--related`.
- **`--subsystem`:** match filenames and skill names in the user's text against `.rota/map/` (or the `## Project Map` block in CLAUDE.md), e.g. `rota-work` or `rota init`. Pass `Subsystem: <name>` only on a confident match; never block or delay capture for it.
- **`--desc`:** what happens, when, what should happen instead (bugs); what it does, where, why it matters (features); what and why (tasks). One to three sentences.
- **Behavior, not paths:** descriptions and acceptance criteria state observable behavior. File paths and line numbers go in a separate `## Pointers` section of the body (`--body-file`), never in the criteria.
- **`## Out of scope`:** features and Major items get this section in the body (`--body-file`): one to three bullets naming what a worker must not take on, or the single line "nothing noted". `rota round assign` copies it into the worker brief, so write it as the contract's boundary, not as a wish list. Bugs and tasks may omit it.

## Step 7 — Brainstorm Nudge

Fires when the batch includes a `[Major]` feature or a `[P0]` bug; skip otherwise. In every autonomy mode, add one line per qualifying ID to the report, after other post-capture nudges and a blank line:

> *"Run `/rota-brainstorm [ID]` before `/rota-plan` to negotiate the design."*

**Never grill and never invoke `/rota-brainstorm` from here** (`references/grilling.md` is not loaded by capture). Capture is pure intake. Advancement lives in `/rota-work`: with no argument it reconciles and suggests the next item.

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
| [`authoring-conventions.md`](references/authoring-conventions.md) | Destructive and manual gates. |
| [`dependent-items.md`](references/dependent-items.md) | When to declare `--depends-on` and in what order to create. |
| [`detail-files.md`](references/detail-files.md) | Detail-file template for bulky input. |
| [`issue-mode.md`](references/issue-mode.md) | Issue-backend umbrella rules (Step 4.6). |
| [`umbrella-mode.md`](references/umbrella-mode.md) | Umbrella-mode verbs, registry shape, `Repos:` semantics. |
