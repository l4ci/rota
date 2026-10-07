## Parallel rounds

Larger rounds run as one orchestrator plus several workers in parallel, each worker a
standing agent with its own git worktree and herdr workspace, taking one issue at a time.
Agents are named people (ben, dana, nia, kit) reused across issues; work goes on
`<agent>/<issue>-<slug>` and its worktree parks on `park/<agent>` between issues. Workers
implement, verify and open a PR; they never merge. The orchestrator assigns issues, relays
decisions, merges PRs and re-verifies on `main` after every merge.

**"You are the orchestrator" is the kickoff trigger: invoke the `rota-orchestrate` skill.**
Read it and `docs/contributing/rounds.md` (the project brief: gate, repo rules, roster)
before running or joining a round. A worker reads `skills/references/worker-contract.md`.

<!-- rota-knowledge-start -->
## Project Knowledge

Durable learnings live in `.rota/KNOWLEDGE.md`. Consult it when work touches these topics:

- Architecture: Module extraction & migration safety
- Architecture: Helper conventions & invariants
- Architecture: Skill authoring
- Build & Tooling: Helpers & migrations
- Build & Tooling: Smoke testing
- Build & Tooling: Git & isolation
- Rounds: Orchestration

<!-- rota-knowledge-end -->

<!-- rota-vision-start -->
## Project Vision

Project milestones live in `.rota/MILESTONES.md`.

_(no active milestones — all shipped or archived; run `/rota-vision` to plan more)_
<!-- rota-vision-end -->

## Working in this repo

**Don't edit `.rota/` by hand — use the skills and `rota` verbs.** Most of `.rota/` is tracked (knowledge, decisions, backlog, milestones, designs, plans, spikes, per-item detail, release checklist, config). These paths stay gitignored: `.rota/status.json` and `.rota/repos.json` (per-developer runtime state); `.rota/config.local.json` (per-developer config overrides deep-merged on top of `.rota/config.json`); `.rota/handoff/` (per-developer `/rota-pause` scratch); `.rota/qa-runs/` (bulky `/rota-qa` artifacts); `.rota/gate-audit.jsonl` (per-developer log of manual-gate approvals); `.rota/train-cache.json` (per-developer merge train verdict cache); `.rota/workers.json` (per-developer worker slot registry); and `.rota/**/*.lock` (transient sidecar lockfiles `rota` takes around read-modify-write). Tracked `.rota/` content is skill-owned — capture via `/rota-capture`, learn via `/rota-learn`, decide via `/rota-decide`, etc. Real code/skill changes still go in canonical sources: skill folders (`skills/rota-*/SKILL.md`), `cmd/` and `internal/` (the `rota` binary), `docs/`, `test/`.

**Run `bash test/smoke.sh` only at integration boundaries — not per task.** The full smoke suite is slow (several times slower than the sharded gate, which takes about 2–3 minutes: `bash test/gate.sh` shards it and runs it beside the Go checks). Per-task verification inside `/rota-work` and `/rota-debug` stays structural: `git status` / `git diff` / targeted greps / re-running the specific reproducer. Run the full smoke in `/rota-ship` and `/rota-review` (pre-merge / pre-PR), or when explicitly asked. If a single section is clearly relevant to the change in flight, sourcing just that section file in a sandbox is fine; defer the full run to ship time.

<!-- rota-skills-start -->
## rota

This project uses rota for backlog tracking, planning, and skill orchestration. State lives in `.rota/` — most content is tracked (backlog, knowledge, decisions, plans, designs, milestones) so it travels with the repo. Only `.rota/status.json`, `.rota/repos.json`, `.rota/config.local.json`, `.rota/handoff/`, `.rota/review/`, `.rota/qa-runs/`, `.rota/verdicts.json`, `.rota/gate-audit.jsonl`, `.rota/train-cache.json`, `.rota/workers.json`, and `.rota/**/*.lock` files are gitignored. Use the skills and `rota` verbs to update tracked content (never edit by hand). Edit canonical sources (`skills/`, `cmd/`, `internal/`, `docs/`, `test/`) for skill changes.

**Capture & pick** — `/rota-capture` (with `--remove <ID>` to delete items; offers to hand off to `/rota-work`), `/rota-pause`
**Plan & build** — `/rota-brainstorm`, `/rota-plan`, `/rota-spike`, `/rota-work` (no argument reconciles active work and suggests the next item; `--preview` for read-only peek), `/rota-debug`
**Rounds** — `/rota-orchestrate` (run a parallel round as the orchestrator)
**Review & ship** — `/rota-review`, `/rota-qa` (opt-in gate via `ship.qa`), `/rota-ship` (`--undo` to roll back the last cycle, `--docs` to maintain public docs)
**Persist** — `/rota-learn` (durable knowledge; `--term <name>` for glossary), `/rota-decide` (hard boundaries — manual only)
**Vision & maps** — `/rota-vision`, `/rota-refactor`
**Maintenance** — `/rota-release`. Setup and upkeep are `rota` verbs: `rota init`, `rota config`, `rota update`, `rota migrate`

Before acting on work that touches a topic listed in `## Project Knowledge`, `## Project Decisions`, or `## Project Vision`, pull only the relevant sections:

- `rota knowledge query <topic>…`
- `rota decisions query <topic>…`
- `rota glossary read <term>…` (terms live as nested-bullet entries under `## Glossary` in `.rota/KNOWLEDGE.md`)
- `rota milestone active` (then `rota backlog ids --milestone <id>` per active milestone)
<!-- rota-skills-end -->

<!-- rota-decisions-start -->
## Project Decisions

Hard boundaries live in `.rota/DECISIONS.md`. Consult them before acting on work that touches these topics:

- Architecture

<!-- rota-decisions-end -->

<!-- rota-map-start -->
## Project Map

Subsystems live in `.rota/MAP.md` (detail in `.rota/map/<name>.md`). Pull with `rota map query <name>`.

- _(no subsystems yet — write `.rota/map/<name>.md` as you discover subsystems)_
<!-- rota-map-end -->

<!-- rota-qa-start -->
## Project QA

QA strategies live in `.rota/QA.md` (detail in `.rota/qa/<target>.md`). Pull with `rota qa query <target>`. `/rota-qa run` consumes these; the skill never hardcodes runners.

- _(no QA strategy yet — run `/rota-qa first-run` to scaffold)_
<!-- rota-qa-end -->
