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

## Next planned step

<one or two sentences — the concrete action `/rota-work` should dispatch. Not a summary; a directive. Start from the first task the task ledger shows unfinished.>

## Current hypothesis (if debugging)

<the causal claim under test, with the verification probe that was about to run>

## Uncommitted work

<one of: "clean tree" / "stashed as `stash@{0}` — message: rota-pause <branch>" / "wip commit `a1b2c3d`" / "dirty tree — see `git status`">
```
