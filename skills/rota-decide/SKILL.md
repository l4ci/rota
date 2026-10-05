---
name: rota-decide
description: Use on "decide on X", "we're committing to X", "lock in the boundary that Y", or when a session has produced a constraint future work must respect. Manual only, never auto-invoked.
---

# rota-decide — Capture Hard-Boundary Decisions

Distill an active commitment from the current session into `.rota/DECISIONS.md`, organized by topic, so future work consults it as a hard constraint. Decisions are *active* (committed boundaries with forbids/permits) — distinct from `/rota-learn` which captures *passive* knowledge (gotchas, conventions, constraints to remember).

**Task list.** Track these phases with the host's task tool if it has one:

1. *Mode* — default vs `--from-learning` vs `--from-spike` resolved (Step 1)
2. *Identify candidate* — boundary articulated; three-gate check passes (Step 2)
3. *Compose four parts* — rule, *Why*, **Forbids**, **Permits** drafted (Step 3)
4. *Confirmation gate* — manual user approval (never auto-invoked, Step 5)
5. *Merge & index update* — append to `DECISIONS.md`, regenerate the decisions index (Steps 6–7)

## Step 1 — Mode (default vs source-prefill)

Inspect the invocation arguments and pick a mode for the rest of the run:

- **No flag** — default mode. Step 2 elicits the candidate decision conversationally, as today.
- **`--from-learning <topic>`** — Source-Prefill Mode (Learning). Carry `<topic>` forward; Step 2 branches into the source-prefill path and seeds the draft from `.rota/KNOWLEDGE.md`.
- **`--from-spike <name>`** — Source-Prefill Mode (Spike). Carry `<name>` forward; Step 2 branches into the source-prefill path and seeds the draft from `.rota/spikes/<name>.md`.

Both flags simultaneously is invalid — error with *"`/rota-decide` accepts at most one of `--from-learning <topic>` or `--from-spike <name>` per invocation."* and stop.

## Step 2 — Identify the Candidate Decision

A decision is worth capturing if it is:

- **Active** — the project has *committed* to it. Violating it is a regression, not a missed opportunity.
- **Bounded** — it has a concrete shape: forbids X, permits Y.
- **Justified** — there's a why behind it (past incident, deadline, stakeholder ask, strong preference).

**Three-gate trigger (pre-write check).**

Active/Bounded/Justified is the conceptual definition; these three gates are the operational filter that catches preference choices that pass it. **All three must pass** for a candidate to proceed past Step 2 — across all modes (default, `--from-learning`, `--from-spike`):

(a) **Hard to reverse.** Undoing the rule would require coordinated edits across many files, retraining habits, or migrating data. If undoing is `git revert` plus a small refactor, it's a preference, not a decision.

(b) **Surprising without context.** A future contributor reading the code would not infer the rule from the existing patterns. If the rule is self-evident from the codebase — "we use TypeScript" in a TS-only repo, "tests live in `tests/`" in a project where every test already does — it's a convention the code already documents.

(c) **Real trade-off.** Genuine alternatives existed and the project deliberately didn't pick them. If only one option was ever on the table, the "rule" is documenting a default, not a decision.

If **any** gate fails, do **not** write to `DECISIONS.md`. Surface to the user:

> "This reads like a [preference / convention / default] rather than a hard boundary — gate (X) failed. Run `/rota-learn` to capture it as durable knowledge instead, or leave it inline at the call site."

Substitute the failing gate's letter for `(X)`. Suggest the redirect (`/rota-learn` if there's still a useful learning to capture, "leave inline" if it's just code-level) and stop. **Do not auto-invoke `/rota-learn`** — same manual-gate policy as the no-forbids/no-permits redirect below.

**Default mode.**

If the user invoked `/rota-decide` with a clear candidate from the conversation, surface it. If not, ask:

> "What boundary do you want to lock in? State it as one sentence — what the decision says."

Ground the boundary with `references/grilling.md`: pin the rule's terms, then grill forbids and permits as numbered questions with a recommended answer each, answering from code first and testing the edges with a scenario. Stop at an empty frontier.

If after one round the user can't articulate **forbids** *or* **permits**, surface that — it's a signal this is a learning, not a decision. Suggest `/rota-learn` instead and stop. **Do not auto-invoke `/rota-learn`** — the user re-runs it deliberately.

**Source-prefill modes (`--from-learning <topic>`, `--from-spike <name>`).** Both pre-fill the four-part draft from a source artifact and surface the same closing prompt. Full bodies live in `references/source-prefill.md`.

The semantic gap is preserved by design — source-prefill seeds only fields it can authoritatively provide (rule, *Why*) from the source's content; destination-specific fields (**Forbids**, **Permits**) stay as `_(user must articulate)_` placeholders that block the merge until the user fills them. The principle generalizes to any "promote A → B" flow where B carries an active commitment A doesn't. Sources without a commitment (an `inconclusive` spike) are refused at the gate — promotion requires a verdict the project is committing to.

