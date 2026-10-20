---
name: rota-answer
description: Use only when the maintainer types "/rota-answer" or asks the orchestrator to put the open decisions to them one by one during a round.
disable-model-invocation: true
---

# rota-answer: Put Pending Decisions to the Maintainer

The orchestrator gathers every decision waiting on the maintainer, asks them one at a time, records each answer where the round reads it and applies it before the next question. Decisions pile up in prose across status messages; this skill puts each one in front of the maintainer once, with options and a recommendation.

This is the one place a round uses a blocking question picker. [`/rota-orchestrate`](../rota-orchestrate/SKILL.md) forbids a picker otherwise, and that rule stands: only a maintainer-invoked `/rota-answer` asks this way, and nothing else starts it.

## When NOT to use

- The maintainer did not invoke it. Ask nothing by picker unprompted; ask in prose and keep working the round.
- A decision is a hard boundary to record → `/rota-decide`.
- You are a worker → `ROTA-BLOCKED` with one question, per the worker contract.

Copy this checklist and track your progress:
```
- [ ] Step 1 — Collect
- [ ] Step 2 — Ask one at a time
- [ ] Step 3 — Record
- [ ] Step 4 — Apply
- [ ] Step 5 — Report
```

## Step 1 — Collect

Keep `rota round watch` armed in the background for the whole run. A wake that arrives mid-run is handled after the current question, not during it.

Gather from these sources, in this order (blocking work first):

1. Open escalations: `rota round status --json`, field `escalations`.
2. Slots `blocked` on a question.
3. PRs whose review or bounce cap needs a maintainer call: a Spec finding that needs a waiver, a bounce at `round.maxBounces`.
4. Candidates you are holding for a wrong premise or an open design question.
5. Items with an open `question` comment (`rota item comment list <ID>`).

Each becomes one question: the item or PR number, the facts needed to decide, 2 to 4 options, the recommended option first. Drop what the code or the issue already answers ([`references/authoring-conventions.md`](references/authoring-conventions.md), *Don't ask what the code can answer*).

With nothing pending, print `no decisions pending` and stop.

## Step 2 — Ask one at a time

One question per call or message. Never batch unrelated decisions.

- **Claude Code:** `AskUserQuestion`, one question per call. Recommended option first, its label ending `(Recommended)`. The tool adds its own "Other" free-text choice.
- **Any other harness (Codex, OpenCode, ...):** one plain message holding one question and a lettered list of at most 5 answers, `A)` to `E)`. `A)` is the recommendation and says so. The last letter is always "Write your own answer". The maintainer replies with a letter or their own text; ask the next question only after the reply.

Map a free-text reply to an option only when the intent is clear; otherwise ask once more. Silence or an unavailable picker is never an answer.

## Step 3 — Record

Write the answer as a `decision` comment on the item or PR's issue: `rota item comment add <ID> --kind decision --body-file <path>`. Word the channel truthfully: "maintainer, answering the orchestrator in its session". Never present it as a relay or as a comment the maintainer typed on the tracker.

When the question was an open escalation, also run `rota round escalate check` so the round reads the thread as answered.

## Step 4 — Apply

Act on the answer before the next question. One of:

- relay to the worker: `rota worker dispatch --relay`, signed, citing the in-session channel as the source
- re-record the verdict (`/rota-review` verdicts, a waiver in the PR's `## Approvals`)
- edit the issue body, or file the follow-up
- leave the item unassigned

A relay to a worker is a relay: the worker cites it as `orchestrator relay round N`, never as the maintainer.

Run the validator the action needs (the verb's exit code, `rota round status`) and continue only when it passes.

## Step 5 — Report

One line per decision: `<item>: <answer> → <what was done>`. Then re-arm the watch if it exited and go back to the round.

## Thought → Reality

| Thought | Reality |
|---|---|
| "I'll ask this in a picker without being invoked, it's quick" | `/rota-orchestrate` section 3 forbids it. Ask in prose. |
| "Two decisions are related, one question will do" | One decision per call; the answers need their own record. |
| "The maintainer said it in prose, skip the comment" | Step 3: the decision comment is the record the round reads. |
| "I'll ask everything first, apply after" | Step 4 applies each answer before the next question. |

## Key Principles

- One decision per question, blocking work first.
- Recommended option first, marked.
- Record the real channel, apply right away.
- No new verb: the skill composes `rota round`, `rota item comment` and `rota worker` verbs.
