---
name: rota-plan
description: Write an implementation plan as a first-class artifact before execution — keyed by milestone and slice or item (M01-S01, M01-B07). Captures goal, approach, task decomposition with verifiable outcomes, open questions, and named assumptions. /rota-work consults the plan if present. Use when an item or slice is too big to one-shot, or when alignment matters before code lands.
---

# rota-plan — Implementation Plan as Artifact

Write a plan to disk that the user signs off on before `/rota-work` runs. The plan is keyed under a milestone and a slice or backlog item — `M01-S01` for a slice, `M01-B07` for a single backlog item that warrants its own plan. On the issue backend a plan is a note on the item's issue (a slice plan, `plan:SNN`, lives on the milestone's tracking issue); on the file backend it is `.rota/plans/<key>.md`.

`/rota-plan` runs in one of two modes:

- **Interactive (default)** — the user redlines via `AskUserQuestion` + free-text iteration. Used in off/auto autonomy.
- **`--auto-loop`** — invoked exclusively from `/rota-work` Step 4 when `autonomy.level == "loop"` and the dispatch site decides a plan is needed. Suppresses `AskUserQuestion` entirely; runs the auto-resolution pipeline (see `## Auto-loop mode` below); logs each fresh pick into `DECISIONS.md` as an `[Auto:Loop]` entry; surfaces unresolved open questions as `_(Unresolved — surfaced for review)_` placeholders in the written plan. Off and auto autonomy modes never invoke this — they always surface open questions through `AskUserQuestion`.

## Step 1 — Task list

Track these phases with the host's task tool if it has one.

Phases:

1. *Resolve target* — milestone-and-unit key extracted from args / cwd (Step 2)
2. *Load context* — backlog item, its detail, codebase greps gathered (Step 3)
3. *Propose* — goal, approach, task decomposition drafted for the user (Step 4)
4. *Iterate* — feedback rounds until alignment (Step 5)
5. *Write* — plan persisted via `rota plan` (Step 6)

## Step 2 — Resolve Target

The user's input may be:

- **A milestone ID** (`M01`) — slice mode; mint the next slice number
- **A backlog item ID** (`#42`; file backend `B07`, `F03`, `T11`) — item mode; the plan key is `<milestone>-<itemId>`
- **Free-form** (*"plan the auth foundation"*, *"for the OAuth work"*) — ask which milestone

For an item target, read it with `rota item field list --json <ID>` (file backend: its `BACKLOG.md` entry and overflow file `.rota/<bugs|features|tasks>/<id>.md`) and look for the `milestone` field. That's the parent. If the item lacks a milestone tag, ask the user to either:

- Tag the item under an active milestone — write the tag via `rota item field set <ID> --name milestone --value <MID>` (never hand-edit `.rota/BACKLOG.md` on the file backend; the verb is idempotent), then proceed
- Skip planning and capture with `/rota-capture` and accept the hand-off to `/rota-work`

When the item carries a `Repos:` field, capture that value as the plan's target sub-repo(s) so `/rota-work` can resolve dispatch from the plan alone. The plan key shape (`<milestone>-<itemId>`) does not change — repo is frontmatter, not key. Multi-repo items pass the full comma-list through (`--repos web,api`); the frontmatter key stays singular `repo:` and just carries the joined string. Slice and milestone targets do not carry a repo (umbrella-flat per M02 acceptance).

For a slice target, read `.rota/milestones/<MID>.md` for goal/acceptance/risks context (issue mode: `rota milestone show <MID>`).

If a plan with the same key already exists (`rota plan show <key>`), ask whether to view (`rota plan show <key>`), edit (skip to Step 4 with current content as the starting point), or replace (`rota plan rm <key>` first, then re-create).

## Step 3 — Load Context Silently

Apply the canonical pre-planning context-load protocol (`references/context-load-protocol.md`) — it lists the common reads (backlog item, plan, milestone, milestone-scoped items via `rota backlog ids --milestone <MID>`, K+D queries, recent git history) and cites the K+D query mechanics. For this skill, the reads also include:

- Existing plans for this milestone: `rota plan list --milestone <MID>`
- For item targets carrying a `Repos:` field: resolve it to an absolute sub-repo path via `.rota/repos.json` (`load_repos()`). Skipped for slice / milestone targets and when umbrella mode is off.
- For item targets (`#N` or `[BFT]\d{2,}`): `rota design show <ID>` to load any design artifact from `/rota-brainstorm`. Skipped for slice targets. When a design artifact exists, it carries the negotiated Goal/Design/Approaches-considered — Step 4's proposal mirrors the design's chosen approach rather than re-exploring.
- **Doc-home awareness (F76).** Note whether the target repo has a doc home — i.e. `<repo-root>/<docs.path>/` (default `<repo-root>/docs/`). In umbrella mode also peek at siblings: if `<repo>-docs` is registered in `.rota/repos.json`, the project's docs likely live there, not in `<repo>/docs/`. The deterministic post-write check `rota plan validate-docs` re-checks this at Step 6.5, but the proposal in Step 4 should already route doc deliverables correctly rather than leave a mismatch for the post-write check to catch.

