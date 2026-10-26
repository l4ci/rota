---
skill: rota-brainstorm
lane: plan
order: 20
situation: Not sure how
summary: Negotiate the design of one Major item before planning it.
not_when:
  - use: rota-vision
    when: the scope is the whole project or a milestone, not one backlog item
  - use: rota-plan
    when: the shape is already clear and you need tasks with verify steps
  - use: rota-spike
    when: one factual question must be tried out before any design makes sense
before: [rota-capture]
after: [rota-plan]
manual_only: false
flags:
  - flag: "--grill"
    does: force the one-pass grilling of open decisions on any item, not only Major features and P0 bugs
example:
  source: usage/brainstorm.md#one-example-end-to-end
  reviewed: false
  steps:
    - you: /rota-brainstorm 42
    - agent: |
        Read #42, KNOWLEDGE.md and DECISIONS.md.
        Question: does "resolved" mean Completed only, or ARCHIVE.md too?
    - you: Completed only.
    - agent: |
        Design drafted: 3 approaches with tradeoffs, recommended #2.
        Approve and hand off to /rota-plan, approve with no plan yet, or revise?
  changes:
    - "the approved design stored as a note on issue #42 (file backend `.rota/designs/<ID>.md`)"
    - nothing in code
---
# /rota-brainstorm

Per-item design exploration before `/rota-plan`: Socratic discovery, 2-3 approaches with tradeoffs, sectioned design with per-section approval; stores the design as `.rota/designs/<ID>.md` (file backend) or a note on the issue (issue backend), which `/rota-plan` reads as soft input. Major features and P0 bugs get one grilling pass first (every open decision settled before drafting); `--grill` forces that pass on any item. It is skipped when the issue body already has `## Acceptance`.

See [brainstorming a design](../usage/brainstorm.md) for the full flow.
