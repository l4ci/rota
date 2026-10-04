# Milestone tagging

Used by `/rota-capture` Step 4.5. Single-consumer extraction, kept for rota-capture's readability.

When `/rota-capture` produces new TODO items and there's at least one active milestone, the items get tagged into the active milestone via an `AskUserQuestion` flow.

## Gate

```bash
rota milestone active --json
```

- If `data.ids` is empty, no milestones are active — skip this step entirely.
- If exactly one milestone is active, ask the obvious-default question (below).
- If multiple milestones are active, ask the multi-active question (no auto-default).

## One active milestone

- **Header:** `"Milestone"`
- **Question:** *"Tag these items with `<MID> — <title>`?"* (using the active milestone's title from its frontmatter `title:`)
- **Options** (single-select):
  1. *"Yes — tag all (Recommended)"*
  2. *"No — leave untagged"*
  3. *"Different milestone"* (free text — accepts any existing `M\d+`)

## Multiple active milestones

- **Header:** `"Milestone"`
- **Question:** *"Tag these items with which milestone?"*
- **Options** (single-select):
  1. One option per active milestone, labelled `<MID> — <title>` (title from each milestone file's frontmatter; mark the first listed `(Recommended)`)
  2. *"None / unrelated — leave untagged"*
  3. *"Different milestone"* (free text — accepts any existing `M\d+`)

## Ambiguous reply

If the reply is ambiguous, default to leaving the items untagged. Under-tagging is recoverable; mis-tagging clutters the milestone view.

## Outcome

Carry the chosen milestone(s) as a comma-separated list (`"M01"` or `"M01, M03"`) into rota-capture's Step 6 `Milestone:` suffix on the TODO entry. If *"No — leave untagged"* was picked, omit the suffix entirely.

## What this reference does NOT cover

- **Sub-repo tagging (`Repos:`)** — see rota-capture Step 4.6 inline / `references/umbrella-mode.md` for the registry context.
- **Detail-file extraction for bulky items** — see `references/detail-files.md`.
- **The TODO-entry write itself** — see rota-capture Step 6 inline.
