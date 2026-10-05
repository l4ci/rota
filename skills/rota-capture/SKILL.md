---
name: rota-capture
description: >-
  Capture bugs, features, and tasks into the backlog without executing them, via `rota item create`, on whichever backend is configured (GitHub/GitLab issues, IDs `#N`, or file BACKLOG.md, IDs `[B07]`). Classifies each item and assigns priority/size. Also supports `--remove <ID>[,<ID>...]` to delete captured items and clean up cross-references (dry-run + confirmation gate). Use when the user brain-dumps work, says "capture", "add to backlog", "note this bug", "/rota-capture", "remove [B07]", "delete this entry", "drop this item", or describes a problem without asking for an immediate fix. Records and prints the new IDs; `/rota-work` picks them up.
---

# rota-capture — Capture & Manage Work Items

Quick-capture bugs, features, and tasks with just enough context to act on them later. Items are created with `rota item create` on the configured backlog backend (`backlog.backend`): a tracker issue (`#N`) or a `.rota/BACKLOG.md` entry (`[B07]`). Handles multiple items and mixed types in one pass. `--remove <ID>` strips an item (the local inverse of capture).

## Step 1 — Task list

Track these phases with the host's task tool if it has one.

- **Capture:** mode / dispatch, audit code state (milestone specs only), classify, dedupe, create, report.
- **Remove (`--remove`):** resolve IDs, preview, de-tag upstream issues, confirm, apply.

## Step 1.5 — Mode Dispatch

