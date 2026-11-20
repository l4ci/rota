# Step 6 — Harness and model

Loaded by `SKILL.md` Step 6 when the text names a harness or a model.

When the text names a harness ("with codex") or a model, record it so `rota round assign` picks it up. Issue backend: after create, `rota issues label <N> --add harness:codex` and, for a model, `--add model:<id>` (values `harness:claude|codex`; one of each). File backend: end `--desc` with `Harness: codex Model: <id>`. Add nothing when the text names neither.
