# Brainstorming a design

`/rota-brainstorm` fills the gap between [`/rota-capture`](capturing-work.md) (records what to build) and [`/rota-plan`](vision-and-plans.md) (decomposes how to build it). It negotiates *whether this is the right thing and what shape it should take* for a single backlog item. The artifact is stored as the item's design: a note on its issue on the [issue backend](issue-backend.md), `.rota/designs/<ID>.md` on the file backend. It feeds `/rota-plan` as soft input: read when present, never required.

## 🎯 When to run it

- Right after capturing a `[Major]` feature or a `[P0]` bug, when its design is unclear.
- When two reasonable approaches need negotiation before you commit to one.
- When the item's TODO entry is one sentence but the implementation isn't obvious.
- When `/rota-capture` or `/rota-work` (no argument) nudges you toward it (`/rota-capture` nudges on a new `[Major]` feature or `[P0]` bug; `/rota-work` on one that doesn't yet have a design).

> [!TIP]
> Skip it when the item is `[Minor]`, `[Cosmetic]`, or a plain task with an obvious shape. Skip it when you already know what you want to build; go straight to [`/rota-plan`](vision-and-plans.md) or [`/rota-work`](running-work.md).

## One example end-to-end

You capture an idea:

```
/rota-capture "an /rota-archive command that ages out resolved items older than 90 days"
# → [F12] [Major] /rota-archive command for old resolved items.
```

`/rota-capture` flags it as `[Major]` and prints a nudge:

> `[F12]` is a `[Major]` feature with no design artifact. Run `/rota-brainstorm F12` before `/rota-plan F12` to negotiate shape and tradeoffs.

You take the nudge:

```
/rota-brainstorm F12
```

The skill resolves `F12`, reads its TODO entry, and queries relevant `KNOWLEDGE.md` and `DECISIONS.md` topics. It asks nothing unless something is truly ambiguous or conflicts with a decision, and then it asks the one question that unblocks it:

> What signals "resolved"? `## Completed` only, or also `ARCHIVE.md`?

Then the skill drafts the whole design in one pass, including three approaches:

1. **In-place TODO mutation.** `rota-archive` rewrites `BACKLOG.md` directly. Simple, but conflicts with parallel `/rota-work` sessions.
2. **Append-only journal.** Move resolved bullets into a dated section in `ARCHIVE.md`, leave `BACKLOG.md` `## Completed` empty. Survives merge conflicts at the cost of some recency info.
3. **Two-phase: mark + sweep.** First pass tags bullets with `archived:` frontmatter, second pass moves them on a separate command. More steps, but reversible.

The draft covers Goal, Design, Approaches considered, Acceptance, Open questions and Assumptions, with your pick marked. You approve it once (or ask for revisions). It is then stored, here at `.rota/designs/F12.md`.

## The artifact

`.rota/designs/F12.md` is a small markdown file with frontmatter and these sections (Acceptance is skipped when the issue body already has one):

```markdown
---
id: F12
title: /rota-archive command for old resolved items
status: draft
created: 2026-05-12
---

# F12: /rota-archive command for old resolved items

## Goal

Ship a `/rota-archive` command that ages out `## Completed` items older than 90 days into `ARCHIVE.md` without disturbing active backlog state.

## Design

Append-only journal pattern. ...

## Approaches considered

1. **In-place mutation**: ...
2. **Append-only journal (chosen)**: ...
3. **Two-phase mark + sweep**: ...

## Acceptance

- [ ] Items resolved more than 90 days ago move to `ARCHIVE.md`.

## Open questions

- Should the 90-day window be configurable per project?

## Assumptions

- `ARCHIVE.md` exists and is append-safe.
```

## How it feeds `/rota-plan`

When you next run `/rota-plan M01-F12`, the planner reads `.rota/designs/F12.md` if present and reflects its chosen approach in the plan's `## Approach` section. The plan's frontmatter carries a `design:` pointer for traceability:

```yaml
key: M01-F12
milestone: M01
unit: F12
unitKind: item
design: .rota/designs/F12.md
title: /rota-archive command for old resolved items
status: planned
```

The design is soft input: `/rota-plan` doesn't require it, and a plan can override a design's choice if facts changed since the brainstorm.

## Re-running on an existing design

If `.rota/designs/<ID>.md` already exists, `/rota-brainstorm` asks how to proceed:

- **View**: print the artifact and exit.
- **Edit**: load it as the starting draft and revise it.
- **Replace**: delete it and start from scratch.

## 🚫 What it does not do

- Project-level design stays with [`/rota-vision`](vision-and-plans.md): milestones, multi-feature arcs, vision rewrites.
- Code-touching feasibility experiments stay with [`/rota-spike`](spikes.md): a throwaway branch that proves a thing works before the design hardens.
- Implementation plan with task decomposition stays with [`/rota-plan`](vision-and-plans.md).
- Items with an obvious shape can skip the brainstorm: capture them and go straight to [`/rota-work`](running-work.md).

## Autonomy interaction

`/rota-capture` and `/rota-work` (no argument) only print the nudge, at every [autonomy level](autonomy.md). `/rota-capture` never invokes `/rota-brainstorm`.
