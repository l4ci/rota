# `/rota-debug` hypothesize (single and competing modes)

Loaded by `/rota-debug` Step 6. Two modes share one brief template and return shape but differ in agent count, framing, and cycle-counter escalation behavior. The mode is selected by `debug.competingHypotheses` in `.rota/config.json` (default `false`).

## Brief template (both modes)

```
Investigate [B##]: <title>.

**Symptom:**
<reproducer output, stack trace, or observed vs expected>

**Entry point(s):**
<file paths + line numbers you suspect are involved>

**Relevant knowledge:**
<bullets from `rota knowledge query`, if any>
<entries from `rota decisions query`, if any — boundaries that rule out fix directions>
<terms from `rota glossary read`, if any — definitions to align bug-report phrasing to canonical names; flag drift between the report's wording and the term's definition since misnamed components are a frequent root cause of misattributed bugs>

[FRAMING — competing mode only: insert one lens prompt below]

Read the code organically. Do not propose a fix yet.

Return: ranked list of 2-3 hypotheses, each with
  - the causal chain (what triggers what)
  - the file:line evidence
  - a concrete verification probe (code to read, a print statement to add, a test to run)
```

## Single hypothesis (default)

Dispatch one orchestrator-model agent with no FRAMING line. Pick the top hypothesis; verify both if the top two are close.

## Competing hypotheses (`debug.competingHypotheses: true`)

Dispatch **3 parallel orchestrator-model agents** in one tool-call batch. Each gets a different framing lens:

- **Recent-changes lens:** *"Start from recent commits — run `git log --oneline -20 -- <suspect paths>`. The bug likely correlates with something that changed; frame hypotheses around what was modified and why."*
- **Data-shape lens:** *"Start from the values flowing through the suspect path. The bug likely arises when a value violates an implicit contract — null/empty, off-by-one, wrong type, stale cache, malformed upstream input. Trace data, not code."*
- **Concurrency / lifecycle lens:** *"Start from timing and ordering. Likely a race window, ordering assumption, partial state, double-fire, listener registered twice, async resolution out of order, or a reference held past its lifetime. Look for state, not logic."*

After all three return: deduplicate (same root cause from different angles → one hypothesis, keep sharper wording), pick the strongest regardless of lens (verify both if the top two are close), discard weak ones silently — don't relay every angle's output.

## Mode divergence (shared spine + intentional differences)

| Axis | Single (default) | Competing (`debug.competingHypotheses: true`) |
|---|---|---|
| Agents dispatched | 1 orchestrator-model agent | 3 orchestrator-model agents in parallel (one tool-call batch) |
| FRAMING line in brief | omitted | one lens prompt per agent (recent-changes / data-shape / concurrency-lifecycle) |
| Cycle-counter escalation | counter ≥ 3 routes to Step 7.5 instead of dispatching | does not escalate — 3 lenses already cover diverse angles |
| Post-return selection | pick top hypothesis; verify both if top two are close | dedupe across angles, pick strongest regardless of lens, discard weak silently |
| Brief template | shared verbatim | shared verbatim (FRAMING slot filled per-agent) |
| Return shape | shared (2–3 ranked hypotheses with causal chain, file:line evidence, verification probe) | shared |

**Why divergence is intentional.** Single mode optimizes for cost — one orchestrator dispatch is cheap when bugs have an obvious surface. Competing mode optimizes for breadth — 3 lenses cover the recent-changes / data-shape / concurrency-lifecycle root-cause categories upfront, useful when the bug surface is unclear and grinding one hypothesis would waste cycles. The config flag exists so projects can pick the right default for their typical bug shape; the cycle-counter rule (single-mode only) is the dynamic escape valve when single-mode runs out of angles.
