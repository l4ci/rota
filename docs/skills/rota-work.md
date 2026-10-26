---
skill: rota-work
lane: execute
order: 40
situation: Ready to build
summary: Build the next item in small commits, one verified task at a time.
not_when:
  - use: rota-orchestrate
    when: several independent issues should run side by side with standing workers
  - use: rota-capture
    when: the work is not captured yet
  - use: rota-debug
    when: it is a bug whose cause you don't know yet
before: [rota-capture, rota-plan]
after: [rota-ship, rota-pause]
manual_only: false
flags:
  - flag: "--preview <ID>"
    does: read-only peek at the intended approach (files, tests, assumptions, unknowns); writes nothing
  - flag: "(no argument)"
    does: reconcile the backlog against git state, read any handoff note, and suggest the next item
example:
  source: walkthroughs/quickstart-manual-mode.md#step-3-work
  reviewed: true
  steps:
    - you: /rota-work B03
    - agent: |
        Tree clean. Branch rota/B03-limit-zero created.
        T1  treat 0 as a real limit in cmd/list.go
            rota proof record B03 -- go test ./cmd -run TestListLimitZero   FAIL (red run)
            rota proof record B03 -- go test ./cmd -run TestListLimitZero   PASS
        a1b2c3d fix: treat --limit 0 as zero rows [B03]
  changes:
    - a feature branch with one commit per task
    - a proof row per check
    - B03 marked in progress in .rota/status.json until it lands
---
# /rota-work

Parallel implementation: the orchestrator plans tasks and dispatches worker subagents to implement them, each on a feature branch or isolated worktree. With no item argument it first reconciles the backlog against git state (`rota status show`, `rota backlog drift`, `rota backlog list`, `rota milestone active`), reads any `/rota-pause` handoff note, and suggests the next item. It consults `KNOWLEDGE.md` and any matching `.rota/plans/<key>.md` at the start, then registers progress in `status.json`.

**The proof gate sits here.** Each task writes proof rows. For a behaviour change the test runs first and must fail on an assertion, then pass. `rota item complete` closes an item only when it has proof, and exits 4 otherwise.

See [running work](../usage/running-work.md) for the full flow and [picking work](../usage/picking-work.md) for the no-argument mode.
