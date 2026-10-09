---
name: rota-plan
description: Use when an item or milestone slice is too big to one-shot, or when alignment matters before code lands, or on "plan 42", "write a plan for this".
---

# rota-plan — Implementation Plan as Artifact

Write a plan the user signs off on before `/rota-work` runs. Keyed by a backlog item (`#42` on the issue backend; file backend `B07`, `F03`, `T11` keep the `M01-B07` form) or a milestone slice, `M01-S01`. **A milestone is never required.** Issue backend: an item plan is a note on the item's issue, a slice plan lives on the milestone's tracking issue. File backend: `.rota/plans/<key>.md`.

Copy this checklist and track your progress:
```
- [ ] Step 1 — Resolve target
- [ ] Step 2 — Load context, once
- [ ] Step 3 — One proposal
- [ ] Step 4 — Write
- [ ] Step 5 — Report
```

## Step 1 — Resolve target

- **Item** (`#42`, `B07`, …): item mode; the key is the ref as given (`M01-B07` is equally valid for a milestone-tagged item).
- **Milestone** (`M01`): slice mode; the next slice number is minted.
- **Free-form**: find the item with `rota backlog` / `rota item show`; ask only if still ambiguous.

`rota plan show <key>` exits 3 for no plan. If one exists, ask once: view, edit (use it as the starting proposal), or replace (`rota plan rm <key>` first).

Keep an item's `Repos:` field for `--repos` in Step 4 (full comma list). Slice plans carry no repo.

## Step 2 — Load context, once

Follow `references/context-load-protocol.md` (parallel, silent): item detail and thread (`decision` comments bind), K+D queries, git history, existing plans. For an item, also `rota design show <ID>`; mirror a `/rota-brainstorm` design's chosen approach rather than re-exploring. For a slice, read the milestone (`rota milestone show <MID>`). For a `Repos:` item, resolve it via `.rota/repos.json`. DECISIONS matches are boundaries: redesign, or surface the conflict, before proposing.

**Acceptance in the issue body or design?** Treat it as the goal; go to Step 3. Otherwise draft one (`- [ ]` observable outcomes) as part of the proposal and store it in the design: `rota design add <ID> --title "<title>"` if none exists, then `rota design put <ID> --body-file <file>`.

**Choose the planning level.** Apply [`references/planning-dial.md`](references/planning-dial.md) to the loaded context and state the level in one line to the user. Level 1 (pointers) is today's plain plan: continue to Step 3. A higher level adds its artifact first (design, spike, or a grilling pass before the Step 3 confirm).

**Grep before claims.** Validate quantified claims in the detail (*"≥5 callers"*) with `git grep` before drafting tasks; the codebase is ground truth. `ls -d` any path you cite.

## Step 3 — One proposal

Show the plan as unsaved markdown:

- **Goal**: one sentence
- **Approach**: 3-6 sentences: the design choice and why
- **Tasks**: each with **Observable behavior** (true after it ships), **Serves** (`Serves: AC-1, AC-2`: the acceptance ids it delivers), **Files**, **Interfaces**, **Verify** (the command or check that proves it done)
- **Review Focus**: `## Review Focus`, at most 5 lines: risky inputs or edges the spec implies but never names
- **Open questions**: decisions needed before or during execution
- **Assumptions**: implicit constraints made explicit
- **Relies on**: `## Relies on`, one line per KNOWLEDGE bullet (`<Topic>: <bold title>`) and DECISIONS entry (title) the plan depends on, taken from the Step 2 queries; `none` when it leans on neither

Rules:

