# How changes are checked

Coding agents can overbuild, write weak tests or call unfinished work complete. rota combines executable checks with review instructions. Their limits matter as much as their coverage.

## Before a change lands

In a configured PR round, workers open PRs and the orchestrator sends them through the merge gate. The gate checks required review records and runs your configured checks on a scratch merge with the current base. Failed checks block the merge.

A green gate means those checks passed. It cannot establish that every requirement is correct or every test is useful.

## Common agent mistakes

| What goes wrong | What rota does |
|---|---|
| The agent expands the task | Plan checks link tasks to acceptance criteria; reviewers examine scope. |
| Weak checks hide missing behavior | Proof records capture real command results. Optional test-dependence checks help identify tests that pass without the change. |
| The summary overstates success | The gate uses recorded results and required reviews before merging. |
| Failed fixes keep accumulating | Recorded attempt and bounce limits can stop work for human help. |
| A fresh session forgets a decision | Project knowledge, decisions and handoffs carry context forward; supported hooks supply relevant entries. |
| Product assumptions stay hidden | Workers are instructed to escalate product decisions; PR structure checks require assumption and ruling sections. |
| A rule gets skipped | CLI checks, locks and supported hooks enforce specific workflow boundaries. |

## What remains your responsibility

Choose useful checks and the level of review your project needs. A passing command may contain no meaningful tests. A required heading does not prove that an agent disclosed every assumption. Hook-based protection depends on the harness and installed hooks.

You can [require approval before each merge](usage/parallel-rounds.md#merge-approval). For each safeguard's configuration, enforcement boundary and remaining gaps, read [Safeguards and their limits](reference/agent-safeguards.md).
