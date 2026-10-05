---
name: rota-brainstorm
description: Per-item design before /rota-plan — one draft with 2-3 approaches and an Acceptance section, one approval, stored as the item's design (a note on its issue, or .rota/designs/<ID>.md on the file backend). Use when a Major feature or P0 bug needs design negotiation before implementation planning.
---

# rota-brainstorm — Per-item Design

Fills the gap between `/rota-capture` (what to build) and `/rota-plan` (how). Scope is one backlog item (`#N`; file backend `B07`/`F03`/`T05`). Project-level exploration is `/rota-vision`. The design (`rota design show <ID>`) is soft input to `/rota-plan`, never required. Milestones play no part: the item's tag, if any, is just context.

## Step 1 — Resolve target

The target is `#N` / a bare number (issue backend) or `[BFT]\d{2,}` (file backend). Reject milestone and slice IDs: *"/rota-brainstorm operates on a single backlog item. For project-level exploration use /rota-vision; for slice planning use /rota-plan."* `rota item field get <ID> --name title` exits 3 for an unknown item: refuse and point at `/rota-capture`.

If a design exists, ask once (View / Edit / Replace, default View): View runs `rota design show <ID>` and exits; Edit loads it as the starting draft; Replace runs `rota design rm <ID>` first.

## Step 2 — Load context

Follow `references/context-load-protocol.md` (parallel, silent), plus `rota item show <ID>` for state and comments (`decision` comments bind; `references/issue-mode.md`, "Resuming an item") or the file backend's detail file, and `rota glossary read <term>` for domain terms. DECISIONS matches are hard boundaries: on a conflict, surface it before drafting.

**Acceptance present in the issue body?** Skip ahead. Read its `## Acceptance` checklist, treat it as settled, and go straight to Step 3 for the approaches only. If it is absent, you draft it in Step 3.

## Step 3 — One draft

Ask nothing unless there is real ambiguity or a decision conflict you cannot resolve from the item, its thread and the code; then ask the one question that unblocks you (`AskUserQuestion`, ≤ 4 options). If a question needs code-touching evidence (feasibility, library support, performance), say: *"This warrants a spike. Run `/rota-spike <name>` first, then re-invoke `/rota-brainstorm <ID>`."* Don't guess.

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
- [`references/context-load-protocol.md`](references/context-load-protocol.md) — shared parallel context load.
