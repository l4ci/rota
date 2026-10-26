---
skill: rota-plan
lane: plan
order: 30
situation: Ready to build
summary: Write a signed-off implementation plan with verifiable tasks.
not_when:
  - use: rota-work
    when: the item is small enough to build directly; work plans its own tasks
  - use: rota-brainstorm
    when: the design itself is still undecided
  - use: rota-vision
    when: you need milestones for the whole project, not a plan for one item or slice
before: [rota-brainstorm, rota-spike, rota-vision]
after: [rota-work]
manual_only: false
flags: []
example:
  source: walkthroughs/quickstart-manual-mode.md#step-2-plan-optional
  reviewed: true
  steps:
    - you: /rota-plan B03
    - agent: |
        Goal: --limit 0 yields zero rows.
        T1. Treat 0 as a real limit in cmd/list.go.
            Verify: go test ./cmd -run TestListLimitZero
        Confirm this plan? (yes / changes)
    - you: "yes"
  changes:
    - "the plan written to `.rota/plans/B03.md` (issue backend: a note on the issue)"
---
# /rota-plan

Writes an implementation plan before `/rota-work` runs, keyed by a milestone slice (`M01-S01`) or an item (`M01-B07`, `#42`). Each task must fit one execution window and have a verifiable outcome. `/rota-work` consults the plan automatically if one exists. See [vision and plans](../usage/vision-and-plans.md) for the full flow.
