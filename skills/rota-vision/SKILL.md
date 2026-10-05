---
name: rota-vision
description: Use on "let's plan", "what's the bigger picture", "create a roadmap", "brainstorm milestones".
---

# rota-vision — Project Vision & Milestones

Milestones are optional structure. This skill is a thin layer over them: on the issue backend a milestone is a native tracker milestone `MNN — <title>` plus a tracking issue (`references/issue-mode.md`), and the list you write mirrors that. `.rota/MILESTONES.md` holds a short vision paragraph and the Active list; each milestone's plan is its tracking-issue body (file backend: `.rota/milestones/MNN.md`). Nothing else in rota requires a milestone.

## Step 1 — Mode

`rota milestone list --json`: `data.milestones` empty means **Create** (build from scratch); non-empty means **Edit** (extend, refine, retire, re-prioritize). Don't announce it.

## Step 2 — Load context

Follow `references/context-load-protocol.md` (parallel, silent), plus `.rota/MILESTONES.md`, `rota milestone show <MNN>` for each existing milestone, the root stack file (`README.md`, `package.json`, …) and `rota glossary read <term>` for the user's terms. Treat definitional signals (*"by X I mean…"*) as triggers for `rota glossary write`. Issue mode: never read `.rota/milestones/*.md`. DECISIONS matches constrain what milestones can promise; surface conflicts before proposing.

## Step 3 — Frame and discover

Open with 3-4 sentences on what you see: the project's shape, existing milestones, obvious gaps. Then one batched `AskUserQuestion` (see `references/design-exploration.md`):

- **Create** — 2-3 questions: *Scope* (new product / strategic refactor / research / other), *Audience*, *Constraint* (time or scope limit).
- **Edit** — one question, *Action*: add a milestone (Recommended if the vision feels incomplete), refine one, retire/activate, re-prioritize, or explore a new direction.

Default to the Recommended option on ambiguity and name it.

## Step 4 — Research (opt-in)

Skip unless the user asks for it or the framing leans on outside context (prior art, pitfalls, a space you don't know). When it runs, ask first: *"Run web research on this? (yes / skip)"*. On yes, decompose the framing into 3-5 independent angles, run one `standard` subagent per angle in a single batch (`WebSearch` / `WebFetch`), and merge to 3-5 actionable, cited findings (*"pitfall to avoid in M01"*, *"pattern worth borrowing"*). Say so and move on if nothing useful comes back. Hold onto any finding that contradicts the user's framing for Step 5.

## Step 5 — Challenge

Push back on the framing. This is the highest-value step; a polite review wastes the cycle. Run the rounds, recommended answers, code-first lookups, edge-case scenarios, term handling and stop condition of `references/grilling.md`, with the tactics below as the lens for each question:

- **Scope check** — *"M02 has 12 acceptance criteria. What's the smaller version that ships in two weeks?"*
- **Risk frontloading** — *"M01 assumes auth is straightforward; session storage is the bigger risk. Frontload it?"*
- **Overlap detection** — *"M02 and M03 touch the same code 60%. One milestone in two phases?"*
- **Cut tradeoff** — *"What would you cut to ship in half the time? That's probably M01."*
- **Dependency surfacing** — *"You said M03 is independent, but it needs M01's auth. Mark it, or change M03's scope?"*
- **Assumption naming** — name implicit assumptions (*"this assumes single-tenant"*) and force a stance.
- **Why this order** — for each adjacent pair, why the earlier comes first.

Batch each round into one `AskUserQuestion` (max 3 questions; `multiSelect: true` for choosing trade-offs). Move on when the frontier is empty and the framing has survived honest pushback.

## Step 6 — Propose, once

Show the milestone list as plain markdown, not yet saved, one block per milestone:

```
### M01 — <title>   [ready · no deps]
**Goal:** <one sentence>
**Acceptance:**
- <checkable bullet>
- <checkable bullet>
**Rationale:** <why this one, why now>
**Open risks:** <at least one; if you can't name one it isn't thought through>
```

The tag is `[ready · no deps]` or `[blocked · depends M01]`. No cap on count. Order by dependency layer and make parallel-able milestones visible. Apply the user's redlines (merge, cut, retire, add, re-order) and ask for one explicit confirmation before writing; silence is not confirmation.

## Step 7 — Write

Batch the writes, then refresh the index once.

```bash
MID=$(rota milestone add --json --title "<title>" --summary "<one line>" [--depends M01,M02] | jq -r .data.id)
```

Issue mode: this creates the native milestone and tracking issue (status `planned`). Draft the full plan (frontmatter `id: <MNN>` required) in a scratch file and publish with `rota milestone put <MNN> --body-file <file>|-`. File mode: it mints `MNN`, a stub `.rota/milestones/MNN.md` and an overview block; `Edit` the stub's sections (keep the frontmatter).

- **Status:** `rota milestone status <MNN> --to <planned|active|shipped|archived>` per changed milestone. Several can be active when independent. `archived` retires one without deleting it, but an archived dependency does not unblock dependents; `shipped` does.
- **Vision paragraph** (Create only): rewrite with `rota milestone overview --body-file <file>|-` (file mode: replace the placeholder under `# Milestones`), 2-4 sentences on the why. In Edit mode leave it unless the framing changed.
- **Index:** `rota milestone index` regenerates the Active list and the managed instructions block. Never hand-edit the Active list.

## Step 8 — Report

```
Vision updated.
- M01 — Auth foundation   [active · ready]
- M02 — Multi-tenant      [planned · blocked by M01]
Active: M01. Run /rota-capture to fill items, or /rota-work to pick from the backlog.
```

If a newly active milestone has no items, offer (default Capture): `/rota-capture` to file items, `/rota-plan` in slice mode for a first slice plan, or skip. Don't recap discovery, research or the challenge.

## Key principles

- **Challenge, don't transcribe.** Surface assumptions, frontload risks, force tradeoffs before anything lands.
- **Research is grounding, not decoration.** A finding that doesn't change a milestone's shape doesn't belong.
- **Dependencies are explicit.** `ready` vs `blocked` is computed, not vibes.
- **Thin by design.** The tracker's milestone is the source of truth; don't duplicate it in prose.

## References

- [`references/context-load-protocol.md`](references/context-load-protocol.md) — shared parallel context load.
- [`references/knowledge-consult.md`](references/knowledge-consult.md) — the K+D query pattern the load uses.
- [`references/grilling.md`](references/grilling.md) — the Step 5 challenge rounds.
- [`references/subagent-dispatch.md`](references/subagent-dispatch.md) — the `light` subagent that grilling sends for broad reads.
- [`references/design-exploration.md`](references/design-exploration.md) — shared spine with `/rota-brainstorm`.