DECISIONS matches are committed boundaries the plan must respect. If the plan would violate any, **redesign before writing**, or surface the conflict and ask the user whether to update the decision first.

**Issue these as parallel tool calls in a single response** — they're independent. Form a picture; don't dump it.

**Plan-author cautions.**

- **Grep before claims.** Validate the detail file's quantified claims (*"≥5 callers"*, *"9 helpers"*) with `grep` / `git grep` before drafting tasks. Detail files describe intent; the codebase is ground truth, and the gap between them is where stale plans get born.
- **Re-resolve skill folder paths at write time.** A plan that names skill folders by path (e.g. `rota-status/SKILL.md`) goes stale within days if a consolidation cycle renames or merges them. `ls -d rota-<name>/` per cited skill before serializing tasks; the plan is a hint, the filesystem is ground truth.

## Step 4 — Propose the Plan

**Under `--auto-loop`**, skip "Propose"/"Iterate" semantics entirely: run the auto-resolution pipeline below for every open question, build the final plan markdown directly, then go to Step 6. No AskUserQuestion call fires anywhere in the run. See `## Auto-loop mode` below for the pipeline.

Output the plan as plain markdown — not yet committed to disk. Required sections:

- **Goal** — one sentence, what shipping this means
- **Approach** — 3–6 sentences describing the design choice and why this over the alternatives
- **Tasks** — decomposition. Each task gets:
  - **Observable behavior** — what's true after this task ships (visible to a user, a test, or another developer)
  - **Files** — paths the orchestrator will touch or create
  - **Verify** — specific command or manual check that proves the task done
- **Open questions** — explicit unknowns that need decisions before or during execution
- **Assumptions** — implicit constraints made explicit (*"assumes single-tenant"*, *"assumes Postgres ≥14"*)

Rules for the plan:

