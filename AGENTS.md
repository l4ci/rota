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

- Architecture: Helper conventions & invariants: Go verbs & fakes: Test fakes & isolation
- Architecture: Helper conventions & invariants: Go verbs & fakes: Gate & tracker plumbing
- Architecture: Helper conventions & invariants: Go verbs & fakes: Verb registration & guards
- Architecture: Skill authoring
- Build & Tooling: Helpers & migrations
- Build & Tooling: Smoke testing: Sections & fixtures
- Build & Tooling: Smoke testing: Gate & frozen records
- Build & Tooling: Git & isolation
- Build & Tooling: Video pipeline
- Glossary
- Process: Knowledge capture
- Product: Market & competitors
- Rounds: Orchestration: Reviews & verdicts: Reviewer briefs & conduct
- Rounds: Orchestration: Reviews & verdicts: Verdict records & lenses
- Rounds: Orchestration: Reviews & verdicts: Bounces & re-reviews
- Rounds: Orchestration: Gate, trains & bookkeeping
- Rounds: Orchestration: Workers & relays
- Rounds: Orchestration: Slate & issues

<!-- rota-knowledge-end -->

<!-- rota-vision-start -->
## Project Vision

Project milestones are tracker milestones, each with a tracking issue (`rota milestone show MNN`). `.rota/MILESTONES.md` is the index.

_(no active milestones — all shipped or archived; run `/rota-vision` to plan more)_
<!-- rota-vision-end -->

## Working in this repo

**Don't edit `.rota/` by hand — use the skills and `rota` verbs.** The `## rota` block below lists which `.rota/` paths are tracked and which stay per-developer. The backlog lives in GitHub issues; `.rota/BACKLOG.md` is frozen and `.rota/issue-map.json` maps old file-mode IDs to issues. Real code/skill changes go in canonical sources: skill folders (`skills/rota-*/SKILL.md`), `cmd/` and `internal/` (the `rota` binary), `docs/`, `test/`.

**Run `bash test/smoke.sh` only at integration boundaries — not per task.** The full smoke suite is slow (several times slower than the sharded gate, which takes about 2–3 minutes: `bash test/gate.sh` shards it and runs it beside the Go checks). Per-task verification inside `/rota-work` and `/rota-debug` stays structural: `git status` / `git diff` / targeted greps / re-running the specific reproducer. Run the full smoke in `/rota-ship` and `/rota-review` (pre-merge / pre-PR), or when explicitly asked. If a single section is clearly relevant to the change in flight, sourcing just that section file in a sandbox is fine; defer the full run to ship time.

## Docs and the website

`docs/skills/<skill>.md` is the single source for what each `/rota-*` skill is for: front matter holds its lane, summary, the skills it is confused with, what usually comes before and after, flags and a short example; `docs/skills/README.md` holds the named chains, and a chain step may carry its own `example:` (same shape) when the skill's default example does not fit that chain's story. The chains in `docs/how-it-works.md` and the skill lists in `docs/README.md`, `docs/skills/README.md` and `README.md` are generated blocks: edit the pages, then run `python3 scripts/skills-docs.py` (doclint runs `--check` and fails on drift). Front matter must stay inside the YAML subset `scripts/fmlite.py` parses, since CI has no PyYAML. An example copied from a walkthrough transcript is `reviewed: true`; a reconstructed one stays `false` until a maintainer checks it.

The public site is <https://volkerotto.net/rota/>, a decoupled section of the `l4ci/l4ci.github.io` Jekyll repo (own layouts under `_layouts/rota/`, assets under `rota/assets/`). Its docs pages, sidebar and skill data (`rota/docs/`, `_data/rota/`) are generated from `docs/` by `python3 scripts/site-sync.py <site-checkout>`; never edit them there. After a docs change lands on `main`, rerun the sync in a checkout of that repo, build it, and push. The landing page (`rota/index.html`), the examples page (`rota/examples/`) and the skill explorer on them are hand-written there and read the generated skill data; the landing page plays only the chains marked `featured: true`.

<!-- rota-skills-start -->
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
<!-- rota-skills-end -->

<!-- rota-decisions-start -->
## Project Decisions

Hard boundaries live in `.rota/DECISIONS.md`. Consult them before acting on work that touches these topics:

- Architecture
- Rounds: Orchestration

<!-- rota-decisions-end -->

<!-- rota-map-start -->
## Project Map

Subsystems live in `.rota/MAP.md` (detail in `.rota/map/<name>.md`). Pull with `rota map query <name>`.

- **backlog-items** — backlog item model over file and issue backends, ID counters, plans, designs, spikes, milestones, status.json, debug counter and sessions, shared section/artifact helpers, blockedBy vocabulary
- **cli-dispatch** — the rota command dispatcher, exit-code contract, error translation and the terminal UI (bare-rota palette, --ui screens)
- **config-and-setup** — layered config, project seeding and registry, on-disk layout paths, version and update checks, and the one-shot migrations
- **doctor-and-reap** — read-only preflight checks (rota doctor) and fail-closed cleanup of what a round left behind (rota reap)
- **foundations** — leaf primitives every other package builds on: atomic files and locks, git and process execution, JSON, timestamps, liveness and small string helpers
- **gate-landing** — manual-gate registry, landing adapters and freeze, post-gate bookkeeping, footprint overlap, ship and release domain logic
- **hooks-guard** — Claude Code hooks: statusline state dump, Stop/prompt context guard, SessionStart handoff, PreToolUse worker guard, settings merge, plugin hooks/ dir
- **knowledge-and-map** — project knowledge, decisions and glossary files, the external knowledge repo, the subsystem map and QA indexes, staleness listing and the shared frontmatter parser
- **review-verdicts** — typed verdict store and routing, proof rows, acceptance-criteria grammar, test and flake ledgers, golden records, Go test-path rules
- **round-orchestration** — orchestrator side of a parallel round: lease, drift report, assign, autopilot tick, watch, escalation, ledger, receipts, launcher
- **skills-packaging** — the rota-* skill sources, their embedding in the binary, install into Claude Code and Codex, the plugin manifests, install.sh and the docs generators
- **terminal-hosts** — terminal host abstraction (herdr, tmux, Orca) that runs worker sessions, and the herdr pane layout (split/tabs)
- **test-infra** — the merge gate, sharded smoke suite, doc lints, fakes, e2e scenarios, evals and the shared Go test helpers
- **tracker-issues** — forge and issue-tracker adapters (gh, glab, Jira, Linear, Itsaplan), upstream-issue verbs, label registry, comment markers, note chunking, item-body grammar, tracker connections, bug-report log
- **workers** — worker slots, registry, gate/train/done verbs, agent harness adapters, subagent definitions, keepalive supervisor, usage limits
<!-- rota-map-end -->

<!-- rota-qa-start -->
## Project QA

QA strategies live in `.rota/QA.md` (detail in `.rota/qa/<target>.md`). Pull with `rota qa query <target>`. `/rota-qa run` consumes these; the skill never hardcodes runners.

- _(no QA strategy yet — run `/rota-qa first-run` to scaffold)_
<!-- rota-qa-end -->
