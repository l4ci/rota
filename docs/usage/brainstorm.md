# Brainstorming a design

`/rota-brainstorm` fills the gap between [`/rota-capture`](capturing-work.md) (records what to build) and [`/rota-plan`](vision-and-plans.md) (decomposes how to build it). It negotiates *whether this is the right thing and what shape it should take* for a single backlog item. The artifact lands at `.rota/designs/<ID>.md` and feeds `/rota-plan` as soft input: read when present, never required.

## When to run it

- Right after capturing a `[Major]` feature or a `[P0]` bug, when its design is unclear.
- When two reasonable approaches need negotiation before you commit to one.
- When the item's TODO entry is one sentence but the implementation isn't obvious.
- When `/rota-capture` or `/rota-work` (no argument) nudges you toward it (the nudge fires on `[Major]` and `[P0]` items that don't yet have a design artifact).

Skip it when the item is `[Minor]`, `[Cosmetic]`, or a plain task with an obvious shape. Skip it when you already know what you want to build; go straight to [`/rota-plan`](vision-and-plans.md) or [`/rota-work`](running-work.md).

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

The skill resolves `F12`, reads its TODO entry, queries relevant `KNOWLEDGE.md` and `DECISIONS.md` topics, and asks 2-3 clarifying questions:

> What signals "resolved"? `## Completed` only, or also `ARCHIVE.md`? Should the 90-day threshold be a flag, a config key, or both?

Once context is anchored, the skill proposes three approaches:

1. **In-place TODO mutation.** `rota-archive` rewrites `BACKLOG.md` directly. Simple, but conflicts with parallel `/rota-work` sessions.
2. **Append-only journal.** Move resolved bullets into a dated section in `ARCHIVE.md`, leave `BACKLOG.md` `## Completed` empty. Survives merge conflicts at the cost of some recency info.
3. **Two-phase: mark + sweep.** First pass tags bullets with `archived:` frontmatter, second pass moves them on a separate command. More steps, but reversible.

You pick approach 2. The skill then drafts the design section by section (Goal, Design, Approaches considered, Open questions, Assumptions), each approved before moving on. When the artifact is complete, it lands at `.rota/designs/F12.md`.

## The artifact

`.rota/designs/F12.md` is a small markdown file with frontmatter and five sections:

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
- **Edit**: open targeted sections and revise them in place.
- **Replace**: start from scratch; the previous artifact is overwritten only after explicit confirm.

## What it does not do

- Project-level design stays with [`/rota-vision`](vision-and-plans.md): milestones, multi-feature arcs, vision rewrites.
- Code-touching feasibility experiments stay with [`/rota-spike`](spikes.md): a throwaway branch that proves a thing works before the design hardens.
- Implementation plan with task decomposition stays with [`/rota-plan`](vision-and-plans.md).
- The `/rota-capture` hand-off to [`/rota-work`](running-work.md) skips the brainstorm step; use it for items with an obvious shape.

## Autonomy interaction

Under `autonomy.level: "off"` (default), `/rota-capture` and `/rota-work` (no argument) print a one-line nudge for `[Major]` features and `[P0]` bugs without a design artifact. Under `"auto"`, the nudge auto-invokes `/rota-brainstorm` before routing to `/rota-plan`. See [Autonomy levels](autonomy.md) for the full chaining rules.
