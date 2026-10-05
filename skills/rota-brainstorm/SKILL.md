---
name: rota-brainstorm
description: Use when a Major feature or P0 bug needs design negotiation before implementation planning, or on "brainstorm 42", "design this item".
---

# rota-brainstorm — Per-item Design

Fills the gap between `/rota-capture` (what to build) and `/rota-plan` (how). Scope is one backlog item (`#N`; file backend `B07`/`F03`/`T05`). Project-level exploration is `/rota-vision`. The design (`rota design show <ID>`) is soft input to `/rota-plan`, never required. Milestones play no part: the item's tag, if any, is just context.

## Step 1 — Resolve target

The target is `#N` / a bare number (issue backend) or `[BFT]\d{2,}` (file backend), plus an optional `--grill` flag (Step 3). Reject milestone and slice IDs: *"/rota-brainstorm operates on a single backlog item. For project-level exploration use /rota-vision; for slice planning use /rota-plan."* `rota item field get <ID> --name title` exits 3 for an unknown item: refuse and point at `/rota-capture`.

If a design exists, ask once (View / Edit / Replace, default View): View runs `rota design show <ID>` and exits; Edit loads it as the starting draft; Replace runs `rota design rm <ID>` first.

## Step 2 — Load context

Follow `references/context-load-protocol.md` (parallel, silent), plus `rota item show <ID>` for state and comments (`decision` comments bind; `references/issue-mode.md`, "Resuming an item") or the file backend's detail file, and `rota glossary read <term>` for domain terms. DECISIONS matches are hard boundaries: on a conflict, surface it before drafting.

**Acceptance present in the issue body?** Skip ahead. Read its `## Acceptance` checklist, treat it as settled, and go straight to Step 3 for the approaches only. If it is absent, you draft it in Step 3.

## Step 3 — One draft

**Grill first, once.** Run one pass of `references/grilling.md` before drafting when the item is a Major feature or P0 bug (`rota item show <ID>`), or the invocation has `--grill`. Skip it when the issue body already has `## Acceptance` (Step 2): that design is settled, and `--grill` does not override it. Skip it for other items too: ask nothing unless there is real ambiguity or a decision conflict you cannot resolve from the item, its thread and the code; then ask the one question that unblocks you (`AskUserQuestion`, ≤ 4 options). Answers that changed direction are recorded in Step 5. If a question needs code-touching evidence (feasibility, library support, performance), say: *"This warrants a spike. Run `/rota-spike <name>` first, then re-invoke `/rota-brainstorm <ID>`."* Don't guess.

Write the whole design as one draft, plain markdown, not yet saved:

- **Goal** — one sentence on what shipping this means
- **Design** — 3-8 sentences: chosen shape, moving parts, where they live
- **Approaches considered** — 2-3 candidates, each with shape, pros, cons, the deciding factor; mark the pick. A truly simple item may have one approach and a two-line design; that is a valid output.
- **Acceptance** — a `- [ ]` checklist, observable outcomes (skip when the issue body already has one)
- **Open questions** — what must be answered before or during `/rota-plan`; mark spike candidates
- **Assumptions** — implicit constraints made explicit

Self-review before showing it: no placeholders, no contradictions (the pick matches Design; Open questions don't conflict with Assumptions), nothing leaking into sibling items.

## Step 4 — One approval

Show the draft and ask once: **Approve and hand off to `/rota-plan`**, **Approve, no plan yet**, or **Revise** (apply the user's redlines, show again). Silence is not approval.

## Step 5 — Store

```bash
rota design add <ID> --title "<title>"                     # mint the stub
rota design put <ID> --body-file <scratch-file>            # issue backend: publish the approved draft
```

On the issue backend the design is a note on the item's issue (`references/issue-mode.md`). On the file backend it is `.rota/designs/<ID>.md`: `Edit` the stub's sections, keep the frontmatter. Post each answer that changed direction with `rota item comment add <ID> --kind decision --body-file -`.

Report two lines (artifact, approaches, open questions). On hand-off say *"Run `/rota-plan <ID>` next."*

## Key principles

- **No noise.** Don't narrate loads or recap; don't pad the artifact.
- **One draft, one approval.** The artifact is the negotiated outcome, not a transcript.
- **Soft input to `/rota-plan`.** Plans without a design stay valid.
- **Single item.** Project scope is `/rota-vision`; slice scope is `/rota-plan`.

## References

- [`references/design-exploration.md`](references/design-exploration.md) — shared spine with `/rota-vision`.
- [`references/grilling.md`](references/grilling.md) — the grilling pass (Major / P0 / `--grill`).
- [`references/subagent-dispatch.md`](references/subagent-dispatch.md) — the `light` subagent that grilling sends for broad reads.
- [`references/context-load-protocol.md`](references/context-load-protocol.md) — shared parallel context load.
- [`references/knowledge-consult.md`](references/knowledge-consult.md) — the K+D query pattern the load uses.
