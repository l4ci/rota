# rota-learn verifier brief

Loaded by `/rota-learn` under `--strict` or `learn.verify: true`.

## Dispatch

Dispatch a `heavy` subagent through the current harness (`references/subagent-dispatch.md`). Cold read: don't pre-bias the verifier with your own notes.

## Brief (paste to the agent, substituting today's date)

```
You are the rota-learn verifier. Read these two files and judge whether the most recent additions are valid durable learnings.

Files:
- .rota/KNOWLEDGE.md  (entries stamped <!-- YYYY-MM-DD --> with today's date are the new ones)
- AGENTS.md or CLAUDE.md, whichever holds the block (the block between <!-- rota-knowledge-start --> and <!-- rota-knowledge-end -->)

Today's date: <absolute date>

For each new entry, judge:
1. Durable — will this still matter in 6 months, or is it ephemeral session state?
2. Sharp — concrete claim in one sentence, not vague advice?
3. Non-obvious — not something any reader would derive from the code or framework docs?
4. Correctly topic-ed — does the bullet sit under the right heading?
5. Not duplicated — does it restate an existing bullet in the same topic?

Also verify structural integrity:
- KNOWLEDGE.md headings are well-formed (## Topic)
- The managed block in the instructions file is intact, topic list matches KNOWLEDGE.md headings in order
- No accidental deletions of existing content

Return in this exact shape, ≤150 words total:

VERDICT: PASS | PASS_WITH_NOTES | FAIL
SUMMARY: <one sentence>
ENTRIES:
  - "<first 8 words of bullet>" — OK | weak: <reason> | duplicate of "<other>" | wrong topic (suggest: <topic>)
  - ...
STRUCTURE: OK | <what's broken>
```

## Applying the verdict

- **PASS** → proceed to the confirmation step.
- **PASS_WITH_NOTES** → Edit each flagged entry: reword weak bullets, remove duplicates, move wrong-topic bullets. Don't re-invoke the verifier; notes are advisory, not a gate.
- **FAIL** → Edit the new entries back out of `KNOWLEDGE.md` and the index block, then tell the user exactly which learnings were rejected and why. Stop.
- **STRUCTURE broken** → fix the specific structural issue regardless of verdict.
