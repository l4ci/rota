# Milestone tagging

Used by `/rota-capture` Step 4.5. Tagging is optional: an untagged item is fully workable, plannable and shippable.

Tag only when the user named a milestone (`--milestone M01`, *"for M02"*), or when exactly one milestone is active (`rota milestone active --json`, `data.ids`) and the items plainly belong to it. In that case ask one question, `AskUserQuestion`, single-select: *"Tag these with `<MID> — <title>`?"* — *"Yes — tag all"* / *"No — leave untagged (Recommended)"*. With no active milestone, or several and none named, skip the step and leave the items untagged. An ambiguous reply means untagged; under-tagging is recoverable, mis-tagging clutters the milestone view.

Carry the choice (`"M01"` or `"M01, M03"`) as `--milestone` into rota-capture's Step 6. Omit it when untagged.

Not covered here: sub-repo tagging (`Repos:`, see `references/umbrella-mode.md`) and detail-file extraction (`references/detail-files.md`).
