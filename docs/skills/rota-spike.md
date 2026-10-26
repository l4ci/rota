---
skill: rota-spike
lane: plan
order: 25
situation: Not sure how
summary: Try one risky idea on a throwaway branch and keep only the finding.
not_when:
  - use: rota-brainstorm
    when: the question is which design to pick, not whether something works
  - use: rota-work
    when: the result should be shipped code, not a finding
before: [rota-vision, rota-brainstorm]
after: [rota-plan]
manual_only: false
flags:
  - flag: "done <name>"
    does: close the spike with a verdict (viable, not viable, depends-on-X, inconclusive)
example:
  source: walkthroughs/greenfield-from-brief.md#step-3-rota-spike-optional
  reviewed: true
  steps:
    - you: /rota-spike will a single-currency schema survive a later move to multi-currency?
    - agent: |
        Question: can a single-currency schema migrate to multi-currency without data loss? Named it sqlite-schema.
        Created branch spike/sqlite-schema and .rota/spikes/sqlite-schema.md.
        The question is recorded. Experiment on the branch; fill in Findings and Decision when done.
  changes:
    - a `spike/<name>` branch that is never merged
    - "`.rota/spikes/<name>.md` with the question, findings and decision"
---
# /rota-spike

Throwaway feasibility experiment on a dedicated `spike/<name>` branch that is never merged. It answers a specific yes/no/conditional question and records the question, what was tried, the findings and the decision in `.rota/spikes/<name>.md`. See [spikes](../usage/spikes.md).
