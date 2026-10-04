---
name: rota-vision
description: Brainstorm a project's vision and break it into milestones — Socratic discovery, web research, deliberate challenge, then write MILESTONES.md and per-milestone detail files. Handles fresh vision and editing/extending an existing one. Use on "let's plan", "what's the bigger picture", "create a roadmap", "brainstorm milestones".
---

**Print the banner below verbatim before any other action — skip if dispatched as a subagent.** See `references/banner-preamble.md`.

```
════════════════════════════════════════════════════════════════════════
  🔭  rota-vision  ·  brainstorm milestones with research
  triggers: "vision", "brainstorm milestones"  ·  pairs: rota-work
════════════════════════════════════════════════════════════════════════
```

# rota-vision — Brainstorm Project Vision & Milestones

Multiple milestones can be active at once when they don't depend on each other.

`.rota/MILESTONES.md` opens with `# Milestones` as its H1, followed by a short vision paragraph as intro preamble, then `## Active milestones` and one short overview section per milestone. `.rota/milestones/MNN.md` holds the full plan for each milestone (goal, acceptance criteria, rationale, open risks, research findings, free-form notes).

## Step 1 — Mode

Determine the mode silently via `rota milestone list --json`: `data.milestones` empty → **Create mode** (build vision from scratch); non-empty → **Edit mode** (extend, refine, retire, re-prioritize). Don't announce — it shapes your questions, not the user's view.

**Initialize task list.** Follow the canonical pattern in `references/task-list-init.md` — load `TaskCreate(…)` via `ToolSearch select:TaskCreate,TaskUpdate` if needed, then create one task per phase below.

Phases:

1. *Discovery* — Socratic exploration of the vision and stakes (Steps 2–3)
2. *Web research* — relevant external context surfaced (Step 4)
3. *Challenge* — deliberate counter-position before committing (Step 5)
4. *Write MILESTONES.md* — top-level roadmap drafted or amended (Step 6)
5. *Per-milestone detail files* — `.rota/milestones/MNN.md` populated (Step 7)

## Step 2 — Load Context Silently

Apply the canonical pre-planning context-load protocol (`references/context-load-protocol.md`) — it lists the common reads (K+D queries, recent git history) and cites the K+D query mechanics. For this skill, the reads also include:

