---
name: rota-decide
description: Use on "decide on X", "we're committing to X", "lock in the boundary that Y", or when a session has produced a constraint future work must respect.
disable-model-invocation: true
---

# rota-decide — Capture Hard-Boundary Decisions

Distill an active commitment from the session into `.rota/DECISIONS.md`, by topic, so future work treats it as a hard constraint. Decisions are *active* (boundaries with forbids/permits); `/rota-learn` captures *passive* knowledge (gotchas, conventions).

Copy this checklist and track your progress:
```
- [ ] Step 1 — Mode (default vs source-prefill)
- [ ] Step 2 — Identify the Candidate Decision
- [ ] Step 3 — Compose the Four Parts
- [ ] Step 4 — Classify by Topic
- [ ] Step 5 — Confirmation Gate
- [ ] Step 6 — Merge into DECISIONS.md
- [ ] Step 7 — Update the Decisions Index
- [ ] Step 8 — Confirm
```

## Step 1 — Mode (default vs source-prefill)

- **No flag** — default mode: Step 2 elicits the candidate conversationally.
- **`--from-learning <topic>`** — Source-Prefill Mode (Learning): Step 2 seeds the draft from `.rota/KNOWLEDGE.md`.
- **`--from-spike <name>`** — Source-Prefill Mode (Spike): Step 2 seeds the draft from `.rota/spikes/<name>.md`.

- **`--supersede <topic> "<title>"`** / **`--retire <topic> "<title>"`** — Lifecycle Mode: mark an existing decision superseded or retired instead of adding one. Read [`retire-modes.md`](retire-modes.md) and follow it instead of Steps 2–8.

The two source flags together are invalid: error with *"`/rota-decide` accepts at most one of `--from-learning <topic>` or `--from-spike <name>` per invocation."* and stop. A lifecycle flag combined with a source flag is invalid too.

## Step 2 — Identify the Candidate Decision

A decision must be **active** (committed; violating it is a regression), **bounded** (forbids X, permits Y) and **justified** (a why: incident, deadline, stakeholder ask).

**Three-gate trigger (pre-write check).** **All three must pass** to proceed past Step 2, in every mode (default, `--from-learning`, `--from-spike`):

(a) **Hard to reverse.** Undoing needs coordinated edits across many files, retraining habits or data migration. `git revert` plus a small refactor means preference, not decision.

(b) **Surprising without context.** A future contributor would not infer the rule from existing patterns. If the codebase already documents it, it's a convention.

(c) **Real trade-off.** Genuine alternatives existed and the project deliberately passed on them. One option only means a default, not a decision.

If **any** gate fails, do **not** write to `DECISIONS.md`. Surface to the user:

> "This reads like a [preference / convention / default] rather than a hard boundary — gate (X) failed. Run `/rota-learn` to capture it as durable knowledge instead, or leave it inline at the call site."

Substitute the failing gate's letter for `(X)`; suggest `/rota-learn` if a learning is worth keeping, else "leave inline", and stop. **Do not auto-invoke `/rota-learn`**: same manual-gate policy as the no-forbids/no-permits redirect below.

**Default mode.**

Surface a clear candidate from the conversation; if none, ask:

> "What boundary do you want to lock in? State it as one sentence — what the decision says."

Ground the boundary with `references/grilling.md`: pin the rule's terms, then grill forbids and permits as numbered questions with a recommended answer each, answering from code first and testing the edges with a scenario. Stop at an empty frontier.

If after one round the user can't articulate **forbids** *or* **permits**, it's a learning, not a decision: suggest `/rota-learn` and stop. **Do not auto-invoke `/rota-learn`**; the user re-runs it deliberately.

When invoked with `--from-learning <topic>` or `--from-spike <name>`, read [`source-prefill-modes.md`](source-prefill-modes.md), then continue to Step 3.

## Step 3 — Compose the Four Parts

Every decision entry has four parts:

1. **Rule** — one-sentence statement of what the decision says
2. **Why** — one paragraph (incident, constraint, deadline, ask)
3. **Forbids** — concrete patterns, files, approaches ruled out
4. **Permits** — what this still allows (keeps the boundary from over-applying)

