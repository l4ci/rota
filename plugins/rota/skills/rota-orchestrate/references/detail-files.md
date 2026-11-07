# Detail files

Used by `/rota-capture` Step 5. Single-consumer extraction, kept for rota-capture's readability. Any future skill that captures then extracts bulky content cites the same pattern.

When a captured item's raw input is bulky enough to bloat the backlog entry beyond ~3 sentences, the extra content lives in `.rota/<bugs|features|tasks>/<id>.md` and the backlog entry carries a `Detail:` pointer.

## When this fires

An item's input has bulky raw data that would push the backlog entry past ~3 sentences: crash dumps, stack traces, log output, specs, checklists, config snippets, long reproduction steps. Skip items that fit in 1–3 sentences; most don't need a detail file.

## The detail-file shape

Start from `rota item template --kind <bugs|features|tasks> [--tag <tag>]`, the same skeleton Step 6 uses. Put the short description under Summary and bulky raw input under Detail, preserved verbatim or lightly formatted for readability. Fill the other sections from the user's input. The binary owns the section list and its required/recommended levels.

## Ordering

1. Write the detail content (shape above, `{ID}` as a placeholder) to a scratch file.
2. Run `rota item create --kind <bugs|features|tasks> --title ... --body-file <scratch-file>` (rota-capture Step 6). It mints the ID, writes `.rota/<kind>/{ID}.md` with `{ID}` replaced, and appends the backlog entry with the `Detail:` reference.

With `issues.source` set to `github` or `gitlab` there is no `.rota/<kind>/` file: the content becomes the issue body and the ID is the issue number with its type letter.

## The Detail: reference

Appended to the backlog entry after the summary, before `Related:` / `Milestone:` / `Repos:`. Format:

```
Detail: .rota/<kind>/{ID}.md
```
