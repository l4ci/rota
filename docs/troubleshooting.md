# Troubleshooting

Ask the orchestrator about a blocked task in the same conversation. It can inspect the round and explain the next step.

## Setup does not pass

The orchestrator runs `rota doctor` before starting. Each failing check names the missing tool, login or configuration and gives a fix. Resolve those failures, then retry `/rota-orchestrate`.

[Worker setup](first-round.md) · [Doctor reference](usage/doctor-and-reap.md)

## No tests yet

Choose **leave for later** during `/rota-init` and finish initialization with test setup deferred. At the next `/rota-orchestrate` start, the agent revisits verification before assigning workers. The agreement must be configured and its required checks must pass before the first merge. The merge gate still refuses to merge when local verification has neither `test.full` nor `test.e2e`, unless explicitly overridden. Deferral does not authorize that override. Configured CI verification is another option.

In a new project, the agent recommends adding the first tests with the implementation; having no tests at init does not prevent starting that work. An existing build or lint command can be an explicitly agreed interim check, with reports identifying its limited coverage rather than claiming behavior tests ran.

Set the agreed command through the agent or with `rota config set test.full '<your command>'`. A command inferred from a manifest is only a suggestion: check that it actually runs useful tests.

Once verification is configured, later rounds use it without repeating the deferred-setup question unless something changes. See [a new project's first merge](first-round.md#starting-without-tests).

## A check fails

A failed merge check keeps the PR from landing. The orchestrator sends the findings back for repair and checks the updated result. Repeated failures can park the issue for your help.

Ask for the failed command and its output. [Merge checks](usage/parallel-rounds.md#merge-approval) describes the verdicts.

## An account reaches its limit

Separate workers can use different configured accounts. Moving work to another account or waiting for a reset depends on the harness, account pool and running limit watcher. Starting the skill alone does not set up supervision.

[Accounts and limits](usage/unattended-rounds.md)

## A session stops

For a supervised first round, return to the project and invoke `/rota-orchestrate`. It checks round state and available handoff notes before continuing. Do not delete active worktrees or force-clear another orchestrator's lease.

[Recovery and cleanup](usage/doctor-and-reap.md) · [Long-running rounds](usage/unattended-rounds.md)