- **Verify is non-negotiable.** No verify step, no task.
- **Serves is non-negotiable too.** Every task carries a `Serves: AC-n` sub-bullet (several ids allowed), and every criterion is served by at least one task. A task that serves no criterion is scope creep: drop it or raise the missing criterion first. `rota plan check <key>` enforces this after the write.
- **Behavior tasks name the RED.** Verify states the failure the new test shows before the change (`RED: <command> fails with <expected message>`). A docs or skill-text task with no test seam writes `no test seam: docs/skill change` instead.
- **Interfaces** is `Consumes:` (what the task relies on) and `Produces:` (what it creates for later tasks). Write `none` rather than omit a line.
- **Relies on** lists only entries the plan's approach actually depends on, not every bullet the queries returned. `/rota-review` checks the diff against this list and records a hit only for entries it followed.
- **Review Focus** entries are each pinned by a test in the owning task's Verify. An edge with no test goes in Open questions instead.
- Tasks fit one execution window; too big means two tasks.
- Vertical slivers, not horizontal layers: each task crosses every layer it needs to be observable.
- No stubs: real runnable code.
- When the work is a rename, a wide refactor, or ships docs, read [`plan-edge-cases.md`](plan-edge-cases.md) for the task-shaping rules.

**Self-check before asking** (silent): every Acceptance criterion maps to a task through its `Serves:` line; no placeholders (`TBD`, `...`, `similar to Task N`); names, paths and signatures match across tasks, each Consumes against an earlier Produces. Fix misses, then say in one line what you fixed (omit if nothing).

**Critic (level 4 only).** When the planning dial picked the adversarial pass, follow [`references/plan-critic.md`](references/plan-critic.md): dispatch a fresh `standard` subagent that sees only the item and the proposal, then fold accepted findings into the plan and list rejected ones with a reason under `## Critic findings`, before asking. At levels 1-3 skip this; the flow is unchanged.

Ask once: *"Confirm this plan? (yes / changes)"*. *yes* writes it; *changes* means apply the edits and write; don't loop. Silence is not approval. If the redirect moves the Goal itself, re-propose.

## Step 4 — Write

```bash
KEY=$(rota plan add --json '#42' --title "<title>" | jq -r .data.key)                 # item, no milestone needed
KEY=$(rota plan add --json '#42' --title "<title>" --repos web,api | jq -r .data.key) # Repos: item (multi-repo items pass the full comma-list)
KEY=$(rota plan add --json --milestone <MID> --slice --title "<title>" | jq -r .data.key) # slice
```

Quote `#42`. Pass `--design <ID>` when a design exists; the frontmatter records it. Issue backend: draft in a scratch file and publish with `rota plan put <key> --body-file <file>|-`; file backend: edit the stub's sections, keep the frontmatter. List with `rota plan list [--milestone <M>]`. Record plan-shaping answers with `rota item comment add <ID> --kind decision --body-file -`.

Issue backend: run `rota plan check <key>` (read-only; exit 1 lists uncovered criteria, tasks with no `Serves:` or an unknown AC id, and tasks with no Verify) and fix the plan until it passes. On the file backend it exits 1 with `blockedBy: backend`; rely on the Step 3 self-check there.

When the plan splits into separate items, or after writing, read [`plan-edge-cases.md`](plan-edge-cases.md) for dependent-item filing and the docs-path check.

## Step 5 — Report

```
Plan written: #42 Auth foundation
  Tasks: 4   Open questions: 1
Next: /rota-work #42 (or --preview to peek first).
```

Offer `/rota-work` in one line.

## Key principles

- **Plans are committed alignment, not rough notes.**

## References

- [`plan-edge-cases.md`](plan-edge-cases.md): rename/refactor/docs task rules (Step 3); splitting into dependent items and `validate-docs` (Step 4).
- [`references/planning-dial.md`](references/planning-dial.md): the level rubric Step 2 applies.
- [`references/plan-critic.md`](references/plan-critic.md): the level-4 critic dispatch, brief and fold-in rules (Step 3).
- [`references/dependent-items.md`](references/dependent-items.md): declaring `## Depends on` edges; expand → migrate → contract.
- [`references/context-load-protocol.md`](references/context-load-protocol.md): shared parallel context load.
- [`references/knowledge-consult.md`](references/knowledge-consult.md): the K+D query pattern the load uses.
