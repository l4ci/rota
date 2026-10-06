# Step 4.5 — Tag Active Milestone

Loaded by `SKILL.md` Step 4.5 when the user named a milestone or one milestone is active.

Tag only when the user named a milestone (`--milestone M01`, *"for M02"*), or when exactly one milestone is active (`rota milestone active --json`, `data.ids`) and the items plainly belong to it. In that case ask one question, `AskUserQuestion`, single-select: *"Tag these with `<MID> — <title>`?"* — *"Yes — tag all"* / *"No — leave untagged (Recommended)"*. With no active milestone, or several and none named, skip and leave untagged. An ambiguous reply means untagged.

Carry the choice (`"M01"` or `"M01, M03"`) as `--milestone` into Step 6. Omit it when untagged.
