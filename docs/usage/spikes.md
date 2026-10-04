# Spikes

A spike is a throwaway feasibility experiment. `/rota-spike` creates a dedicated git branch and a question record in `.rota/spikes/<name>.md`. The branch never merges. Only the findings come back.

## When to spike

Use a spike when a milestone hinges on a question you cannot answer from the chair:

- *"Can SSE work over our nginx setup?"*
- *"Is this library's threading model compatible with our concurrency model?"*
- *"Does this approach scale to N items without a full rewrite?"*

The tell: implementing without an answer carries real risk, and the answer changes the plan rather than an implementation detail. If you already know what to do, that's a backlog item, not a spike.

## /rota-spike

Run `/rota-spike <name> "<question>"` to start. It creates two things:

- A `spike/<name>` branch off your current HEAD.
- `.rota/spikes/<name>.md` with a question stub ready for your findings.

You experiment freely on that branch: try the library, prototype the integration, whatever it takes to answer the question. The skill keeps you focused on the question rather than on production code. Nothing from that branch is expected to ship. The value is the markdown record, not the experimental code.

## How spikes end

When you have enough evidence, update `.rota/spikes/<name>.md` with one of four verdicts:

| Verdict | Meaning |
|---------|---------|
| `viable` | Yes, this works; proceed with confidence |
| `not viable` | No, this doesn't work; rule it out |
| `depends-on-X` | Conditional; works only if a specific condition holds |
| `inconclusive` | Not enough evidence; document what you tried and why it wasn't enough |

Honest reporting matters more than salvage. A `not viable` conclusion is useful: it tells the team what not to build. Fill in what you tried and the evidence behind the verdict while context is fresh.

The spike branch stays around as a reference but is never merged.

After you mark the spike done, the skill asks one extra question when the verdict is `viable`, `not viable`, or `depends-on-X`: *"Promote the finding to a hard-boundary decision in `DECISIONS.md`?"* If you say yes, it dispatches `/rota-decide --from-spike <name>`. The spike's question, verdict, and recommended approach pre-fill the rule and why; you supply the forbids and permits that distinguish a decision from a learning. `inconclusive` spikes skip the prompt. See [decisions](decisions.md) for the rule/why/forbids/permits structure.

## After the spike

The findings feed back into whatever decision triggered the spike, usually a milestone or implementation plan. If the spike came out of a [`/rota-vision`](vision-and-plans.md) session, reference the spike file from the relevant milestone detail file (`.rota/milestones/M01.md`). If a [`/rota-plan`](vision-and-plans.md) depends on the verdict, call it out in the plan's named assumptions or open questions section.

See [vision and plans](vision-and-plans.md) for how spikes fit the broader planning flow.

## Spikes vs capture-and-work vs /rota-work

| Skill | Use when… |
|-------|-----------|
| `/rota-spike` | You don't know if something is even possible and need to find out before committing |
| `/rota-capture` (accept the hand-off) | You know exactly what to do and want to capture and implement it in one pass |
| `/rota-work` | You have a captured backlog item and want the orchestrator to implement it properly |

Spike when you don't know; work when you do. Skipping a spike on a hunch costs more than running one.

See [running work](running-work.md) for how the `/rota-capture` hand-off and `/rota-work` behave once the question is settled.

## Spike hygiene

- **Keep branches short-lived.** A spike that's been sitting for three weeks is probably stale.
- **Name the question clearly.** `spike/sse-nginx` beats `spike/experiment1`. Future you will thank present you.
- **Write findings while context is fresh.** The moment you close the experiment is the best moment to write the verdict and evidence.
- **Archive or delete branches whose findings are no longer relevant.** Spikes are research artifacts. Don't let abandoned ones pile up.
- **Don't grow a spike into a feature.** If the spike turns out `viable` and you want to build it properly, create a backlog item and start fresh on a real branch.
