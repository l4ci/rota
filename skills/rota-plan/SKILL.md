---
name: rota-plan
description: Use when an item or milestone slice is too big to one-shot, or when alignment matters before code lands, or on "plan 42", "write a plan for this".
---

# rota-plan — Implementation Plan as Artifact

Write a plan the user signs off on before `/rota-work` runs. Keyed by a backlog item — `#42` (issue backend; file backend `B07`, `F03`, `T11` keep the `M01-B07` form) — or a milestone slice, `M01-S01`. **A milestone is never required.** Issue backend: an item plan is a note on the item's issue, a slice plan lives on the milestone's tracking issue. File backend: `.rota/plans/<key>.md`.

## Step 1 — Resolve target

- **Item** (`#42`, `B07`, …) — item mode; the key is the ref as given (`M01-B07` is equally valid for a milestone-tagged item).
- **Milestone** (`M01`) — slice mode; the next slice number is minted.
- **Free-form** — find the item with `rota backlog` / `rota item show`; ask only if still ambiguous.

`rota plan show <key>` exits 3 for no plan. If one exists, ask once: view, edit (use it as the starting proposal), or replace (`rota plan rm <key>` first).

Keep an item's `Repos:` field for `--repos` in Step 4 (full comma list). Slice plans carry no repo.

## Step 2 — Load context, once

Follow `references/context-load-protocol.md` (parallel, silent): item detail and thread (`decision` comments bind), K+D queries, git history, existing plans. For an item, also `rota design show <ID>`; mirror a `/rota-brainstorm` design's chosen approach rather than re-exploring. For a slice, read the milestone (`rota milestone show <MID>`). For a `Repos:` item, resolve it via `.rota/repos.json`. DECISIONS matches are boundaries: redesign, or surface the conflict, before proposing.

**Acceptance in the issue body or design?** Treat it as the goal; go to Step 3. Otherwise draft one (`- [ ]` observable outcomes) as part of the proposal and store it in the design: `rota design add <ID> --title "<title>"` if none exists, then `rota design put <ID> --body-file <file>`.

**Grep before claims.** Validate quantified claims in the detail (*"≥5 callers"*) with `git grep` before drafting tasks; the codebase is ground truth. `ls -d` any path you cite.

## Step 3 — One proposal

Show the plan as unsaved markdown:

- **Goal** — one sentence
- **Approach** — 3-6 sentences: the design choice and why
- **Tasks** — each with **Observable behavior** (true after it ships), **Files**, **Interfaces**, **Verify** (the command or check that proves it done)
- **Review Focus** — `## Review Focus`, at most 5 lines: risky inputs or edges the spec implies but never names
- **Open questions** — decisions needed before or during execution
- **Assumptions** — implicit constraints made explicit

Rules:

- **Verify is non-negotiable.** No verify step, no task.
- **Behavior tasks name the RED.** Verify states the failure the new test shows before the change (`RED: <command> fails with <expected message>`). A docs or skill-text task with no test seam writes `no test seam: docs/skill change` instead.
- **Interfaces** is `Consumes:` (what the task relies on) and `Produces:` (what it creates for later tasks). Write `none` rather than omit a line.
- **Review Focus** entries are each pinned by a test in the owning task's Verify. An edge with no test goes in Open questions instead.
- Tasks fit one execution window; too big means two tasks.
- Vertical slivers, not horizontal layers: each task crosses every layer it needs to be observable.
- No stubs: real runnable code.
- A rename and its incoming-link sweep are one task; derive the file list from `git grep -l "<old-name>"`.
- A wide rename-style refactor (too many call sites for one window) is planned as expand → migrate in batches → contract, per `references/dependent-items.md`; each step becomes its own task.
- Doc deliverables under `docs/` (or `docs.path`) must land in an existing doc home; otherwise raise it as an Open question (umbrella: a sibling `<repo>-docs` is the usual home).

**Self-check before asking** (silent): every Acceptance criterion maps to a task; no placeholders (`TBD`, `...`, `similar to Task N`); names, paths and signatures match across tasks, each Consumes against an earlier Produces. Fix misses, then say in one line what you fixed (omit if nothing).

Ask once: *"Confirm this plan? (yes / changes)"*. *yes* writes it; *changes* means apply the edits and write; don't loop. Silence is not approval. If the redirect moves the Goal itself, re-propose.

## Step 4 — Write

```bash
KEY=$(rota plan add --json '#42' --title "<title>" | jq -r .data.key)                 # item, no milestone needed
KEY=$(rota plan add --json '#42' --title "<title>" --repos web,api | jq -r .data.key) # Repos: item (multi-repo items pass the full comma-list)
KEY=$(rota plan add --json --milestone <MID> --slice --title "<title>" | jq -r .data.key) # slice
```

Quote `#42`. Pass `--design <ID>` when a design exists; the frontmatter records the pointer. Issue backend: draft in a scratch file and publish with `rota plan put <key> --body-file <file>|-`; file backend: `Edit` the stub's sections, keep the frontmatter. List with `rota plan list [--milestone <M>]`. Record plan-shaping answers with `rota item comment add <ID> --kind decision --body-file -`.

When the plan splits into separate items (a sliced milestone, or the expand/migrate/contract steps), file each with `rota item create ... --depends-on <prerequisite IDs>` where it clearly needs another open one first, prerequisites first (`references/dependent-items.md`). Tasks inside one plan need no items.

Then `rota plan validate-docs <key>` (advisory, exits 0): for each `data.mismatches` entry append an Open question naming the path, target repo and suggestion.

## Step 5 — Report

```
Plan written: #42 — Auth foundation
  Tasks: 4   Open questions: 1
Next: /rota-work #42 (or --preview to peek first).
```

Offer `/rota-work` in one line.

## Key principles

- **Plans are committed alignment, not rough notes.**

## References

- [`references/dependent-items.md`](references/dependent-items.md) — declaring `## Depends on` edges; expand → migrate → contract.
- [`references/context-load-protocol.md`](references/context-load-protocol.md) — shared parallel context load.
- [`references/knowledge-consult.md`](references/knowledge-consult.md) — the K+D query pattern the load uses.
