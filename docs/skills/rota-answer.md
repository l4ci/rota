---
skill: rota-answer
lane: rounds
order: 75
situation: Many issues at once
summary: "Answer every decision a round is waiting on, one question at a time."
not_when:
  - use: rota-orchestrate
    when: you want to run or steer the round itself, not answer its queued questions
  - use: rota-decide
    when: you are recording a lasting hard boundary rather than answering one pending question
before: [rota-orchestrate]
after: [rota-orchestrate]
manual_only: true
flags: []
example:
  source: null
  reviewed: true
  steps:
    - you: /rota-answer
    - agent: |
        1 of 3: slot nia is blocked on #58.
        Worker asks: keep the old --json shape or break it?
        A) Keep the old shape (recommended)
        B) Break it and note it in the changelog
        C) Write your own answer
    - you: A
    - agent: |
        Recorded as a decision comment on #58 and relayed to nia.
  changes:
    - a `decision` comment per answer, citing the maintainer
    - the answer applied (relay, re-recorded verdict, issue edit or follow-up)
---
# /rota-answer

During a round, asks you each pending decision in turn: open escalations, slots blocked on a question, PRs whose review or bounce cap needs a call, candidates the orchestrator is holding, and items with an open `question` comment. Each question gives the facts and 2 to 4 options, recommended one first. In Claude Code it uses the `AskUserQuestion` picker; in any other harness it sends one message per question with a lettered list `A)` to `E)` (`A)` the recommendation, the last entry "Write your own answer"). Each answer is recorded as a `decision` comment citing "maintainer, answering the orchestrator in its session", then applied (relay, re-recorded verdict, issue edit, follow-up) before the next question. With nothing pending it prints `no decisions pending`. Manual only: the orchestrator never starts it, and a round otherwise uses no blocking picker. See [parallel rounds](../usage/parallel-rounds.md#asking-the-maintainer).

