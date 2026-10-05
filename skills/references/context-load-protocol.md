# Context-load protocol

Used by `/rota-work` (Step 4 for the normal flow, `references/work-preview.md` for the peek), `/rota-plan` Step 2, and `/rota-vision` Step 2 — the silent context load that runs before the skill proposes anything to the user. The goal: read everything that informs the planned action in parallel, form a picture, then act.

## The canonical reads

Run as a checklist. Items are ordered by broadening scope (target item → plan → milestone → repo-wide). Skip an item when its precondition doesn't apply — that's not a failure, that's the protocol.

- **The target item entry** in `.rota/BACKLOG.md` (when a specific backlog ID is the target) and its overflow detail file at `.rota/<bugs|features|tasks>/<id>.md` if one exists.
- **The plan file** at `.rota/plans/<key>.md` if one exists for this work. Use:

  ```
  rota plan show <key>
  ```

  A missing plan exits 3 with empty stdout, not a failure. Treat that as "no plan yet".

- **The milestone** (`rota milestone show <MID>`) only when the work is milestone-scoped or the item carries a milestone tag. Never required.
- **Items scoped to the milestone** via:

  ```
  rota backlog ids --milestone <MID>
  ```

  Used by `/rota-plan` and `/rota-vision` to see siblings under the same milestone.

- **KNOWLEDGE + DECISIONS** — see `references/knowledge-consult.md` for the canonical query pattern. Pass the topic names inferred from the work area.
- **Recent git history**:

  ```
  git log --oneline -20
  ```

  Plus `git log --oneline -- <path>` for any probable target file.

## Issue in parallel

All reads in the list above are independent. The calling skill MUST issue them as parallel tool calls in a single response — load latency dominates this step, and serial reads make the skill feel slow without any benefit. Workers reading this reference should treat sequential reads as a planning failure.

## Skill-specific extras

Each calling skill adds its own reads inline. The protocol lists only the common subset. Concretely:

- `/rota-vision` Step 2 adds `.rota/MILESTONES.md`, every `.rota/milestones/M*.md`, glossary terms from `.rota/KNOWLEDGE.md` `## Glossary` (via `rota glossary read`), and stack files (`README.md`, `package.json`, `Cargo.toml`, `pyproject.toml`, etc.) — domain-shape reads that other skills don't need.
- `/rota-work` preview (`references/work-preview.md`) adds Repos: parsing for umbrella items (resolves via `rota repo resolve` when umbrella mode is on).
- `/rota-plan` Step 2 adds `rota plan list` (with `--milestone <MID>` for a slice) to see existing plans.

## What to do with the loaded context

Read for picture, don't dump. The user did not invoke the skill to receive a context dump — they invoked it for the skill's actual deliverable (a peek, a plan, a milestone, a work cycle). Carry what's relevant into the next step; discard the rest silently.

If a skill finds itself wanting to recite the loaded context back at the user, that's a signal the load was the wrong shape, not that the user needs the recital.

## Lookup, not resolve

Reads in this list are lookups. A missing plan (`rota plan show` exits 3), an empty `rota backlog ids` list, or a missing detail file is the answer, not a failure. Do not wrap these calls in `2>/dev/null` or fallbacks; handle the exit code or read `--json` `data`.

## What this reference does NOT cover

- **K+D query mechanics** — those live in `references/knowledge-consult.md`. This reference cites that one for the K+D portion; it does not redefine the query pattern.
- **`/rota-debug`, `/rota-refactor`, `/rota-review` context loads** — those consume only `references/knowledge-consult.md`, not the full protocol. Their inputs are different (a bug ID, a diff range, a feature branch), so they don't load backlog entries / plans / milestones the same way.
