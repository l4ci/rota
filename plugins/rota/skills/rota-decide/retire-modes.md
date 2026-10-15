# Lifecycle modes — `--supersede` and `--retire`

A lifted boundary is marked in `.rota/DECISIONS.md`, never hand-edited away. The entry stays in the file as history; `rota decisions query` and the decisions index block skip it, and a topic left with no active entry drops out of both.

## `--retire <topic> "<title>"`

The boundary is lifted and nothing replaces it.

1. Find the `### <title>` entry under `## <topic>` (case-insensitive; one match required, else list candidates and stop). Refuse an entry that already carries a `*Status.*` line.
2. Show the entry and ask for confirmation (Step 5 shape): Header `"Retire"`, Question *"Retire this decision? Its forbids stop applying."*, options *"Retire it (Recommended)"* / *"Cancel"*. Write only on an explicit "Retire it". Same manual gate as a new decision, even in `autonomy.level: auto`. Cancel stops with *"Decision unchanged."*
3. Append to the entry, before its `<!-- YYYY-MM-DD -->` stamp:

   ```markdown
   *Status.* Retired YYYY-MM-DD. <one-line reason from the user>
   ```

4. Run `rota block decisions` (Step 7).

## `--supersede <topic> "<title>"`

A new decision replaces the old one.

1. Find the old entry as above.
2. Run Steps 2–5 for the replacement, unchanged: **all three gates still apply**, the four parts are drafted, topic classified. A replacement that fails a gate is not written and the old entry stays active (suggest `--retire` if the boundary is simply lifted).
3. One confirmation covers both writes: Question *"Lock in the replacement and supersede `<old title>`?"*, options *"Write it (Recommended)"* / *"Edit first"* / *"Cancel"*.
4. On "Write it": insert the new entry per Step 6, and append to the old entry:

   ```markdown
   *Status.* Superseded YYYY-MM-DD by "<new title>".
   ```

5. Run `rota block decisions` (Step 7).

## Confirm

```
Retired 1 decision in .rota/DECISIONS.md:
  Architecture — "<title>"
```

or, for `--supersede`, `Superseded "<old>" with "<new>" under <topic>.` plus the index-block line from Step 8.

The `*Status.* (Retired|Superseded)` line is what `rota decisions query/topics/stats` and the index block key on: keep that exact prefix.
