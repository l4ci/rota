---
skill: rota-debug
lane: execute
order: 45
situation: Something broke
summary: Find the root cause of one bug, fix it atomically and prove it.
not_when:
  - use: rota-work
    when: the cause is obvious and the fix is a one-liner
  - use: rota-capture
    when: the bug is not captured yet
  - use: rota-spike
    when: you are testing whether an approach can work, not chasing a defect
before: [rota-capture, rota-work]
after: [rota-learn, rota-ship]
manual_only: false
flags: []
example:
  source: walkthroughs/brownfield-existing-project.md
  reviewed: false
  steps:
    - you: /rota-debug B01
    - agent: |
        Reproduced: TestDedupAcrossIntegrations fails, two events instead of one.
        Hypothesis: dedupKey includes severity, and the integrations differ in casing.
        Verified before touching code: lowercased severities give matching keys.
        Fix: lowercase severity in the dedup-key computation. Reproducer passes.
        fix: normalize severity casing in dedup key [B01]
        Non-obvious cause: run /rota-learn to save this gotcha.
  changes:
    - one atomic fix commit on a `rota/fix-*` branch
    - a proof row for the reproducer
    - a PR on the issue backend; on the file backend B01 is marked complete instead
---
# /rota-debug

Systematic root-cause cycle for a single bug (`[B##]` on the file backend, `#N` on the issue backend): reproduce, state a testable hypothesis and verify it before touching code, fix with one atomic commit, confirm the reproducer passes, record proof and open a PR (on the file backend it marks the item complete instead). After two refuted hypotheses it re-reads the symptom and the reproducer from scratch. One circuit breaker: after 3 failed committed fixes (counted per item in `.rota/verdicts.json`, so a new session or branch does not reset it) the Iron Law hard-stops with no further attempts and surfaces to the user. It uses the same isolation mode as `/rota-work` and nudges you toward [`/rota-learn`](../usage/learning.md) when the root cause was non-obvious. See [debugging](../usage/debugging.md) for the full flow.
