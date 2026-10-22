# Release-range review (Step 5)

Before the notes go to the maintainer, one model family other than the one that wrote most of the code reads the whole range since the last tag (#1612). It checks cross-cutting risks a per-PR review cannot see, and only those. It records no verdict and gates nothing: its findings sit beside the draft in Step 6.

## Harness

The first `claude` or `codex` entry of `round.harnesses` (`rota config show round.harnesses`) that is not the harness you run in. None (the key is unset or lists only yours): print `release review: no other harness in round.harnesses, skipped` and go on to Step 6.

## Inputs

With `<prev_tag>` from Step 4 (no tag: the whole history, and say so):

1. `git archive HEAD | tar -x -C <scratch>` and `mkdir <scratch>/.review`.
2. `{ echo "Review token: <hex>"; git log --format='%h %s' <prev_tag>..HEAD; git diff --stat <prev_tag>..HEAD; git diff <prev_tag>..HEAD; } > <scratch>/.review/range.txt`.
3. The Step 5 draft to `<scratch>/.review/notes.md`, and `CHANGELOG.md` is already in the extract.

Run it with [`references/cross-harness-run.md`](references/cross-harness-run.md) (*Run*, *Account*, *Read the output*) from inside the extract, with this brief and the token line at the end:

```
Review a release range on cross-cutting risks only. You run in an extract of the
release head; .review/range.txt holds the commits and the diff since the last tag,
.review/notes.md the draft release notes. Do not edit files and do not run tests.

Report only these, one line each with file:line or commit:
1. Contract or JSON changes (verb output fields, flags, file formats) that the notes
   do not mention.
2. Exit-code changes.
3. Migrations and new, renamed or removed config keys, and whether the notes say how
   to move.
4. CHANGELOG.md or notes claims the code does not back.
Write `none` under a heading with nothing to report. Skip style, naming and per-PR
bugs: each PR was reviewed on its own.
```

## Show

Trim the reply to the four headings and show it under the draft in Step 6 as `## Cross-harness review (<harness> <model>)`. When the run is unavailable, show one line instead: `release review: <harness> unavailable (<reason>)`. Never retry on another harness and never stop the release for it. Delete the extract afterwards.