- **Tasks fit one execution window.** A task too big to ship in one focused pass is two tasks.
- **Every task has a verify step.** No verify = the task isn't well-defined.
- **No half-implementations.** Each task results in real, runnable code — no stubs or placeholders.
- **Tasks are vertical slivers, not horizontal layers.** Each task crosses every layer it needs to be observable — UI + logic + data together for one feature path, not "all UI first, all logic second". The **Observable behavior** requirement and the **No half-implementations** rule already enforce this implicitly; calling it out by name keeps slice plans from drifting into the horizontal anti-pattern that AI agents fall into by default. For a hotel-reservation slice: "Reserve" button is one task end-to-end; "Cancel" is a separate task end-to-end; "email confirmation" is a third. Not "build all the UI in T1, then all the controllers in T2, then all the persistence in T3."
- **Rename + incoming-link sweep is ONE task, not two.** Every file linking to a renamed target must update; co-scheduling rename + sweep in the same wave races on the index. Group rename + all incoming-link updates into one task, derive the file list from `git grep -l "<old-name>"` (the plan's enumeration is a hint, grep is ground truth).
- **Doc-by-path deliverables must resolve to an existing doc home (F76).** Any task file path under a `docs/` segment (or the configured `docs.path`) must land in a doc home that exists in the target repo — `<repo-root>/<docs.path>/`. If the doc home is missing, do not plant the file there; surface as an Open question instead. In umbrella mode, a sibling sub-repo named `<repo>-docs` is the usual cross-repo doc home (e.g. `runlog` → `runlog-docs`); call it out by name in the question. The Step 6.5 post-write check enforces this deterministically; surfacing it here is cheaper than reworking the plan after `rota plan add`.
- **Name assumptions you'd otherwise leave implicit.** Forces the user to confirm or push back.
- **List open questions you'd resolve mid-flight.** If they should be answered before `/rota-work` runs, ask now.

## Step 5 — Iterate

**Skipped under `--auto-loop`** — the auto-resolution pipeline already produced the final plan. Proceed directly to Step 6.

The user redlines. Common edits:

- *"T2 and T3 should be merged"* — combine and restate
- *"Files for T1 are wrong, the auth lives in `auth/session.ts`"* — correct the plan
- *"Add T5 for the migration"* — extend
- *"Assumption #2 is wrong, we're multi-tenant from day one"* — replace; this likely changes Approach

Iterate until the user explicitly confirms.

**Loop semantics:**

- **Round budget — up to 5 iterations** of *propose → user redlines → re-propose*. After the 5th revision without an explicit confirmation, stop drafting further changes and surface *"We've been iterating for a while — is this plan close enough to ship, or should we step back and rescope?"*
- **What counts as explicit confirmation** — a short affirmative reply directed at the plan: *yes*, *ship it*, *go*, *looks good*, *approved*, *lgtm*, *confirmed*, *do it*, or equivalent. Silence and topic-shift do **not** count — when the user pivots to a different question or stays quiet, ask *"Confirm this plan? (yes / changes)"* once and wait for a direct answer before Step 6 fires.
- **Exit conditions** — (a) explicit confirmation → proceed to Step 6; (b) 5-round budget hit → halt drafting and surface the check-in above; (c) the user redirects scope so far that the original **Goal** no longer matches → restart from Step 4 with the new spec rather than retrofitting the existing plan.

## Step 6 — Write to Disk

```bash
# Slice mode — auto-mint slice number (umbrella-flat, no --repos):
KEY=$(rota plan add --json --milestone <MID> --slice --title "<title>" | jq -r .data.key)

# Item mode — explicit key; pass --repos for umbrella items:
KEY=$(rota plan add --json <MID>-<itemId> --title "<title>" | jq -r .data.key)
# or, if the item carries Repos: <name>
KEY=$(rota plan add --json <MID>-<itemId> --title "<title>" --repos <name> | jq -r .data.key)
# multi-repo items pass the full comma-list:
KEY=$(rota plan add --json <MID>-<itemId> --title "<title>" --repos web,api | jq -r .data.key)
```

Pass `--repos` only for item-mode targets that carry a `Repos:` value. Slice mode never sets `--repos`. Multi-repo items keep all names in one comma-separated `--repos` value; `rota plan add` validates each name against `.rota/repos.json` before writing the plan.

When a design exists for the plan's item, pass `--design <ID>` to `rota plan add`. The plan's frontmatter records `design: .rota/designs/<ID>.md` (issue backend, on a slice: `design: note:<ID>:design`) as a traceability pointer.

On the issue backend (`references/issue-mode.md`), `rota plan add` still creates the plan (`rota plan add --milestone <M> --slice --title "<title>"` mints `SNN`), but draft the confirmed sections in a scratch file and publish with `rota plan put <key> --body-file <scratch-file>|-` instead of `Edit` (slice keys look like `M07-S05`). List with `rota plan list [--milestone <M>]`, remove with `rota plan rm <key>`. Record plan-shaping answers with `rota item comment add <ID> --kind decision --body-file -`.

On the file backend the verb creates `.rota/plans/<key>.md` with frontmatter and stub sections. Use the `Edit` tool to fill in Goal, Approach, Tasks, Open questions, and Assumptions — replacing the placeholder sections with confirmed content. Keep the frontmatter intact.

## Step 6.5 — Validate Doc-by-Path Deliverables (F76)

```bash
rota plan validate-docs <key>
```

The verb scans `Files:` bullets under the Tasks section, classifies each path as doc-by-path when it contains a `docs/` (or configured `docs.path`) segment, and warns when the corresponding doc home does not exist in the target repo. Multi-repo plans validate against every name in `repo:`; slice / milestone plans (no `repo:` frontmatter) resolve against cwd. Sibling `<repo>-docs` sub-repos are surfaced as suggested alternatives.

Read `--json` `data.valid` and `data.mismatches` (`path`, `targetRepo`, `issue`, `suggestion`); the verb exits 0 either way.

- **`valid: true`.** Continue silently to Step 7.
- **`valid: false`.** For each mismatch, append a question to the plan's *Open questions* section via `Edit` — keep the path / target-repo / suggestion shape so the user sees the concrete mismatch:

  ```
  - **Doc home mismatch.** `docs/api/auth.md` plans to land in `<repo>/docs/`, but that directory does not exist. Sibling `<repo>-docs` is registered — should the deliverable move there, or should this plan also create `<repo>/docs/`?
  ```

  Then proceed to Step 7. The validator is advisory — false positives (e.g. a task earlier in the plan creates the doc home before the doc deliverable lands) are the user's call to dismiss in review.

**Under `--auto-loop`**, warnings ride the standard placeholder pipeline (`## Auto-loop mode` → step 3): the open question lands in the plan literally with `_(Unresolved — surfaced for review)_` appended, so the post-cycle review catches it.

## Step 7 — Report

Compact summary:

```
Plan written: M01-B07 — Auth foundation
  Repo: web                              # omit when no repo tag
  Tasks: 4
  Open questions: 1
  Status: planned

Next: /rota-work M01-B07 to execute, or /rota-work --preview M01-B07 to peek before running.
```

Include `Repo: <names>` only when the plan was tagged with a sub-repo (item targets with `Repos:` under umbrella mode). Render multi-repo plans with the joined list — e.g. `Repo: web, api`. Slice and milestone plans omit the line entirely.

If `/rota-work` is the natural next step and the user is ready, offer it as a one-line prompt rather than just printing the hint.

## Auto-loop mode

Activated by the `--auto-loop` flag. Invoked exclusively by `/rota-work` Step 4 in loop mode when no plan exists for a Major + Milestone-tagged item — see `/rota-work`'s Step 4 dispatch directive for the trigger conditions and the inline `Skill`-tool dispatch language. This section describes the run shape once the flag is set; the dispatch decision lives at `/rota-work`'s call site (per the authoring-conventions rule "Imperative rules in autonomy-aware steps must live inline at every dispatch point" convention).

**Orchestrator-model contract.** `--auto-loop` makes design picks autonomously (no `AskUserQuestion`), so it depends on orchestrator-grade design judgment. The contract: this skill is invoked via the `Skill` tool from `/rota-work` Step 4, which loads it inline in `/rota-work`'s session. Since `/rota-work` runs under `models.orchestrator` (per `.rota/config.json`, default `opus`), `--auto-loop` inherits that model. If a future change moves the dispatch to the `Agent` tool, the call site MUST explicitly pass `model: orchestrator` (resolved from `.rota/config.json`) — running `--auto-loop` under the worker model would push design picks onto an execution-tuned model and degrade plan quality. The interactive (default) mode has no such constraint; it can run under any model since the user redlines via `AskUserQuestion`.

### Pipeline

For each open question the orchestrator would normally surface to the user, run three steps in order:

1. **Local-first.** Grep `DECISIONS.md` / `MILESTONES.md` / `KNOWLEDGE.md` via `rota decisions query` / `rota knowledge query` on the question's topic keywords. If a matching commitment exists, the answer is "honor the existing commitment" — do **not** log a new `[Auto:Loop]` entry; the existing commitment IS the record.
2. **Bounded web (opt-in).** If unmatched AND the question references an external library, API, or protocol (anything outside the F14 rota surface scan: `/rota-(\w+)`, `rota <verb>` calls, `.rota/*` artifacts), AND `loop.webResearch == true` in `.rota/config.json` (default `false`), call `WebSearch` with a budget of **2 queries per question, 6 queries per plan**. Block on results; no async fetch.
3. **Placeholder fallback.** If still unresolved, retain the question literally in the written plan with `_(Unresolved — surfaced for review)_` after the question text. The auto-write proceeds — never stop the loop.

### Logging

Each fresh pick from steps 1 (when no existing commitment matched and you made a new pick) and 2 produces an `[Auto:Loop]` entry via:

```bash
rota decisions auto-log --topic "<topic>" --title "<rule-title>" --why "<why-text>" --plan-key "<plan-key>" --date "$(date +%Y-%m-%d)"
```

The entry follows the standard `DECISIONS.md` template, but **only the rule and `*Why.*` are auto-filled**; `**Forbids.**` and `**Permits.**` stay as `_(Unresolved — user must articulate)_` placeholders the user fills at session end (per the 2026-05-08 source-prefill rule that destination-specific fields stay as placeholders the skill blocks on). A footer comment encodes provenance: `<!-- [Auto:Loop] <plan-key> <date> — review and articulate Forbids/Permits -->`. The verb is idempotent on `(topic, rule-title)` — re-running the same plan key writes each entry exactly once.

After all questions are resolved, write the plan using the same `rota plan add` + `Edit` flow as Step 6, with `--auto-loop` on `rota plan add`: the verb writes the `auto: true` frontmatter key that marks the plan as auto-written, and refuses the flag (exit 2) outside loop mode. The plan's "Open questions" section lists every step-3 placeholder verbatim; "Resolved open questions" lists every step-1/2 outcome with a brief rationale.

### Surfacing

`/rota-plan --auto-loop` itself does not surface auto-decisions to the user — surfacing fires only on terminal paths (`/rota-work` empty-backlog branch, `/rota-work` guard-fail branch, `/rota-pause`) via `rota decisions auto-since`. The user sees the running summary at session end, articulates `Forbids/Permits` in `DECISIONS.md`, and removes the `<!-- [Auto:Loop] -->` footer (signaling the entry is now a normal decision).

## Key Principles

- **Plans are committed alignment, not rough notes.** If the user wouldn't sign off, it isn't ready to write.
- **Verify is non-negotiable.** Every task has a check that proves it done.
- **Open questions beat hidden assumptions.** Surface what you don't know.
- **Tasks fit one execution.** If they don't, split.
- **The plan key relates to a milestone.** Items without a milestone get tagged before planning.

## References

- [`references/context-load-protocol.md`](references/context-load-protocol.md) — K+D context loading sequence shared by every cycle-starting skill.
