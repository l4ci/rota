# Detail files

Used by `/rota-capture` Step 5. Single-consumer extraction — the reference exists for rota-capture's readability. Future skills that capture-then-extract bulky content would cite the same pattern.

When a captured item's raw input is bulky enough to bloat the backlog entry beyond ~3 sentences, the extra content lives in `.rota/<bugs|features|tasks>/<id>.md` and the backlog entry carries a `Detail:` pointer.

## When this fires

If any item's input contains bulky raw data that would push the backlog entry past ~3 sentences — crash dumps, stack traces, log output, specs, checklists, config snippets, long reproduction steps. Skip entirely for items that fit comfortably in 1–3 sentences. Most entries won't need a detail file.

## The detail-file shape

```markdown
# {ID}: Short title

> Related TODO entry: `[{ID}]` in `.rota/BACKLOG.md`

## Summary

{The same 1–3 sentence summary that goes into BACKLOG.md}

## Detail

{Full user input — crash dump, stack trace, logs, specs, checklists, etc. Preserved verbatim or lightly formatted for readability.}
```

## Ordering

1. Write the detail content (shape above, `{ID}` as a placeholder) to a scratch file.
2. Run `rota item create --kind <bugs|features|tasks> --title ... --body-file <scratch-file>` (rota-capture Step 6). It mints the ID, writes `.rota/<kind>/{ID}.md` with `{ID}` replaced, and appends the backlog entry with the `Detail:` reference.

With `backlog.backend: "issues"` there is no `.rota/<kind>/` file: the content becomes the issue body and the ID is the issue number with its type letter.

## The Detail: reference

Appended to the backlog entry after the summary, before `Related:` / `Milestone:` / `Repos:`. Format:

```
Detail: .rota/<kind>/{ID}.md
```

## What this reference does NOT cover

- **The BACKLOG-entry write itself** — see rota-capture Step 6 inline.
- **Milestone tagging** — see rota-capture Step 4.5 inline.
- **Sub-repo tagging** — see rota-capture Step 4.6 inline / `references/umbrella-mode.md`.