| First arg | Mode |
|-----------|------|
| `--remove <ID>[,<ID>...]` | [Remove Mode](#remove-mode) |
| `--from-github` / `--from-gitlab` | Print: *"Import was removed. Under `backlog.backend: "issues"` the issues already are the backlog. On the file backend, create the item by hand: `rota item create --kind <bugs|features|tasks> --title "..." --desc "... GH: #N"`."* Stop. |
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

Milestone specs drift behind code. Capturing criteria that already shipped under other IDs creates duplicate work and wastes a run on finished work (real F27 incident: 11 captures, 10 already shipped). Run `rota item shipped "<title 1>" "<title 2>" …` with the parsed titles. Exit 0 means ship evidence was found (stdout lists hits per title, `--json` has `data.titles[].hits`); exit 1 means none, continue silently.

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

Follow `references/milestone-tagging.md`: the `rota milestone active` gate, and the question shapes. Carry the chosen milestone as `--milestone` in Step 6. Omit it if the user left the item untagged.

## Step 4.6 — Tag Sub-Repo (when umbrella mode is on)

Gate and registry semantics: `references/umbrella-mode.md`. Ask only when `rota repo umbrella` exits 0 and `.rota/repos.json` registers at least one sub-repo (the registry is the truth, not the config flag). Otherwise skip silently.

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`): an item lives on one sub-repo's tracker, so pick exactly one repo (single-select, no multi-repo option) and pass `--repos <name>`. `rota item create` refuses multi-repo items: for work spanning repos, capture one item per repo and link them with `Related:`.

Otherwise ask:

- **Header:** `"Repos"`; **Question:** *"Which sub-repo(s) does this item belong to?"* (with the item's short title)
- **multiSelect:** true
- **Options:** one per `name` in `.rota/repos.json` (mark the likely match `(Recommended)` when the item text names a repo), then *"None / unsure — leave untagged"* last.

Two or more repos make a multi-repo item that `/rota-work` branches in each repo. If *"None / unsure"* comes with concrete names, the names win. An ambiguous reply leaves the item untagged, and `/rota-work` will then refuse it and point back here.

Carry the picks as a comma-separated list of registered sub-repos into `--repos`. Omit the flag if untagged.

## Step 5 — Handle Large Input

When an item's input would bloat the entry beyond about 3 sentences (stack traces, logs, specs, long repro), use `references/detail-files.md` and pass the file as `--body-file`. Skip this for items that fit in 1 to 3 sentences.

## Step 6 — Create All Items

**Consult the Glossary.** Scan the `## Glossary` topic of `.rota/KNOWLEDGE.md` (`rota glossary read <term>`). If the user's phrasing maps to a canonical term or alias, write the canonical name silently (no question) and add one line to the report: `Used canonical term "<term>" for "<phrasing>".` Spend a question only when one phrase maps to two glossary entries; ask which, with the likelier entry `(Recommended)`, and count it against the Step 3 cap. If the capture introduces a new domain concept the user names, suggest `/rota-learn --term <name>` afterwards; never auto-invoke.

Create each item in one command; it prints the new ID:

```bash
ID=$(rota item create --json --kind bugs --title "Short title" --tag P1 --desc "Description." --related "[F02]" | jq -r .data.id)
```

Flags: `--kind bugs|features|tasks`, `--tag` (`P0`-`P3` for bugs, `Major`/`Minor`/`Cosmetic` for features, none for tasks), `--desc`, `--related`, `--milestone`, `--repos`, `--subsystem`, `--body-file`. See `rota item create --help` and `docs/design/contract/backlog.md` (*rota item create*) for ID minting, field order, the `Since:` stamp, detail-file placement and the issue-backend mapping; none of that is the skill's job.

Judgment the skill does own:

- **`--related`:** link only items that clearly relate. Scan open items with `rota backlog list` for connections (file backend: also `.rota/ARCHIVE.md`); items in the same batch can reference each other. Don't force links.
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

The inverse of capture. `rota item rm` owns the mechanics: BACKLOG entry, `Related:` cross-references, the detail file and any plan keyed to the item. It previews by default and only `--apply` writes. ARCHIVE entries stay unless `--scrub-archive`, the only audit trail a removed item has left. Counters never decrement. Contract: `docs/design/contract/backlog.md` (*rota item rm*).

### Step R1 — Resolve IDs

Split the argument on commas and pass the IDs as positionals. Exit 3 means an ID is unknown: show stderr and stop.

### Step R2 — Dry-Run Preview

Run `rota item rm <IDS>` and show stdout verbatim. If an item has `activeBranch` (`--json`: `data.items[].activeBranch`), tell the user: *"The item is active on `<branch>`. Apply refuses it until the stream is dropped with `rota status rm <branch>`."* and continue.

### Step R3 — De-tag Upstream Issues

> Removing the `in-progress` label upstream is externally visible: collaborators see the issue no longer claimed. The item delete proceeds either way; this decides only whether the label is cleaned up too.

Legacy file-backend items carry `GH: #N` / `GL: #N` tags. Find upstream links: `rota issues imported --json`, keep `data.entries` whose `itemId` is in the removal set. Read the label from `rota config show --json issues.label` (default `in-progress`). No matches: skip to Step R4.

Otherwise ask:

- **Header:** `"De-tag"`; **Question:** *"Remove the `<label>` label on <N> upstream issue(s)? <list of #N>."*
- **Options:**
  1. *"Yes, remove the label upstream"* — `rota issues label <issue> --remove "<label>"` per entry, adding `--repo <repo>` when the entry's `repo` is non-null. Propagate a failure.
  2. *"No, just delete the item"* — print: *"Note: upstream issues still carry the `<label>` label. Remove via `gh issue edit <N> --remove-label <label>` or `glab issue update <N> --unlabel <label>` if desired."*

### Step R4 — Confirmation Gate

Show the preview, then one `AskUserQuestion`. Header `"Apply"`, question *"Apply this removal plan for <IDS>?"*:

1. *"Apply (Recommended)"* — `rota item rm --apply <IDS>`; ARCHIVE entries stay as the historical record.
2. *"Apply + scrub ARCHIVE"* — `rota item rm --apply --scrub-archive <IDS>`; also removes the ARCHIVE entry and its cross-references.
3. *"Cancel"* — print *"No changes."* and stop.

Anything but an explicit yes cancels. This is a destructive gate: it always asks.

### Step R5 — Apply

Run the chosen command and pass its per-ID output through verbatim. On exit 4 (`data.blockedBy: "active"`), tell the user to run `rota status rm <branch>` first and stop. Don't nudge any other skill.

**When to use `--remove`:** duplicate of an existing item, wrong premise, made obsolete by other work, captured against the wrong project or milestone, ruled out by a spike or decision, or the user changed their mind. It does not close upstream issues; that is always manual.

---

---

## Rules

- Capture only creates items: never remove or reorder existing ones, never investigate now.
- Print every new ID with its title.
- Remove: preview is the default; the de-tag gate (Step R3) and apply gate (Step R4) always ask.

## References

| Reference | Purpose |
|-----------|---------|
| [`authoring-conventions.md`](references/authoring-conventions.md) | Destructive and manual gates. |
| [`detail-files.md`](references/detail-files.md) | Detail-file template for bulky input. |
| [`issue-mode.md`](references/issue-mode.md) | Issue-backend umbrella rules (Step 4.6). |
| [`milestone-tagging.md`](references/milestone-tagging.md) | Milestone-tagging question shapes (Step 4.5). |
| [`umbrella-mode.md`](references/umbrella-mode.md) | Umbrella-mode verbs, registry shape, `Repos:` semantics. |
