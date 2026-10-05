---
name: rota-plan
description: Write an implementation plan as a first-class artifact before execution — keyed by item (#42, or M01-B07 when milestone-tagged) or milestone slice (M01-S01). One proposal, goal, approach, tasks with verifiable outcomes, open questions, assumptions. /rota-work consults the plan if present. Use when an item or slice is too big to one-shot, or when alignment matters before code lands.
---

# rota-plan — Implementation Plan as Artifact

Write a plan the user signs off on before `/rota-work` runs. A plan is keyed by a backlog item — `#42` (issue backend; file backend `B07`, `F03`, `T11` keep the `M01-B07` form) — or by a milestone slice, `M01-S01`. **A milestone is never required.** On the issue backend an item plan is a note on the item's issue; a slice plan lives on the milestone's tracking issue. On the file backend it is `.rota/plans/<key>.md`.

## Step 1 — Resolve target

- **Item** (`#42`, `B07`, …) — item mode. The key is the item ref as given; if the item carries a milestone tag, `M01-B07` is equally valid but not needed.
- **Milestone** (`M01`) — slice mode; the next slice number is minted.
- **Free-form** (*"plan the OAuth work"*) — find the item with `rota backlog` / `rota item show`; ask only if it stays ambiguous.

`rota plan show <key>` exits 3 for no plan. If one exists, ask once: view, edit (use it as the starting proposal), or replace (`rota plan rm <key>` first).

When the item carries a `Repos:` field, keep it for `--repos` in Step 4 (multi-repo: the full comma list). Slice plans carry no repo.

## Step 2 — Load context, once

Follow `references/context-load-protocol.md` (parallel, silent): item detail and thread (`decision` comments bind), K+D queries, git history, existing plans. For an item, also `rota design show <ID>`; a design from `/rota-brainstorm` carries the chosen approach, so mirror it rather than re-exploring. For a slice, read the milestone (`rota milestone show <MID>`). For a `Repos:` item, resolve it via `.rota/repos.json`. DECISIONS matches are boundaries: redesign, or surface the conflict, before proposing.

**Acceptance present in the issue body (or the design)? Skip ahead.** Treat the checklist as the goal and go to Step 3. If it is absent, draft one (`- [ ]` observable outcomes) as part of the proposal and store it in the design: `rota design add <ID> --title "<title>"` if none exists, then `rota design put <ID> --body-file <file>`.

**Grep before claims.** Validate quantified claims in the detail (*"≥5 callers"*, *"9 helpers"*) with `git grep` before drafting tasks. The detail describes intent; the codebase is ground truth. Likewise `ls -d` any skill folder or path you cite.

## Step 3 — One proposal

Show the plan as plain markdown, not yet saved:

- **Goal** — one sentence
- **Approach** — 3-6 sentences: the design choice and why
- **Tasks** — each with **Observable behavior** (true after it ships), **Files**, **Interfaces**, **Verify** (the command or check that proves it done)
- **Review Focus** — `## Review Focus`, at most 5 lines: risky inputs or edges the spec implies but never names
- **Open questions** — decisions needed before or during execution
- **Assumptions** — implicit constraints made explicit

Rules:

- **Verify is non-negotiable.** No verify step, no task.
- **Interfaces** is `Consumes:` (types, functions, files the task relies on) and `Produces:` (what it creates for later tasks). Write `none` rather than omit a line.
- **Review Focus** entries are each pinned by a test in the owning task's Verify. An edge with no test goes in Open questions instead.
- Tasks fit one execution window; too big means two tasks.
- Vertical slivers, not horizontal layers: each task crosses every layer it needs (UI + logic + data) to be observable.
- No half-implementations: real runnable code, no stubs.
- A rename and its incoming-link sweep are one task; derive the file list from `git grep -l "<old-name>"`.
- Doc deliverables under `docs/` (or `docs.path`) must land in an existing doc home; otherwise raise it as an Open question (umbrella: a sibling `<repo>-docs` is the usual home).

**Self-check before asking.** Silently verify: every Acceptance criterion maps to a task; no placeholder text (`TBD`, `...`, `similar to Task N`); every name, path and signature matches across tasks, including each task's Consumes against an earlier task's Produces. Fix misses in the draft, then say in one line what you fixed (omit the line if nothing).

Ask for approval once: *yes* writes it, *changes* means apply the user's edits and write. Don't loop. Silence is not approval; ask *"Confirm this plan? (yes / changes)"* once. If the redirect moves the Goal itself, re-propose.

## Step 4 — Write

```bash
KEY=$(rota plan add --json '#42' --title "<title>" | jq -r .data.key)                 # item, no milestone needed
KEY=$(rota plan add --json '#42' --title "<title>" --repos web,api | jq -r .data.key) # Repos: item (multi-repo items pass the full comma-list)
KEY=$(rota plan add --json --milestone <MID> --slice --title "<title>" | jq -r .data.key) # slice
```

Quote `#42` so the shell keeps it. Pass `--design <ID>` when a design exists; the frontmatter records the pointer. On the issue backend draft the sections in a scratch file and publish with `rota plan put <key> --body-file <file>|-`; on the file backend `Edit` the stub's sections and keep the frontmatter. List with `rota plan list [--milestone <M>]`. Record plan-shaping answers with `rota item comment add <ID> --kind decision --body-file -`.

Then `rota plan validate-docs <key>` (advisory, exits 0): for each `data.mismatches` entry append an Open question naming the path, target repo and suggestion.

## Step 5 — Report

```
Plan written: #42 — Auth foundation
  Tasks: 4   Open questions: 1
Next: /rota-work #42 (or --preview to peek first).
```

Offer `/rota-work` as a one-line prompt if the user is ready.

## Key principles

- **Plans are committed alignment, not rough notes.** If the user wouldn't sign off, don't write it.
- **Verify is non-negotiable.**
- **Open questions beat hidden assumptions.**
- **Tasks fit one execution.**

## References

- [`references/context-load-protocol.md`](references/context-load-protocol.md) — shared parallel context load.
