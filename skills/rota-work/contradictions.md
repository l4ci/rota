# Knowledge-vs-correction contradictions (Step 2.5)

Loaded by `SKILL.md` Step 2.5 when Step 2 produced a non-Recommended answer.

When Step 2 produced a non-Recommended answer that pushes back on a stated assumption, cache the correction text. After Step 4's K+D query, a bullet is a candidate when ≥4 contiguous words of it appear in the correction (case-insensitive). Log each, in parallel:

```bash
rota knowledge contradiction add --topic <T> --title <S> --text "<first 200 chars of correction>"
```

`/rota-learn` Step 9 surfaces these at session end and asks per bullet whether to demote. Skip silently when Step 2 didn't run.
