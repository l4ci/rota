---
skill: rota-orchestrate
lane: rounds
order: 70
situation: Many issues at once
summary: "Run a parallel round: pick the slate, watch workers, merge their PRs."
not_when:
  - use: rota-work
    when: you have one item and need no worker isolation
  - use: rota-capture
    when: the issues are not captured yet
before: [rota-capture, rota-plan]
after: [rota-learn, rota-release]
manual_only: false
flags: []
example:
  source: walkthroughs/round-cycle.md#2-run-the-round
  reviewed: true
  steps:
    - you: rota orchestrate
    - you: run a round on issues 42, 51, 58 and 63
    - agent: |
        rota round start --scope slate --items 42,51,58,63
        rota round assign 42   # claims #42, cuts <agent>/42-<slug>, starts a worker
        ...
        Armed rota round watch in the background; it wakes me on a slot, PR or escalation change.
  changes:
    - one worktree, branch and terminal tab per assigned issue
    - an orchestrator lease and slot rows in the round state
    - worker PRs that you review, gate and merge
---
# /rota-orchestrate

Runs a parallel round: you are the orchestrator, and standing workers (`round.slots`, at most as many as `round.roster` names) each take one issue in their own worktree and terminal tab. Start it with `rota orchestrate` or bare `rota` (`orchestrator.harness` picks the agent), or tell an existing session "you are the orchestrator". The skill decides what the `rota round` verbs cannot: which issues make a good slate, how to read a stuck worker, what to escalate to you and when to merge. Sequencing and the rules are the verbs' job (`rota doctor`, `rota round start`, `assign`, `wait`, `rota worker gate`, `rota round wind-down`), and a refusal from a verb is the rule, not an obstacle. Workers build and open PRs; they never merge. "You are the orchestrator" or "run a round" triggers it. For one item, use `/rota-work`. See [parallel rounds](../usage/parallel-rounds.md) and the [`rota round` verbs](../reference/cli-helpers.md#rota-round).