| Mode | Section in `references/source-prefill.md` |
|------|-------------------------------------------|
| `--from-learning <topic>` | `## --from-learning <topic>` |
| `--from-spike <name>`     | `## --from-spike <name>`     |

After the mode runs and the user supplies Forbids/Permits, continue to Step 3.

## Step 3 — Compose the Four Parts

Every decision entry has four parts:

1. **Rule** — one-sentence statement of what the decision says
2. **Why** — one paragraph rationale (past incident, constraint, deadline, stakeholder ask)
3. **Forbids** — concrete things this rules out at apply time (patterns, files, approaches)
4. **Permits** — concrete things this still allows (anchors the boundary so it's not over-applied)

**Draft all four from conversation context.** Use a single `AskUserQuestion` call only when one of the four parts is genuinely ambiguous from context — otherwise show the assembled draft and let Step 5 handle approval.

In source-prefill modes, Step 2 has already drafted rule/why/forbids/permits — Step 3 is the user's chance to redline the draft before Step 5's confirmation gate.

## Step 4 — Classify by Topic

Open `.rota/DECISIONS.md` first and reuse existing `## Topic` headings when they fit. Also reuse `KNOWLEDGE.md` topics where overlap exists (`Architecture`, `Testing`, `Build & Tooling`, etc.) so a single topic name maps to both files. Create a new topic only if nothing fits.

Don't create a topic per decision — they should be coarser than learnings.

## Step 5 — Confirmation Gate

Present the assembled entry to the user via `AskUserQuestion`:

- **Header:** `"Decide"`
- **Question:** *"Lock in this decision?"*
- **Options** (single-select):
  1. *"Write it (Recommended)"* — *"Append to `.rota/DECISIONS.md` under `<topic>` and update the decisions index."*
  2. *"Edit first"* — *"Show the draft inline so you can rewrite any of the four parts before writing."*
  3. *"Cancel"* — *"Skip — nothing is written."*

Show the full draft entry (rule, why, forbids, permits) above the question. Write only on an explicit "Write it"; anything else is a cancel. **Edit first** shows the draft inline, takes revisions and re-asks. **Cancel** stops with *"Decision not captured."*

## Step 6 — Merge into DECISIONS.md

`.rota/DECISIONS.md` is organized as:

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

- Preserve existing topics — never rewrite sections you didn't change.
- Insert new `### Decision title` blocks at the **top** of their topic (newest first).
- Stamp new entries with today's absolute date as an HTML comment: `<!-- YYYY-MM-DD -->`.
- New topics go alphabetically, except `Architecture` and `Build & Tooling` may be pinned near the top (matching `KNOWLEDGE.md` convention).

Use `Edit` for surgical updates, not `Write`.

## Step 7 — Update the Decisions Index

```bash
rota block decisions
```

Reads `.rota/DECISIONS.md`, extracts `## Topic` headings in order, and updates the managed `<!-- rota-decisions-start -->` block in the project instructions file: `AGENTS.md` when it exists, else `CLAUDE.md`. The verb resolves the file; never hardcode one. Creates or appends as needed; never touches other content. The read-site skills (`/rota-work`, `/rota-debug`, `/rota-plan`, `/rota-refactor`, `/rota-review`, `/rota-vision`) read this block to know when to consult `DECISIONS.md`.

## Step 8 — Confirm

Tell the user, in one compact block, what was captured:

```
Captured 1 decision into .rota/DECISIONS.md:
  Architecture — "Background jobs run in-process, never via external queue"

Updated the decisions index in <AGENTS.md|CLAUDE.md> — /rota-work, /rota-debug, /rota-plan, /rota-refactor, /rota-review, /rota-vision will consult it.
```

If the entry created a new topic, prepend a line: *"New topic: `<topic>`."*

## Key Principles

- **Never auto-invoked.** Regardless of `autonomy.level`, and Step 5's confirmation is the only gate. Manual confirmation is the verification: no verifier runs.
- **One sentence rule, one paragraph why.** If a decision needs more, link to a plan or knowledge entry.

## References

- [`references/grilling.md`](references/grilling.md) — Frontier-round questioning for forbids/permits.
- [`references/subagent-dispatch.md`](references/subagent-dispatch.md) — the `light` subagent that grilling sends for broad reads.
- [`references/persistence-skills.md`](references/persistence-skills.md) — Shared spine and divergence axes for the persistence duo (`/rota-learn`, `/rota-decide`) — including `/rota-learn --term` for Glossary entries.
- [`references/source-prefill.md`](references/source-prefill.md) — Source-prefill / promote-between-artifacts semantics for `/rota-decide`.
