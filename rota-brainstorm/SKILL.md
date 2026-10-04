---
name: rota-brainstorm
description: Per-item design exploration before /rota-plan — Socratic discovery, 2-3 approaches with tradeoffs, sectioned design with per-section approval, writes the item's design (a note on its issue, or .rota/designs/<ID>.md on the file backend), hands off to /rota-plan. Use when a Major feature or P0 bug needs design negotiation before implementation planning.
---

# rota-brainstorm — Per-item Design Exploration

`/rota-brainstorm` fills the gap between `/rota-capture` (records what to build) and `/rota-plan` (decomposes how to build it) by negotiating *whether this is the right thing and what its shape should be*. Scope is a single backlog item (`#N`, or `[B07]`/`[F03]`/`[T05]` on the file backend); project-level exploration stays with `/rota-vision`. The artifact is the item's design (`rota design show <ID>`) and feeds `/rota-plan` as soft input — never required.

## Step 1 — Setup

Track these phases with the host's task tool if it has one.

Phases:

1. *Resolve target* — item ID parsed, existence verified, re-run mode picked (Step 2)
2. *Load context* — backlog item, detail, K+D+C queries gathered in parallel (Step 3)
3. *Discover* — Socratic clarifying rounds, capped at 5 (Step 4)
4. *Propose approaches* — 2-3 candidates with tradeoffs, user picks one (Step 5)
5. *Section drafts* — Goal → Design → Approaches → Open questions → Assumptions with per-section approval (Step 6)
6. *Write* — design persisted via `rota design` (Step 7)
7. *Self-review* — placeholder/contradiction/scope/ambiguity scan (Step 8)
8. *User review* — final approval gate (Step 9)

## Step 2 — Resolve Target

Parse the item ID from the invocation. It must be `#N` or a bare number (issue backend), or match `[BFT]\d{2,}` (file backend). Reject milestone IDs (`M01`) and slice IDs (`S01`) with: *"Error: /rota-brainstorm operates on a single backlog item. For project-level exploration use /rota-vision; for slice planning use /rota-plan."*

Verify the item exists in the backlog:

```bash
rota item field get <ID> --name title
```

Exit 3 means the ID is not in the backlog. Refuse with: *"Error: <ID> not found in the backlog. Run /rota-capture first to add it."*

If a design already exists, ask via `AskUserQuestion` (single-select, 3 options):

- **View** — print the existing design and exit
- **Edit** — enter brainstorm with existing design loaded as starting context
- **Replace** — discard the existing design and re-run discovery from scratch

Default: opt-in-off / cancel (replace is destructive). Routing:

- **View** → invoke `rota design show <ID>` and exit 0.
- **Edit** → load the existing design content; the first Step 4 clarifying question is *"What would you change about the existing design?"*
- **Replace** → run `rota design rm <ID>` then proceed to Step 3.

## Step 3 — Load Context Silently

Pull the picture in parallel — these reads are independent and latency-bound:

- `rota item field list --json <ID>` — `data.fields` with `title`, `milestone`, `related`, `detail`, `repos`, `subsystem`, `since` (single corpus load; avoids re-reading the backlog per field)
- Issue backend: `rota item show <ID>` for state and comments (`decision` comments are binding; `references/issue-mode.md`, "Resuming an item"). File backend: the detail file `.rota/bugs/<ID>.md`, `.rota/features/<ID>.md`, or `.rota/tasks/<ID>.md` (read whichever exists)
- `rota knowledge query <topic>` for topics inferred from the item and its detail
- `rota decisions query <topic>` for the same topics — committed boundaries the design must respect
- `rota glossary read <term>` for any domain terms the item references

**Issue all of these as parallel tool calls in a single response.** Don't narrate the loading. Form a picture; carry findings forward into Steps 4 and 5.

DECISIONS matches are hard boundaries. If the brainstorm would violate any, surface the conflict before proposing approaches and ask whether to update the decision first.

## Step 4 — Frame & Discover

Socratic clarifying questions via `AskUserQuestion`, one per round, multi-choice preferred (≤ 4 options per the picker cap). **Cap: 5 clarifying rounds**; after the fifth, switch to plain-text prose. Section gates (Step 6) and the final review (Step 9) are check-ins, not exploratory questions, and do **not** count against the budget.

`/rota-vision` is the project-scope sibling and uses batched discovery — see `references/design-exploration.md` for the family spine and divergence rationale.

Good clarifying-question shapes for an item:

- **Scope** — *"Does this need to handle umbrella sub-repos in V1, or is single-repo fine?"*
- **Interaction** — *"Should this run automatically after /rota-capture, or only on explicit invocation?"*
- **Gotcha boundaries** — *"What existing skill must this NOT touch?"*
- **Failure modes** — *"What happens when <key precondition> is false?"*