**Draft all four from conversation context.** One question only when a part is genuinely ambiguous; otherwise show the draft and let Step 5 handle approval. In source-prefill modes Step 3 is the user's chance to redline.

## Step 4 — Classify by Topic

Reuse existing `## Topic` headings in `.rota/DECISIONS.md`, then `KNOWLEDGE.md` topics (`Architecture`, `Testing`, `Build & Tooling`, …) so one name maps to both files. New topic only if nothing fits; keep topics coarser than learnings.

## Step 5 — Confirmation Gate

Present the assembled entry to the user with a question:

- **Header:** `"Decide"`
- **Question:** *"Lock in this decision?"*
- **Options** (single-select):
  1. *"Write it (Recommended)"* — *"Append to `.rota/DECISIONS.md` under `<topic>` and update the decisions index."*
  2. *"Edit first"* — *"Show the draft inline so you can rewrite any of the four parts before writing."*
  3. *"Cancel"* — *"Skip — nothing is written."*

Show the full draft (rule, why, forbids, permits) above the question. Write only on an explicit "Write it"; anything else cancels. **Edit first** takes revisions and re-asks. **Cancel** stops with *"Decision not captured."*

## Step 6 — Merge into DECISIONS.md

Format of `.rota/DECISIONS.md`:

```markdown
# Decisions

Hard boundaries for this project. Each entry is a commitment, not a preference — re-read before proposing changes that touch its area.

## <Topic>

### <Decision title>

<One-sentence rule.>

*Why.* <One paragraph rationale.>

**Forbids.** <Concrete patterns/files/approaches.>

**Permits.** <What this still allows.>

<!-- 2026-05-01 -->
```

**Merge rules:**

- Never rewrite sections you didn't change; make targeted edits rather than replacing the whole file.
- Insert new `### Decision title` blocks at the **top** of their topic (newest first).
- Stamp today's absolute date as `<!-- YYYY-MM-DD -->`.
- New topics go alphabetically, except `Architecture` and `Build & Tooling` may be pinned near the top (as in `KNOWLEDGE.md`).

## Step 7 — Update the Decisions Index

```bash
rota block decisions
```

Updates the managed `<!-- rota-decisions-start -->` block in the project instructions file (`AGENTS.md` if present, else `CLAUDE.md`; the verb resolves it, never hardcode). The read-site skills (`/rota-work`, `/rota-debug`, `/rota-plan`, `/rota-refactor`, `/rota-review`, `/rota-vision`) read this block to know when to consult `DECISIONS.md`.

## Step 8 — Confirm

Report in one compact block:

```
Captured 1 decision into .rota/DECISIONS.md:
  Architecture — "Background jobs run in-process, never via external queue"

Updated the decisions index in <AGENTS.md|CLAUDE.md> — /rota-work, /rota-debug, /rota-plan, /rota-refactor, /rota-review, /rota-vision will consult it.
```

If the entry created a new topic, prepend a line: *"New topic: `<topic>`."*

## Key Principles

- **Never auto-invoked.** Regardless of `autonomy.level`; Step 5's confirmation is the only gate. Manual confirmation is the verification: no verifier runs.
- **One sentence rule, one paragraph why.** More: link a plan or knowledge entry.

## References

- [`references/grilling.md`](references/grilling.md) — Frontier-round questioning for forbids/permits.
- [`references/subagent-dispatch.md`](references/subagent-dispatch.md) — the `light` subagent that grilling sends for broad reads.
- [`references/persistence-skills.md`](references/persistence-skills.md) — Shared spine and divergence axes for the persistence duo (`/rota-learn`, `/rota-decide`), including `/rota-learn --term` for Glossary entries.
- [`references/source-prefill.md`](references/source-prefill.md) — Source-prefill / promote-between-artifacts semantics for `/rota-decide`.
- [`retire-modes.md`](retire-modes.md) — `--supersede` / `--retire` flow, status line format and gate.
- [`source-prefill-modes.md`](source-prefill-modes.md) — Source-prefill seeding rules and mode-to-section table.
