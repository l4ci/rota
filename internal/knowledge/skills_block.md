## rota

This project uses rota for backlog tracking, planning, and skill orchestration. State lives in `.rota/` — most content is tracked (backlog, knowledge, decisions, plans, designs, milestones) so it travels with the repo. Only `.rota/status.json`, `.rota/repos.json`, `.rota/config.local.json`, `.rota/handoff/`, `.rota/debug/`, `.rota/review/`, `.rota/qa-runs/`, `.rota/verdicts.json`, `.rota/gate-audit.jsonl`, `.rota/ledger.jsonl`, `.rota/flakes.jsonl`, `.rota/bugreports.jsonl`, `.rota/train-cache.json`, `.rota/workers.json`, `.rota/receipts.json`, `.rota/**/knowledge-hits.jsonl*`, `.rota/**/*.proof` (file-mode proof rows), and `.rota/**/*.lock` files are gitignored. Use the skills and `rota` verbs to update tracked content (never edit by hand). Edit canonical sources (`skills/`, `cmd/`, `internal/`, `docs/`, `test/`) for skill changes.

**Capture & pick** — `/rota-capture` (with `--remove <ID>` to delete items; offers to hand off to `/rota-work`), `/rota-pause`
**Plan & build** — `/rota-brainstorm`, `/rota-plan`, `/rota-spike`, `/rota-work` (no argument reconciles active work and suggests the next item; `--preview` for read-only peek), `/rota-debug`
**Rounds** — `/rota-orchestrate` (run a parallel round as the orchestrator), `/rota-answer` (put pending decisions to the maintainer one by one)
**Review & ship** — `/rota-review`, `/rota-qa` (opt-in gate via `ship.qa`), `/rota-ship` (`--undo` to roll back the last cycle)
**Persist** — `/rota-learn` (durable knowledge; `--term <name>` for glossary), `/rota-decide` (hard boundaries — manual only)
**Vision & maps** — `/rota-vision`, `/rota-refactor`
**Maintenance** — `/rota-release`, `/rota-bugreport` (file rota's own mistakes upstream; nothing is filed without your yes), `/rota-docs` (maintain public docs: scaffold, after-work, restructure), `/rota-init` (suggest config values from the project, confirmed one by one). Setup and upkeep are `rota` verbs: `rota init`, `rota config`, `rota update`, `rota migrate`

Before acting on work that touches a topic listed in `## Project Knowledge`, `## Project Decisions`, or `## Project Vision`, pull only the relevant sections:

- `rota knowledge query <topic>…`
- `rota decisions query <topic>…`
- `rota glossary read <term>…` (terms live as nested-bullet entries under `## Glossary` in `.rota/KNOWLEDGE.md`)
- `rota milestone active` (then `rota backlog ids --milestone <id>` per active milestone)