**Spike handoff.** When a clarifying question or candidate approach needs code-touching evidence to answer (feasibility, library support, performance characteristics), surface:

> *"This warrants a spike. Run `/rota-spike <name>` first to gather evidence, then re-invoke `/rota-brainstorm <ID>` to finish the design."*

Don't guess at the answer to keep the brainstorm moving.

## Step 5 — Propose 2-3 Approaches

Present 2 or 3 candidate approaches inline as plain markdown — not yet committed to disk. Each approach gets:

- **Shape** — one paragraph naming the moving parts and where they live
- **Pros** — 2-4 bullets on what this approach wins
- **Cons** — 2-4 bullets on what it costs
- **Why this might or might not be the right answer** — one sentence on the deciding factor

Frame each candidate around the item's detail file and the K+D+C findings from Step 3 — *"approach A reuses the helper pattern flagged in KNOWLEDGE"*, *"approach B violates the DECISION on X"*. Then ask the user to pick via `AskUserQuestion` (single-select, ≤ 4 options: one per approach plus *"Ask more questions first"* as an escape back to Step 4). Silence means more discussion, not a pick.

The picked approach is the design. Carry it into Step 6.

## Step 6 — Sectioned Design with Per-Section Approval

Draft the design one section at a time in this order: Goal → Design → Approaches considered → Open questions → Assumptions. See `references/design-exploration.md` for the shared approval shape (yes / changes / approve all remaining).

Section content rules:

- **Goal** — one sentence on what shipping this design means
- **Design** — 3-8 sentences on the chosen shape, moving parts, where they live
- **Approaches considered** — the 2-3 candidates from Step 5 with Pros/Cons/Why summarized
- **Open questions** — questions that need answering before or during `/rota-plan`; mark spike candidates explicitly
- **Assumptions** — implicit constraints made explicit (*"assumes single-repo"*, *"assumes K+D queries return ≤ 20 hits"*)

## Step 7 — Write Artifact

Mint the design stub:

```bash
rota design add <ID> --title "<title>"
```

The verb creates the design with frontmatter (`id`, `title`, `status: draft`, `created`) and the five placeholder section headers. On the issue backend it is a note on the item's issue (`references/issue-mode.md`): draft the approved sections from Step 6 in a scratch file and publish with `rota design put <ID> --body-file <scratch-file>`. On the file backend it is `.rota/designs/<ID>.md`: use the `Edit` tool to overwrite each placeholder section body, keeping the frontmatter intact. Read it back with `rota design show <ID>`. Post each answer that changed the design's direction with `rota item comment add <ID> --kind decision --body-file -`.

## Step 8 — Self-Review

Scan per the shared shape — see `references/design-exploration.md` (placeholders, internal contradictions, scope creep, ambiguous adjectives). Item-specific check: every claim ties back to the item ID; nothing leaks into a sibling item or future milestone. Internal-contradiction check: Step 5's chosen approach matches the Design section; Open questions don't conflict with Assumptions.

## Step 9 — User Review Gate

Print the final artifact (or invoke `rota design show <ID>`) and ask via `AskUserQuestion` per the shared review-gate shape (see `references/design-exploration.md`). Item-specific routes:

- **Approve and hand off to `/rota-plan`**
- **Revise** — return to Step 6 with the user's redlines
- **Stop here** — design is enough for now; no plan handoff

## Step 10 — Report

One compact summary:

```
Design written: #58 — <title>
  Artifact: design note on #58 (file backend: .rota/designs/F58.md)
  Approaches considered: 3
  Open questions: 2
  Status: draft
```

If the user approved-and-hand-off:

- Milestone-tagged item → *"Run `/rota-plan <milestone>-<ID>` next."*
- Untagged item → *"Run `/rota-plan` next (it will prompt for milestone)."*

If the user picked *Stop here* → exit without a `/rota-plan` nudge.

## Anti-pattern guard

> *"This item is too simple to need a design"* is rationalization. Every brainstormed item gets a design pass, but the design can be short — a few sentences in Goal and Design with empty Approaches-considered is a valid output for a truly simple item. The artifact captures the negotiated outcome, not a word count.

## Key Principles

- **No noise.** Don't narrate context loads; don't recap discovery rounds; don't pad the artifact with adjectives.
- **Design is negotiable; the artifact captures the negotiated outcome.** Step 9 is where alignment lands on disk.
- **Soft input to `/rota-plan` — never required.** Plans without a design stay valid; the `design:` pointer is additive.
- **Scope is single-item.** Project-level exploration is `/rota-vision`; slice-level is `/rota-plan` slice mode.

## References

- [`references/design-exploration.md`](references/design-exploration.md) — Shared spine (Socratic discovery, 2-3 approaches, sectioned design with per-section approval, self-review, user-review gate) used by `/rota-vision` and `/rota-brainstorm`.
