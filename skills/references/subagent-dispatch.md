# Subagent dispatch discipline

Cross-skill rulebook for when and how skills push work into subagents instead of the orchestrator's main thread. The orchestrator is a dispatcher and synthesizer; reads, scans, summaries and serial queries belong elsewhere.

Skills MUST consult this file for any step that trips the *Dispatch when* thresholds below; the rule sets a floor, and each skill judges which of its steps trip it. Companion to the worktree-isolation rule in `.rota/DECISIONS.md` (`work.isolation` for ≥2 commit-producing parallel workers).

## When to dispatch

A cost/benefit rule, not a vibe.

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

Also forbidden: cross-worker communication, returning full transcripts instead of synthesis, and calling out to `superpowers:dispatching-parallel-agents` or other external skills (this discipline is self-contained). Mixed tiers in one wave are fine (a `light` subagent beside three `standard` ones in the same turn).

## Small-brief template

Briefs say what the orchestrator needs back, not what the orchestrator already knows.

- **Goal** — 1 sentence
- **Inputs** — paths / IDs only, never pasted content
- **Constraints** — relevant forbids + hard boundaries from `.rota/DECISIONS.md`
- **Return shape** — exact structure expected back
- **Word budget** — default ≤200 words

## Return-shape contract

Subagents return synthesis, not transcripts. Structured shape: `findings · decisions · open questions`. The caller treats the return as the source of truth; the worker's working memory is discarded.

## Subagent tiers

Skills size a subagent by **tier**, never by model name. Three tiers, lightest first:

| Tier | Work | Claude model (default) | Config key |
|---|---|---|---|
| `light` | mechanical or read-only: parse JSON, count items, format markdown, search, discover, relay a known query | `haiku` | `round.tiers.claude.light` |
| `standard` | routine reasoning and writing: summarize a file, classify items, write code and tests | `sonnet` | `round.tiers.claude.standard`, which follows `models.worker` until set |
| `heavy` | judgment: verification, design selection, hypothesis evaluation, hard debugging | `opus` | `round.tiers.claude.heavy` |

`rota round assign --tier` picks the tier of a round worker from the same table; Codex maps tiers through `round.tiers.codex.*` (see `docs/usage/configuration.md`, *Round keys*). The tier is chosen per subagent call; the main session's model is `models.orchestrator`, not a tier. A skill that names a subagent model says `light`, `standard` or `heavy`; the model comes from this table. Where the `Agent` tool takes a literal `model`, resolve the tier through the config key. Haiku usage is opportunistic: declared inline in the brief, not in config.

## Parallel fan-out pattern

When dispatching N independent subagents:

- Issue all `Agent` tool calls in a **single assistant turn** (one message, multiple tool-use blocks) so they run concurrently.
- Independence requirement: no shared mutable state between workers. File disjointness is mandatory; for commit-producing waves the worktree-isolation rule from `.rota/DECISIONS.md` applies — under `work.isolation == "branch"`, ≥2 commit-producing parallel workers in one wave is forbidden because they race the shared `.git/index`.
- Aggregation: the orchestrator collects returns and merges per the return-shape contract above. Workers never communicate with each other; the orchestrator is the only synthesizer.

Read-only workers (research, summary, query relays) are exempt from the worktree-isolation guard because they don't touch `.git/`. The guard fires only when ≥2 workers in one wave are told to stage and commit.

## What stays on the orchestrator

- **Decisions** — which approach, which file, which next step.
- **User interaction** — `AskUserQuestion`, Socratic flows.
- **Atomic disk writes** — when ordering or all-or-nothing matters.
- **Verification of subagent output** — confirm the return shape, sanity-check claims, reconcile contradictions.

The orchestrator dispatches and synthesizes; it never reads everything.
