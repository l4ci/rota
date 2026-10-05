# Subagent dispatch discipline

Cross-skill rulebook for when and how skills push work into subagents instead of doing it on the orchestrator's main thread. The orchestrator is a dispatcher + synthesizer; reads, scans, summaries, and serial queries belong elsewhere.

Cited by `references/authoring-conventions.md`. Companion to the worktree-isolation rule in `.rota/DECISIONS.md` (the 2026-05-02 entry on `work.isolation` for ≥2 commit-producing parallel workers).

## When to dispatch

Cost/benefit rule, not a vibe.

**Dispatch when:**

- Read-heavy exploration — ≥3 file reads or greps in one step
- Independent parallel work — N items, same operation, no shared mutable state
- Context-polluting tool output — long logs, large diffs, multi-page query results
- Fan-out research — multiple angles on the same question

**Do not dispatch when:**

- ≤2 small reads
- Work depends on context the orchestrator has already loaded
- Step is interactive (`AskUserQuestion`, Socratic discovery)
- The brief itself would cost more tokens than the work

## Small-brief template

Briefs say what the orchestrator needs back, not what the orchestrator already knows.

- **Goal** — 1 sentence
- **Inputs** — paths / IDs only, never pasted content
- **Constraints** — relevant forbids + hard boundaries from `.rota/DECISIONS.md`
- **Return shape** — exact structure expected back
- **Word budget** — default ≤200 words

## Return-shape contract

Subagents return synthesis, not transcripts. Structured shape: `findings · decisions · open questions`. The caller treats the return as the source of truth; the worker's working memory is discarded.

## Model tier per work type

- `haiku` — mechanical: parse JSON, count items, format markdown, run a known query and relay its output
- `sonnet` — routine reasoning: summarize a file, classify items, search and synthesize
- `opus` — judgment: verification, design selection, hypothesis evaluation

Read `models.*` from `.rota/config.json` when the skill exposes the keys (`models.orchestrator`, `models.worker`). Haiku usage is opportunistic — declared inline in the brief, not in config.

## Parallel fan-out pattern

When dispatching N independent subagents:

- Issue all `Agent` tool calls in a **single assistant turn** (one message, multiple tool-use blocks) so they run concurrently.
- Independence requirement: no shared mutable state between workers. File disjointness is mandatory; for commit-producing waves the worktree-isolation rule from `.rota/DECISIONS.md` (2026-05-02) applies — under `work.isolation == "branch"`, ≥2 commit-producing parallel workers in one wave is forbidden because they race the shared `.git/index`.
- Aggregation: the orchestrator collects returns and merges per the return-shape contract above. Workers never communicate with each other; the orchestrator is the only synthesizer.

Read-only workers (research, summary, query relays) are exempt from the worktree-isolation guard — they don't touch `.git/`. The guard fires only when ≥2 workers in a single wave are instructed to stage and commit.

## What stays on the orchestrator

- **Decisions** — which approach, which file, which next step.
- **User interaction** — `AskUserQuestion`, Socratic flows.
- **Atomic disk writes** — when ordering or all-or-nothing matters.
- **Verification of subagent output** — confirm the return shape, sanity-check claims, reconcile contradictions.

The orchestrator is dispatcher + synthesizer. Never reader-of-everything.
