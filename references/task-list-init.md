# Task list initialization

Canonical block referenced by every rota SKILL.md with three or more phases. Cited from each skill's Step 1; the phases list itself stays per-skill since it reflects the skill's natural structure.

## The block (cite this from SKILL.md)

> **Initialize task list.** When `TaskCreate` is loaded (load via `ToolSearch select:TaskCreate,TaskUpdate` if not), create one task per phase below — e.g. `TaskCreate(subject="<phase 1 name>", description="<phase 1 outcome>")`. Mark each `in_progress` when starting and `completed` when its observable outcome lands; short-circuited phases (no work to do — empty backlog, skipped review, etc.) get `completed` with the no-op reason in the description.

Place the Phases list directly after this citation, per-skill.

## How to cite from a SKILL.md

In Step 1 (after the preflight call), replace the inline boilerplate with this single line:

```markdown
**Initialize task list.** Follow the canonical pattern in `references/task-list-init.md` — load `TaskCreate` via `ToolSearch select:TaskCreate,TaskUpdate` if needed, then create one task per phase below.

Phases:

1. *<phase 1 name>* — <one-line outcome> (Step <N>)
2. *<phase 2 name>* — <one-line outcome> (Step <N>)
...
```

Keep the Phases list inline; only the boilerplate citation extracts.

## Why this lives here

- Authoring convention rule #4 ("Surface multi-step skill progress with TaskCreate") established the requirement; this reference owns the canonical wording so all citing skills stay byte-identical without manual cross-skill alignment.
- Host compatibility: on platforms where `TaskCreate` isn't loaded, the boilerplate self-skips silently — the `ToolSearch select:` load is conditional.
- Phase list per-skill: each skill's natural structure dictates the phases. Cross-skill alignment of phase names would force unnatural granularity; the citation extracts only the boilerplate shell.

## See also

- `authoring-conventions.md` rule #4 — original authoring requirement.
- Cited from: `rota-capture`, `rota-plan`, `rota-work`, `rota-debug`, `rota-review`, `rota-ship`, `rota-learn`, `rota-decide`, `rota-refactor`, and other multi-phase skills. New SKILL.md files cite this reference from their Step 1 instead of restating the boilerplate.