- `.rota/MILESTONES.md` (current vision paragraph and overviews)
- Every `.rota/milestones/M*.md` (full per-milestone plans — read whatever exists)
- `.rota/BACKLOG.md` (what's already on the backlog hints at the user's mental model)
- Glossary terms from `.rota/KNOWLEDGE.md` `## Glossary` via `rota glossary read <term>` — vision sessions are the highest-yield surface for canonical-term capture; consult so brainstorming uses existing terms, and treat user definitional signals (*"by X I mean…"*, *"let's call this X"*) as triggers for inline `rota glossary write` writes (also available as `/rota-learn --term <name>`)
- `README.md`, `package.json`, `Cargo.toml`, `pyproject.toml`, or whatever stack file exists at the root

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`): milestone plans are tracking-issue bodies, not files. Read them with `rota milestone list` and `rota milestone show <MNN>` (both modes); never read `.rota/milestones/*.md`. `.rota/MILESTONES.md` still holds the vision paragraph.

DECISIONS matches are committed boundaries that constrain what milestones can promise; surface any conflict before proposing milestones.

**Dispatch a context-bundle worker** rather than issuing the reads on the orchestrator. Per `references/subagent-dispatch.md`, this step is read-heavy (≥3 file reads — `.rota/MILESTONES.md`, every `.rota/milestones/M*.md`, `.rota/BACKLOG.md`, glossary query results from `.rota/KNOWLEDGE.md`'s `## Glossary` topic, plus the root stack file) and the orchestrator only needs synthesis to do Step 3, not the raw text.

Brief (haiku tier — this is mechanical aggregation):

- **Goal:** Return a compact snapshot of the project's vision state.
- **Inputs:** `.rota/MILESTONES.md`, `.rota/milestones/M*.md`, `.rota/BACKLOG.md`, `README.md` (or whichever stack file exists), plus the output of `rota knowledge query`, `rota decisions query`, and `rota glossary read` for vision-relevant topics and domain terms (the orchestrator selects topics from the user's framing).
- **Constraints:** Surface any DECISIONS conflict explicitly in the snapshot — committed boundaries that constrain milestone proposals must be visible to the orchestrator before Step 3.
- **Return shape:** `{vision-paragraph, existing-milestones[], gaps[], hard-boundaries[], context-terms[]}` — bullets, not paragraphs, ≤200 words total.
- **Word budget:** ≤200 words.

The orchestrator uses the snapshot to ground the Step 3 framing paragraph. Definitional signals from the user (*"by X I mean…"*) still trigger inline `rota glossary write` writes on the orchestrator (also reachable via `/rota-learn --term <name>`) — that's a write, which stays per the reference.

## Step 3 — Frame & Discover

`/rota-vision` is the project-scope half of the design-exploration family — see `references/design-exploration.md` for the shared spine (Socratic discovery, propose before disk write, iterate before commit, write via verb, user-review gate) and the per-axis divergences. Vision's discovery is batched (single `AskUserQuestion` call with 2–3 questions in Create mode); brainstorm's is one-per-round. Both honor the same plain-text fallback rule.

Open with one short paragraph (3–4 sentences max) summarizing what you see — the project's apparent shape, the existing milestones if any, the obvious gaps. This grounds the conversation; it's not a status report.

Then ask discovery questions via `AskUserQuestion`. Tailor them to the mode:

**Create mode** — single call with 2–3 questions:

- **Header `"Scope"`** — *"What kind of work is this?"*
  - New product / new major feature line
  - Strategic refactor or platform shift
  - Research / discovery project
  - Other (free text)
- **Header `"Audience"`** — *"Who's the consumer of the result?"* (single-select with options that match the project's apparent type, plus Other)
- **Header `"Constraint"`** — *"Hard time or scope constraint?"* — options like *"Ship something usable in 4 weeks"*, *"No constraint, optimize for quality"*, *"Hit a specific event/launch date"*, Other.

**Edit mode** — single question, single-select:

- **Header `"Action"`** — *"What do you want to do with the vision?"*
  - Add a new milestone (Recommended if vision feels incomplete)
  - Refine an existing milestone (goal/acceptance/risks)
  - Retire one or activate/deactivate
  - Re-prioritize the active set
  - Explore a new direction the project should consider

Plain-text fallback: ask the same question in prose; default to Recommended on ambiguity, naming it explicitly. See `references/ask-user-question-fallback.md`.

## Step 4 — Web Research

Before proposing milestones, ground the conversation in outside context. Use `WebSearch` (and `WebFetch` for promising results) to look up:

- Prior art for the project category — what others have built, how they structured it
- Common pitfalls in this space
- Architectural or product patterns worth borrowing
- Recent industry shifts that change the calculus

**Dispatch one research worker per angle** rather than running `WebSearch` calls on the orchestrator. Per `references/subagent-dispatch.md`, multi-angle research is fan-out work — angles are independent by definition, and each one produces its own block of `WebSearch` / `WebFetch` output that the orchestrator doesn't need to retain after synthesis.

The orchestrator decomposes the framing from Step 3 into 3–5 research angles (by judgment, not a fixed count) — typically: competitive landscape, technical feasibility, user-need patterns, adjacent prior art. Dispatch one sonnet worker per angle in a single tool-call batch.

Per-worker brief:

- **Goal:** Return actionable findings on `<angle>` relevant to the project's framing.
- **Inputs:** The framing summary from Step 3 (one paragraph), the angle name, and the project's apparent type.
- **Constraints:** Findings must be **actionable** — *"pitfall to avoid in M01"*, *"pattern worth borrowing"*, *"competitor's mistake"*. Generic observations get cut. Cite sources.
- **Return shape:** 3–5 findings, each: `{finding (one sentence), citation (URL + title), so-what (why it matters for milestone design)}`.
- **Word budget:** ≤200 words per worker.

After all workers return, the orchestrator merges to 3–5 concrete findings total (deduplicate across angles, keep the sharpest framing). Present inline with citations. If an angle yields nothing useful, say so and move on.

Present findings inline with citations. If a search yields nothing useful, say so and move on — don't pad with generic observations.

When you find a finding that contradicts the user's current framing, that's exactly what Step 5 needs. Hold onto it.

## Step 5 — Challenge

Push back on the framing. This is the highest-value step — a polite review wastes the cycle. Concrete tactics:

- **Scope check** — *"M02 has 12 acceptance criteria. That's months. What's the smaller version that ships in two weeks?"*
- **Risk frontloading** — *"M01 assumes auth is straightforward. Research suggests session storage is the bigger risk. Frontload it?"*
- **Overlap detection** — *"M02 and M03 touch the same code 60%. Are they really separate milestones, or one delivered in two phases?"*
- **Cut tradeoff** — *"What would you cut if you had to ship in half the time? That's probably what M01 should be."*
- **Dependency surfacing** — *"You said M03 is independent, but M03 needs M01's auth. Mark the dependency, or change M03's scope?"*
- **Assumption naming** — explicitly name implicit assumptions (*"this assumes single-tenant"*) and force the user to take a stance.
- **"Why this order"** — for each adjacent pair, ask why the earlier one comes first. If the answer is *"it just feels right"*, dig deeper.

If multiple challenge points need user input, batch them into one `AskUserQuestion` call (max 3 questions). Use `multiSelect: true` when the user is choosing which subset of trade-offs to take.

This step is interactive — expect to iterate with the user. Don't move to Step 6 until the framing has survived honest pushback.

## Step 6 — Propose Milestones

Output a clear plan as plain markdown (not yet committed to disk):

```
M01 — Auth foundation     [ready · no deps]
  Goal: OAuth + sessions for end users.
  Acceptance:
    - Google + GitHub OAuth login works
    - Sessions persist across browser restarts
    - Token refresh handles 401 transparently
  Rationale: Foundation for everything user-facing.
  Open risks: Session storage choice (DB vs Redis).

M02 — Multi-tenant         [blocked · depends M01]
  Goal: Org isolation for B2B customers.
  ...
```

Rules for the plan:

- **No hard cap on count.** Take what the vision needs — two is fine, fifteen is fine.
- **Ready vs blocked** is computed: `ready` = all prerequisites shipped or no deps; `blocked` = ≥1 prerequisite still planned/active.
- **Order by dependency layer, not chronology.** Make parallel-able milestones visible (*"M01 and M03 are both ready, no deps between them"*).
- **Every milestone has at least one open risk.** If you can't name one, it isn't well thought out yet.

## Step 7 — Iterate

The user redlines. Common edits:

- *"Combine M02 and M03"* — merge them in the plan, restate, ask for confirmation
- *"M01 should ship faster, cut acceptance #3"* — adjust, restate
- *"M04 actually depends on M02"* — update the dependency, recompute ready/blocked
- *"Add an M05 for X"* — extend the plan
- *"Retire M03, we're not doing that anymore"* — set `status: archived` (preserves the section and detail file as a record), or keep it with `status: shipped` if already shipped, or remove the section entirely if it was a mistake

Iterate until the user explicitly confirms. Don't skip confirmation — the writes in Step 8 mutate disk state.

## Step 8 — Write to Disk

Once confirmed, persist each milestone. Batch all writes, then refresh the index once at the end.

**New milestone:**

```bash
MID=$(rota milestone add --json --title "<title>" --summary "<one-line summary>" [--depends M01,M02] | jq -r .data.id)
```

The verb mints `MNN`, creates `.rota/milestones/MNN.md` with a stub plan, and appends an overview block to `.rota/MILESTONES.md`. Status starts as `planned`.

**Issue mode** (`backlog.backend: "issues"`): `rota milestone add` returns `MNN` and creates the native milestone `MNN — <title>` plus a tracking issue labelled `milestone-tracker` and `status:planned`; no stub file or overview block is written. Draft the full plan in a scratch file (frontmatter `id: <MNN>` required; `depends` updates the tracking issue's Depends field) and publish it with `rota milestone put <MNN> --body-file <scratch-file>|-` instead of the `Edit` steps below. Status follows the label: `rota milestone status <MNN> --to <status>` (`shipped`/`archived` close the tracking issue and native milestone; `planned`/`active` reopen). The vision paragraph stays in `MILESTONES.md`; `rota milestone index` regenerates only its Active list and the managed instructions block (`AGENTS.md`, else `CLAUDE.md`).

**Fill in the detail file.** After `rota milestone add` creates the stub, edit `.rota/milestones/MNN.md` with the full content — replace the placeholder sections (`Goal`, `Acceptance criteria`, `Rationale`, `Open risks`, `Research findings`, `Notes`) with what the brainstorm produced. Use the `Edit` tool, not `Write`, so the frontmatter stays intact.

**Existing milestone edits:** use `Edit` to update `.rota/milestones/MNN.md` directly. If you change the title or the dependencies, also update the `### MNN — Title` section in `.rota/MILESTONES.md` so the overview matches.

**Activate / deactivate / retire:** call `rota milestone status MNN --to <planned|active|shipped|archived>` once per milestone whose status changed. Multi-active is supported — independent milestones (no shared dependencies) can run simultaneously. Use `archived` to retire a milestone that's no longer being pursued — its section stays in `MILESTONES.md` as a record but it drops out of the "Active milestones" header (same exclusion model as `shipped`). Note: `shipped` deps satisfy dependent milestones; `archived` deps do not, so a milestone blocked by an archived prerequisite stays blocked until the prerequisite is reframed.

**Vision paragraph (Create mode only).** Replace the placeholder under the `# Milestones` H1 in `MILESTONES.md` — *"(no vision yet — run `/rota-vision` to brainstorm milestones)"* — with 2–4 sentences that frame the project's why. This preamble sits above `## Active milestones` and provides context for the milestone list. In Edit mode, leave the paragraph alone unless the brainstorm meaningfully changed the framing.

**Refresh the index — once, at the end:**

```bash
rota milestone index
```

This regenerates `## Active milestones` in `MILESTONES.md` and the managed `<!-- rota-vision-start -->` block in `CLAUDE.md`. It also heals any drift in the per-section `**Status:**` lines from frontmatter — frontmatter is the single source of truth.

## Step 9 — Report (with optional handoff)

One compact summary:

```
Vision updated.
- M01 — Auth foundation         [active · ready]
- M02 — Multi-tenant             [planned · blocked by M01]
- M03 — Public API               [active · ready]
- M04 — Admin dashboard          [planned · blocked by M01, M02]

Active: M01, M03. Run /rota-capture to start filling items, or /rota-work (no argument) to pick from the existing backlog.
```

If a freshly active milestone has no captured items yet, append a two-route offer instead of just printing the run hint:

> *"M01 has no items yet — seed it now? I can hand off to `/rota-capture` to file individual items, or to `/rota-plan` to write the first slice plan up front."*

Use `AskUserQuestion` with header `"Seed M01"` and three options:

- `"Capture items (Recommended)"` — file individual bugs/features/tasks tagged to M01
- `"Write first slice plan"` — write `M01-S01.md` as a durable plan before any items land
- `"Skip for now"` — leave M01 empty; the user will populate later

Plain-text fallback: ask once in prose; default to Recommended (Capture items) on ambiguity, naming it explicitly. See `references/ask-user-question-fallback.md`.

**Capture items** — invoke `/rota-capture` via the `Skill` tool:

```
(/rota-vision — capture seed items for M01)

Active milestone: M01 — Auth foundation.
Acceptance criteria:
- ...

Capture these as discrete items, tag each with Milestone: M01.
```

**Write first slice plan** — invoke `/rota-plan` via the `Skill` tool:

```
(/rota-vision — plan first slice for M01)

Active milestone: M01 — Auth foundation.
Acceptance criteria:
- ...

Open in slice mode for M01 so the key auto-mints (M01-S01). Decompose the first reachable acceptance criterion into tasks with verifiable outcomes.
```

**Skip for now** — print *"OK — run `/rota-capture` or `/rota-plan` when you're ready."* and exit.

Otherwise the run is done. Don't recap discovery, research, or the challenge round — those happened in the conversation.

## Key Principles

- **Challenge, don't transcribe.** A polite write-down of whatever the user says is failure. The point of `/rota-vision` is to surface assumptions, frontload risks, and force tradeoffs before milestones land on disk.
- **Research is grounding, not decoration.** If a finding doesn't change a milestone's shape, it doesn't belong in the conversation.
- **Dependencies are explicit.** Every blocked relationship is named. `ready` vs `blocked` is computed from disk, not vibes.
- **Multi-active is fine.** Independent milestones can run in parallel. Don't force a single-track ordering when the dependency graph allows more.
- **No hard milestone count.** Two is fine. Fifteen is fine. Take what the vision needs.
- **`MILESTONES.md` is the overview; `milestones/MNN.md` is the plan.** Don't bloat the overview with full plans, and don't scatter the overview across detail files.
- **Active list is generated, not edited.** `## Active milestones` is regenerated by `rota milestone index` from frontmatter — never hand-edit it.

## References

- [`references/ask-user-question-fallback.md`](references/ask-user-question-fallback.md) — Plain-text fallback shape for AskUserQuestion-less hosts.
- [`references/banner-preamble.md`](references/banner-preamble.md) — Banner-print rule shared by every skill.
- [`references/context-load-protocol.md`](references/context-load-protocol.md) — K+D context loading sequence shared by every cycle-starting skill.
- [`references/design-exploration.md`](references/design-exploration.md) — Shared spine (Socratic discovery, propose, iterate, write, user-review) and per-axis divergences with `/rota-brainstorm`.
