---
skill: rota-review
lane: ship
order: 50
situation: Done
summary: Review a branch against its spec and conventions before it leaves.
not_when:
  - use: rota-qa
    when: you need evidence the built product works (perf, a11y, smoke), not a read of the diff
  - use: rota-refactor
    when: you want code changed based on findings rather than a verdict
  - use: rota-work
    when: the code is still in flight
before: [rota-work]
after: [rota-ship]
manual_only: false
flags:
  - flag: "--since <sha>"
    does: re-review a bounced branch, only the fix, against the last review's findings
  - flag: "--queue"
    does: issue backend; review every needs-review item's open PR or MR and merge the ones that pass
example:
  source: getting-started.md
  reviewed: false
  steps:
    - you: /rota-review
    - agent: |
        Spec reviewer: B03 promised --limit 0 yields zero rows; cmd/list.go and TestListLimitZero match.
        Standards reviewer: conventions and test quality fine.
        Verdict: PASS (the worse of the two)
  changes:
    - nothing in the tree (read-only, no commits)
    - spec and quality verdicts recorded against the reviewed sha
---
# /rota-review

Staff-engineer review of a feature branch before it leaves your machine: scopes the diff, pulls relevant `KNOWLEDGE.md` topics and returns PASS / CONCERNS / FAIL with file-and-line evidence. Read-only: no mutations, no commits. On the issue backend, `--queue` instead works through every `needs-review` item's open PR or MR and merges the ones that pass. See [review and ship](../usage/review-and-ship.md) for the full flow.
