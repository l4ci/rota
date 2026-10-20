# Optional paths

## Umbrella projects

`rota refactor targets --json` lists the sub-repos. Run once per sub-repo with the global `--repo <name>` so each finding lands on the owning tracker. Do not fan out sub-agents from here; a round's orchestrator assigns one area per worker.

## Map mode (`--map`)

On demand only; `rota init` never runs it. It reads `models.orchestrator` and nothing else, and replaces Orient to File: no findings, no issues.

1. **Explore.** One exploration subagent (`light`), scoped to `<area>` if given, else the whole repo. It names the subsystems (one coherent area each, at most `map.softcap_subsystems`) and, per subsystem: entry points as `- <path>:<line> — <what>` lines (real, current lines; `rota map stats` counts a reference that no longer resolves as broken), key files or directories, the stack, the conventions specific to it, the commands that test it, and the concerns (fragile spots, known debt). It reports what the code shows, with no invented rationale.
2. **Draft.** For each subsystem take `git rev-parse HEAD` once and write the draft in memory, not to disk:

```markdown
---
subsystem: <name>
summary: <one line>
touched: <today, YYYY-MM-DD>
last_mapped_commit: <full sha of HEAD>
---

## Purpose
## Entry points
- <path>:<line> — <what>
## Key files / dirs
## Stack
## Conventions specific here
## Test commands
## Concerns
```

Keep each file short: a page, not a tour.
3. **Approve.** List every file the run would write, marking each `new` or `exists`. Ask once: write the new files, or pick which. A file that already exists is never overwritten silently: for each, ask overwrite or skip (or show the diff first when the user wants it). With no answer, write nothing.
4. **Write and index.** Write only the approved files, then run `rota map index` and `rota map stats`. Report the files written and skipped. `rota map stats` warns later, once HEAD is more than `map.staleCommits` commits (default 50) past a file's `last_mapped_commit`; re-run `--map` to refresh.

A file the user kept (skipped) keeps its old `last_mapped_commit`, so it still shows as stale until it is remapped.

## Rejections

If the user rejects a finding for a reason a later review must respect, offer `/rota-decide`. Skip ephemeral ("not now") and self-evident reasons.

## Interactive filing

Under `--interactive`, show the ranked list before filing and let the user drop or reorder; design discussion of a chosen finding belongs in `/rota-brainstorm`.
