# Spikes

A spike is a throwaway feasibility experiment. `/rota-spike` creates a dedicated git branch and a question record in `.rota/spikes/<name>.md`. The branch never merges; only the findings come back.

## When to spike

Use a spike when a milestone hinges on a question you cannot answer by reasoning alone:

- *"Can SSE work over our nginx setup?"*
- *"Is this library's threading model compatible with our concurrency model?"*
- *"Does this approach scale to N items without a full rewrite?"*

Spike when implementing without an answer carries real risk and the answer changes the plan rather than an implementation detail. If you already know what to do, that's a backlog item, not a spike.

## /rota-spike

Run `/rota-spike <name> "<question>"` to start. It creates two things:

- A `spike/<name>` branch off your current HEAD.
- `.rota/spikes/<name>.md` with a question stub ready for your findings.

Experiment freely on that branch: try the library, prototype the integration. The skill keeps you on the question rather than on production code. Nothing from the branch ships; the value is the markdown record.

### Package check (opt-in)

Set `research.packageCheck` to `true` (default `false`, see [configuration](configuration.md#researchpackagecheck)) and the skill checks each new package it would put in a finding against its registry before it does. This **makes network calls** to npm, PyPI, crates.io or the Go proxy. A `missing` package is dropped. A `suspect` one (first published under 90 days ago, or no source repository link) is listed with its reason and kept only if you say yes. Dependencies already in the repo are not checked.

## How spikes end

When you have enough evidence, run `/rota-spike done <name>`. It records one of four verdicts in `.rota/spikes/<name>.md`:

| Verdict | Meaning |
|---------|---------|
| `viable` | Yes, this works; proceed with confidence |
| `not viable` | No, this doesn't work; rule it out |
| `depends-on-X` | Conditional; works only if a specific condition holds |
| `inconclusive` | Not enough evidence; document what you tried and why it wasn't enough |

A `not viable` verdict is useful: it tells the team what not to build. Fill in what you tried and the evidence behind the verdict.

The spike branch stays around as a reference.

When the verdict is `viable`, `not viable`, or `depends-on-X`, `/rota-spike done <name>` prints a one-line nudge: run `/rota-decide --from-spike <name>` to promote the finding to a hard-boundary decision in `DECISIONS.md`. It does not ask or dispatch it. When you run it, the spike's question, verdict, and recommended approach pre-fill the rule and why; you supply the forbids and permits that distinguish a decision from a learning. `inconclusive` spikes skip the nudge. See [decisions](decisions.md) for the rule/why/forbids/permits structure.

## After the spike

The findings feed back into whatever decision triggered the spike, usually a milestone or implementation plan. If the spike came out of a [`/rota-vision`](vision-and-plans.md) session, reference the spike file from the relevant milestone detail file (`.rota/milestones/M01.md`). If a [`/rota-plan`](vision-and-plans.md) depends on the verdict, call it out in the plan's named assumptions or open questions section.

See [vision and plans](vision-and-plans.md) for how spikes fit the broader planning flow.

## Spikes vs capture and work

| Skill | Use when… |
|-------|-----------|
| `/rota-spike` | You don't know if something is even possible and need to find out before committing |
| `/rota-capture`, then `/rota-work <ID>` | You know exactly what to do and want to file it and implement it |
| `/rota-work` | You have a captured backlog item and want the orchestrator to implement it properly |

Spike when you don't know; work when you do. See [running work](running-work.md) for how `/rota-capture` and `/rota-work` behave once the question is settled.

## Spike hygiene

- **Keep branches short-lived.** A spike that's been sitting for three weeks is probably stale.
- **Name the question clearly.** `spike/sse-nginx` beats `spike/experiment1`.
- **Write findings while context is fresh.** The moment you close the experiment is the best moment to write the verdict and evidence.
- **Archive or delete branches whose findings are no longer relevant.** Don't let abandoned ones pile up.
- **Don't grow a spike into a feature.** If the spike turns out `viable` and you want to build it properly, create a backlog item and start fresh on a real branch.
