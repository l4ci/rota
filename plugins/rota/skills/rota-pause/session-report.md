# Session report for `/rota-pause --report`

Loaded by `skills/rota-pause/SKILL.md` Step 4 when the user passed `--report`.

Skip this unless the user passed `--report`. Not for an orchestrator pause (Step 1 sends that to `rota round summary`).

For each entry, write the report next to its note: the note path from `rota status handoff ... --canonical` with `.md` replaced by `.report.md` (`<branch>.report.md`, or `<branch>@<repo>.report.md` in a wave). Always overwrite. Sections, in order, from [`references/handoff-template.md`](references/handoff-template.md) *Session report*:

- **Commits:** `git log --format='%h %s' "$(rota git base)"..HEAD`.
- **Files changed:** `git diff --stat "$(rota git base)"...HEAD`.
- **Tasks:** the finished `Task:` markers from the task ledger, one per line.
- **Proof:** `rota proof show <ID>` for each paused item.

The report is never read on resume and never committed.
