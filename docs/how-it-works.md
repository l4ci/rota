# How rota works

You choose the tasks and make product decisions. rota coordinates the agents that implement, review and check the changes.

## One conversation, several workers

Start `/rota-orchestrate` in your coding agent. That session becomes the orchestrator: it chooses ready issues, assigns workers and follows their progress.

Workers run in separate worktrees, using the configured harnesses and accounts. Each builds one issue and opens a PR. The orchestrator handles review and merging.

```text
                  worker → PR
ready issues →    worker → PR    → review → merge checks → main
                  worker → PR
```

## Checks before merging

The merge gate checks the required review records and runs the configured verification on a scratch merge of the PR and the current base. On a failure, the change needs more work. On a pass, it can merge, subject to your approval policy.

This verifies the combination that will land. What it catches depends on the checks you configure. [Safeguards and limits](agent-mistakes.md).

## You stay in control

Agents bring unresolved product choices back to you. Repeated failed attempts can park an issue for human help. You can require approval before each merge and ask the orchestrator to wind down.

With multiple accounts, rota can distribute work and react to usage limits. [Account and restart behavior](usage/unattended-rounds.md) depends on the harness and supervision you configure.

## Context stays with the project

Knowledge, decisions and handoff notes help later sessions pick up the work. The agent calls the rota CLI to manage this state; you steer the round in your existing conversation.

[Start your first round](getting-started.md) · [Other workflows](skills/workflows.md) · [Reference](reference/README.md)
