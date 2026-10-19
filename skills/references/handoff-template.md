# Handoff note template

Used by `/rota-pause` (writes the handoff note) and `/rota-work` with no argument (reads it). Both skills point here so the template lives in one place.

Fill each section from the current session. Omit sections that don't apply; don't manufacture content. The four sections below are exactly what `/rota-work` consumes (its no-argument mode reads Stage, Next planned step and Current hypothesis). Anything else (commit log, files mid-edit, gotchas, dead ends) belongs in `git log`, `git status` or `/rota-learn`.

An orchestrator mid-round writes a different handoff note, to `.rota/handoff/<base>.md` with first line `<!-- rota-handoff: orchestrator -->`; its sections are listed in `/rota-pause` *Pausing an orchestrator*.

## Template

```markdown
# Handoff — <branch>

<!-- Paused YYYY-MM-DD HH:MM UTC -->

## Working on

- **Repo:** web                              <!-- omit when single-repo / no umbrella scope -->
- **Items:** [B07], [F03]
- **Milestone:** M01 — Auth foundation  <!-- omit if no active milestone or items aren't tagged -->
- **Stage:** <e.g., "mid-hypothesis verification for B07", "tasks 1-2 of 4 done (task ledger)">
- **Debug session:** fix-7                    <!-- slug of an open `rota debug session list` entry for this work; omit when none -->

## Next planned step

<one or two sentences — the concrete action `/rota-work` should dispatch. Not a summary; a directive. Start from the first task the task ledger shows unfinished.>

## Current hypothesis (if debugging)

<the causal claim under test, with the verification probe that was about to run>

## Uncommitted work

<one of: "clean tree" / "stashed as `stash@{0}` — message: rota-pause <branch>" / "wip commit `a1b2c3d`" / "dirty tree — see `git status`">
```

## Session report

`/rota-pause --report` writes `.rota/handoff/<branch>.report.md` beside the note. It is for the human: `/rota-work` never reads or deletes it, and `rota status handoff` returns the note path only. Same secret redaction as the note.

```markdown
# Session report — <branch>

<!-- Paused YYYY-MM-DD HH:MM UTC -->

## Commits

<`git log --format='%h %s' <base>..HEAD`, base from `rota git base`>

## Files changed

<`git diff --stat <base>...HEAD`>

## Tasks

<finished `Task: <key>/<n>` markers from the task ledger; "none" when empty>

## Proof

<`rota proof show <ID>` per item; "none" when empty>
```
