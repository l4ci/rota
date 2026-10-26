---
skill: rota-pause
lane: persist
order: 100
situation: Out of context
summary: Stop cleanly and leave a handoff note for the next session.
not_when:
  - use: rota-learn
    when: you want to keep a durable gotcha, not the state of the work in flight
  - use: rota-ship
    when: the work is done and ready to integrate
before: [rota-work, rota-debug]
after: [rota-work]
manual_only: false
flags:
  - flag: "--report"
    does: also write .rota/handoff/<branch>.report.md (commits, files, tasks, proof) for you to read
example:
  source: usage/pausing-and-resuming.md
  reviewed: true
  steps:
    - you: /rota-pause
    - agent: |
        Uncommitted work on rota/fix-B07-timer-badge: wip commit, stash, or leave in place?
    - you: wip commit
    - agent: |
        Paused rota/fix-B07-timer-badge - handoff note saved.
        Resume with /rota-work in a fresh session.
  changes:
    - "`.rota/handoff/<branch>.md` (gitignored, one per branch, overwritten on re-pause)"
    - the chosen wip commit or stash
---
# /rota-pause

Stops mid-session (or an orchestrator mid-round, without winding the round down) by writing a handoff note to `.rota/handoff/<branch>.md` that captures current hypothesis, next planned step, files mid-edit, and gotchas discovered. Use it when the context window is filling or you need to step away mid-`/rota-work`. With `--report` it also writes `.rota/handoff/<branch>.report.md` (commits, files changed, tasks, proof) for you to read; `/rota-work` ignores it. See [pausing and resuming](../usage/pausing-and-resuming.md) for the full flow.
