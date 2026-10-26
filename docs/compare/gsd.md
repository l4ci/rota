# rota vs GSD

GSD ("Get Shit Done") is a meta-prompting, context-engineering and spec-driven development system for AI coding agents. It runs each unit of work (a phase) through discuss, plan, execute, verify and ship, with research, planning and execution handled by fresh-context subagents and state kept in a `.planning/` folder. The original repository, `glittercowboy/get-shit-done`, now lives at [`gsd-build/get-shit-done`](https://github.com/gsd-build/get-shit-done), which is archived with a pointer to the active project, [GSD Core](https://github.com/open-gsd/gsd-core) (`open-gsd/gsd-core`, MIT). This page compares rota with GSD Core. Checked 2026-10-26 against GSD Core v1.19.0 and rota 0.26.0.

## The short version

- Pick GSD when the hard part is deciding and specifying what to build: a greenfield project, a roadmap of phases, decisions captured before planning, and a spec you want verified against requirements.
- Pick rota when you already have a backlog of issues and want several of them built in parallel, each in its own worktree, and merged only after the full test suite passes on the merged tree.
- They overlap on the single-agent loop (plan, build, verify, ship). GSD's unit is a phase of a milestone, rota's is a tracker issue.

## Side by side

| | GSD Core | rota |
|---|---|---|
| What it is | Spec-driven meta-prompting framework: slash commands, workflows and agents installed into your agent | A Go binary plus `/rota-*` skills for Claude Code and Codex |
| Unit of work | A phase inside a milestone (`ROADMAP.md`, `PLAN.md` files) | A backlog item or tracker issue |
| Parallel agents | Plans in a phase run in dependency waves; `parallelization.max_concurrent_agents` defaults to 3 | A round: an orchestrator plus 2 to 5 standing workers, one issue each |
| Isolation | Git worktrees for parallel executors (`workflow.use_worktrees`, default true); sequential when off | One worktree per worker, reused across issues; optional per-slot ports and database |
| Verification before merge | A verifier agent checks requirement and decision coverage, writes `VERIFICATION.md` and fix plans; optional code review; human UAT | `test.full` runs once on a scratch merged tree under the gate; `/rota-review` runs Spec and Standards reviewers |
| Who merges | `/gsd-ship` opens the PR; you merge | The orchestrator, through the gate. Workers never merge |
| Backlog / tracker | `ROADMAP.md` and `.planning/`; its docs say it has no tracker integration and describe a manual issue-to-phase loop | `.rota/BACKLOG.md`, GitHub, GitLab, Jira, Linear or It's a Plan |
| Memory across sessions | `STATE.md`, `CONTEXT.md`, research and plan files; optional global learnings store | `KNOWLEDGE.md`, `DECISIONS.md` and handoff notes in `.rota/` |
| Agents and models | Claude Code, Codex, OpenCode, Copilot, Cursor, Windsurf, Kimi CLI, Antigravity and others, via an installer | Claude Code, Codex, OpenCode; several accounts with usage-limit awareness |
| Platforms | Installer handles Windows, macOS, Linux, WSL, Docker | Linux and macOS; Windows through WSL2 |
| Install | `npx @opengsd/gsd-core@latest` | `brew install l4ci/tap/rota` or `install.sh` |

## Where GSD is stronger

- Upfront specification. The discuss step records implementation decisions in `CONTEXT.md` before planning, and a research agent and plan-checker run before any code. rota has `/rota-brainstorm`, `/rota-plan` and `/rota-vision`, but they work per item or milestone, not as a mandatory loop.
- Verification against intent. The verifier checks requirement IDs and captured decisions, not only whether tests pass. rota's gate proves the merged tree is green; it does not trace requirements.
- Reach. It installs into many more agent runtimes than rota's three, and on Windows natively; rota needs WSL2 there.
- Greenfield start. `/gsd-new-project` and `/gsd-onboard` take a project to a roadmap. rota assumes you capture work yourself.

## Where rota is stronger

- Parallelism across issues. GSD parallelizes plans inside one phase. A [rota round](../usage/parallel-rounds.md) runs separate issues side by side, with `rota round assign`, `wait` and `worker gate` doing the bookkeeping ([unattended rounds](../usage/unattended-rounds.md) cover long runs).
- A merge gate. `rota worker gate` runs the full suite on a scratch merge of each PR and merges only on a pass; a red result goes back to the worker. GSD ends at "open a PR".
- Trackers. The backlog can be GitHub, GitLab, Jira or Linear issues ([issue backend](../usage/issue-backend.md)), so work comes from where your team already files it.
- Usage limits. `round.accounts` and `rota limit watch` move work to another account when one hits a limit ([several accounts](../usage/parallel-rounds.md)).

## Using both

They compose loosely. You could use GSD to produce a roadmap and phase plans, then capture each plan task as a tracker issue and let a rota round build them. The folders (`.planning/`, `.rota/`) do not collide. This is a pattern, not a documented integration.

## Sources

- [gsd-build/get-shit-done README](https://github.com/gsd-build/get-shit-done): archived, redirects to GSD Core.
- [open-gsd/gsd-core](https://github.com/open-gsd/gsd-core) README and releases: v1.19.0, MIT, install, supported runtimes, the five-step loop.
- [The phase loop](https://github.com/open-gsd/gsd-core/blob/main/docs/explanation/the-phase-loop.md): discuss, plan, verify, ship and `.planning/` artifacts.
- [Configuration](https://github.com/open-gsd/gsd-core/blob/main/docs/CONFIGURATION.md): `use_worktrees`, `parallelization`, `ship`, model profiles.
- [Issue-driven orchestration](https://github.com/open-gsd/gsd-core/blob/main/docs/issue-driven-orchestration.md): states "no tracker integration, no daemon".
- [Architecture](https://github.com/open-gsd/gsd-core/blob/main/docs/ARCHITECTURE.md) and [Features](https://github.com/open-gsd/gsd-core/blob/main/docs/FEATURES.md): agents, runtimes, Windows support, global learnings.

More comparisons: [how rota compares](README.md).
