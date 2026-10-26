---
skill: rota-vision
lane: plan
order: 110
situation: Bigger picture
summary: Turn an idea into milestones with dependencies and ready/blocked status.
not_when:
  - use: rota-brainstorm
    when: the question is the design of one backlog item
  - use: rota-plan
    when: you need tasks for one item or slice, not the roadmap
before: []
after: [rota-capture, rota-plan]
manual_only: false
flags: []
example:
  source: walkthroughs/greenfield-from-brief.md#step-2-rota-vision
  reviewed: true
  steps:
    - you: /rota-vision
    - agent: |
        I found README.md (Tally). Seed milestones from it?
        Your brief says "dogfood within two weeks." What does dogfooded mean concretely?
    - you: a CLI I use daily for two weeks before showing anyone else
    - agent: |
        Proposed: M01 CLI MVP, M02 Web dashboard (depends M01), M03 Budget alerts (depends M01).
        M01 marked active.
  changes:
    - "`.rota/MILESTONES.md` and `.rota/milestones/M01.md` ... `M03.md`"
    - the always-on `## Project Vision` block in `AGENTS.md`
---
# /rota-vision

Brainstorms a project's bigger vision and breaks it into milestones with explicit dependencies and ready/blocked status, writing `MILESTONES.md` and per-milestone detail files under `.rota/milestones/` (on the issue backend each milestone is also a tracker milestone with a tracking issue). It uses Socratic discovery, web research and deliberate challenge before proposing milestones. See [vision and plans](../usage/vision-and-plans.md) for the full flow.
