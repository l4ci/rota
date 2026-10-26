---
skill: rota-decide
lane: persist
order: 85
situation: Worth remembering
summary: Record a hard-boundary decision with explicit forbids and permits.
not_when:
  - use: rota-learn
    when: you cannot state forbids and permits, so it is a gotcha rather than a commitment
  - use: rota-spike
    when: the question is still open and needs an experiment first
before: [rota-spike, rota-learn]
after: []
manual_only: true
flags:
  - flag: "--from-learning <topic>"
    does: seed rule and why from a KNOWLEDGE.md bullet
  - flag: "--from-spike <name>"
    does: seed rule and why from a spike finding (inconclusive spikes are refused)
  - flag: "--supersede <topic> \"<title>\""
    does: replace an existing decision with a new one
  - flag: "--retire <topic> \"<title>\""
    does: mark a decision lifted, nothing replacing it
example:
  source: usage/decisions.md#capturing-a-decision
  reviewed: false
  steps:
    - you: /rota-decide
    - agent: |
        Draft for topic "Architecture":
          Rule: never store session tokens client-side
          Why: <from the conversation>
          Forbids / Permits: <drafted from context>
        Write it? (nothing is written without your "Write it")
  changes:
    - a four-part entry (rule, why, forbids, permits) in .rota/DECISIONS.md
---
# /rota-decide

Captures a hard-boundary decision into `.rota/DECISIONS.md`. Manually confirmed, never auto-invoked. Decisions differ from learnings in `KNOWLEDGE.md` by being commitments with explicit forbids/permits; `/rota-work`, `/rota-debug`, `/rota-refactor`, `/rota-plan`, `/rota-brainstorm`, `/rota-vision` and [`/rota-review`](../usage/review-and-ship.md) consult them as constraints. Accepts `--from-learning <topic>` to promote a hardened `KNOWLEDGE.md` bullet into a decision (rule/why are pre-filled; you supply the forbids/permits), and `--from-spike <name>` to promote a `.rota/spikes/<name>.md` finding the same way (`inconclusive` spikes are refused). See [decisions](../usage/decisions.md) for the full flow.
