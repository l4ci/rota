# Slash commands

Quick-reference table of every `/rota-*` command. Detailed entries follow below.

Setup, config, update and migration are `rota` verbs, not skills: `rota init` (and `rota init umbrella`), `rota config show` / `rota config set`, `rota update`, `rota migrate hv` / `rota migrate issues`. See [config options](config-options.md).

| Skill | Description |
|-------|-------------|
| `/rota-vision` | Brainstorm a project's bigger vision and milestones using Socratic discovery, web research, and a critique pass; writes `MILESTONES.md` plus per-milestone detail files |
| `/rota-brainstorm` | Per-item design exploration before `/rota-plan`: Socratic discovery, 2-3 approaches with tradeoffs, sectioned design with per-section approval; stores the design as `.rota/designs/<ID>.md` (file backend) or a note on the issue (issue backend), which `/rota-plan` reads as soft input |
| `/rota-capture` | Capture bugs, features, and tasks into the configured backlog (`.rota/BACKLOG.md` or GitHub/GitLab issues): auto-classifies, assigns priority/size, routes to the correct section or labels. On milestone-spec captures, audits the diff for ship-evidence and asks per flagged title before appending. Prints the new IDs and stops |
| `/rota-capture --remove` | Remove a captured backlog item and clean up its dependencies. Dry-run preview by default, asks before applying |
| `/rota-pause` | Gracefully stop mid-session; writes a handoff note (next step, hypothesis, mid-edit files) for the next session's `/rota-work` (no argument) |
| `/rota-plan` | Write an implementation plan for a milestone slice or item (`M01-S01`, `M01-B07`): task decomposition with verifiable outcomes, named assumptions, open questions; `/rota-work` consults if present |
| `/rota-spike` | Throwaway feasibility experiment on a `spike/<name>` branch. Branch never merges, only findings come back as `.rota/spikes/<name>.md` |
| `/rota-work` | Orchestrated parallel implementation with per-task commits. With no item argument it reconciles the backlog against git state and suggests the next item; consults `KNOWLEDGE.md` and `.rota/plans/<key>.md` if present. Pass `--preview <ID>` for a read-only peek of the intended approach (files, tests, assumptions, unknowns) that gates high-stakes work without writing anything |
| `/rota-orchestrate` | Run a [parallel round](../usage/parallel-rounds.md) as the orchestrator: choose the slate, read what workers are doing, answer or escalate their questions, merge their PRs, wind the round down. The `rota round` verbs do the sequencing; the skill holds the judgment |
| `/rota-debug` | Systematic bug cycle: reproduce, hypothesize, verify, fix with one atomic commit; hard-stops via the Iron Law after 3 failed committed fixes, nudges `/rota-learn` |
| `/rota-decide` | Capture a hard-boundary decision into `.rota/DECISIONS.md`. Manually confirmed, never auto-invoked; decisions differ from learnings by being active commitments with explicit forbids/permits |
| `/rota-review` | Review of a branch by two parallel reviewers, reported separately: Spec (intent match, drift, decision violations) and Standards (`KNOWLEDGE.md` conventions, code smells, test quality, silent failures); returns PASS / CONCERNS / FAIL, the worse of the two |
| `/rota-qa` | Product-level QA: executes per-target strategy files (`.rota/qa/<target>.md`) with Playwright / smoke / lighthouse / axe / ZAP / contract runners; emits PASS / CONCERNS / FAIL. Modes: first-run / run / restructure |
| `/rota-ship` | Bundle commits into a PR (or direct merge) with ID-linked body; runs `/rota-review` first by default, plus opt-in second-opinion (`ship.secondOpinion`) and product QA (`ship.qa`) gates. Flags: `--undo` (guided rollback of the last cycle on the base branch) and `--docs` (public-docs maintenance: first-run / after-work / restructure modes; auto-fires inline at ship time when `docs.afterWork: true`) |
| `/rota-learn` | Extract durable session learnings into `KNOWLEDGE.md`, grouped by topic; `--strict` adds Opus verification; `--retro` turns mistakes into guardrails and standards |
| `/rota-refactor` | Architecture review that files findings as refactor issues; `--fix` implements them |
| `/rota-release` | Cut a release: walk per-project checklist, bump version, generate notes, tag, push, publish to GitHub/GitLab |

---

Alphabetical reference of every `/rota-*` command.

## /rota-capture

Captures bugs, features, and tasks into the backlog with `rota item create`. Where the item lands depends on `backlog.backend`. On the file backend (default) it goes into [`BACKLOG.md`](rota-folder.md) under the correct section with a zero-padded auto-incrementing ID (`[B01]`, `[F01]`, `[T01]`). On the issue backend it becomes a GitHub or GitLab issue with type, priority and size labels, and the ID is the issue number (`#42`); see [issue backend](../usage/issue-backend.md). Either way it auto-classifies each item and assigns priority (P0/P1/P2) for bugs and size (Major/Minor/Cosmetic) for features. See [capturing work](../usage/capturing-work.md) for the full flow.

## /rota-debug

Systematic root-cause cycle for a single bug (`[B##]` on the file backend, `#N` on the issue backend): reproduce, state a testable hypothesis and verify it before touching code, fix with one atomic commit, confirm the reproducer passes, record proof and open a PR (on the file backend it marks the item complete instead). After two refuted hypotheses it re-reads the symptom and the reproducer from scratch. One circuit breaker: after 3 failed committed fixes (counted per item in `.rota/verdicts.json`, so a new session or branch does not reset it) the Iron Law fires a hard stop with no further attempts, surfacing to the user. Uses the same isolation mode as `/rota-work`, and nudges you toward [`/rota-learn`](../usage/learning.md) when the root cause was non-obvious. See [debugging](../usage/debugging.md) for the full flow.

## /rota-decide

Captures a hard-boundary decision into `.rota/DECISIONS.md`. Manually confirmed, never auto-invoked. Decisions differ from learnings in `KNOWLEDGE.md` by being commitments with explicit forbids/permits; `/rota-work`, `/rota-debug`, `/rota-refactor`, `/rota-plan`, `/rota-brainstorm`, `/rota-vision` and [`/rota-review`](../usage/review-and-ship.md) consult them as constraints. Accepts `--from-learning <topic>` to promote a hardened `KNOWLEDGE.md` bullet into a decision (rule/why are pre-filled; you supply the forbids/permits), and `--from-spike <name>` to promote a `.rota/spikes/<name>.md` finding the same way (`inconclusive` spikes are refused). See [decisions](../usage/decisions.md) for the full flow.

## /rota-learn

Writes durable knowledge from the current session into `.rota/KNOWLEDGE.md`, grouped by topic. Captures gotchas, project conventions, constraints, debugging insights, and decisions with rationale. Skips anything already obvious from reading the code. `--strict` adds a verifier pass over the new bullets (off by default). `--retro` classifies the session's mistakes into guardrails (filed as items), standards, navigation pointers and tool-economy fixes, and flags no-op bullets for removal without deleting them. In umbrella mode the write (and `--term` Glossary entries) routes to the cwd/`--repo`-resolved scope: repo-local vs the umbrella-shared `.rota/KNOWLEDGE.md`. See [learning](../usage/learning.md) and [umbrella mode](../usage/umbrella-mode.md) for the full flow.

## /rota-orchestrate

Runs a parallel round: you are the orchestrator, and standing workers (`work.workerSlots`, at most as many as `round.roster` names) each take one issue in their own worktree and terminal tab. Start it with `rota orchestrate` or bare `rota` (`orchestrator.harness` picks the agent), or tell an existing session "you are the orchestrator". The skill decides what the `rota round` verbs cannot: which issues make a good slate, how to read a stuck worker, what to escalate to you and when to merge. Sequencing and the rules are the verbs' job (`rota doctor`, `rota round start`, `assign`, `wait`, `rota worker gate`, `rota round wind-down`), and a refusal from a verb is the rule, not an obstacle. Workers build and open PRs; they never merge. Triggered by "you are the orchestrator" or "run a round". For one item, use `/rota-work`. See [parallel rounds](../usage/parallel-rounds.md) and the [`rota round` verbs](cli-helpers.md#rota-round).

## /rota-pause

Stops mid-session (or an orchestrator mid-round, without winding the round down) by writing a handoff note to `.rota/handoff/<branch>.md` that captures current hypothesis, next planned step, files mid-edit, and gotchas discovered. Use it when the context window is filling or you need to step away mid-`/rota-work`. See [pausing and resuming](../usage/pausing-and-resuming.md) for the full flow.

## /rota-plan

Writes an implementation plan before `/rota-work` runs, keyed by a milestone slice (`M01-S01`) or an item (`M01-B07`, `#42`). Tasks must fit one execution window and each requires a verifiable outcome. `/rota-work` consults the plan automatically if one exists. See [vision and plans](../usage/vision-and-plans.md) for the full flow.

## /rota-qa

Product-level QA: runs the per-target strategy declared in `.rota/qa/<target>.md` and emits a scored verdict. `/rota-qa` does NOT read commits or the diff (that's `/rota-review`'s job). It runs the built artifact: Playwright, smoke scripts, contract tests, Lighthouse, axe, ZAP, whatever the strategy declares, grouped into executable and audit pillars. Three modes: `first-run` (probe surfaces, propose strategy), `run` (execute, score, verdict), `restructure` (audit strategy files). Returns `PASS` / `CONCERNS` / `FAIL`, or `INFRA-FAIL` when required infra is missing. Invoked from `/rota-ship` when `ship.qa: true`; route on verdict gated by `qa.gate` (`"advisory"` reports only, `"blocking"` halts on `FAIL`). See [product QA](../usage/qa.md) for the full flow.

## /rota-refactor

Architecture review. Explores the codebase (or one area, `/rota-refactor internal/cli`) for friction using one vocabulary (module, interface, depth, seam, adapter, leverage, locality) and a few heuristics (the deletion test, the interface as the test surface, one adapter is a hypothetical seam). It reads the glossary, knowledge and decisions first, ranks candidates Strong / Worth exploring / Speculative, and files each as a `refactor`-labelled issue with a `## Acceptance` section, deduped against open and closed issues. It changes no code by default.

Flags: `--fix` implements the candidates you pick (parallel workers, verification, one commit; `refactor.confirmBeforeExecute` gates it), `--designs` drafts competing interfaces for structural candidates, `--interactive` lets you choose which to file. Several workers can each review one area in a round.

## /rota-release

Cuts a release end-to-end: walks the project's release checklist (`.rota/RELEASE.md` by default; override via `release.checklistPath`) as a preflight gate, bumps the project version (`major`/`minor`/`patch` or explicit semver), generates categorized release notes from commits since the last tag, prepends a section to `CHANGELOG.md` (creating it if absent), creates an annotated git tag, pushes commit + tag, and publishes a release on GitHub or GitLab when origin is set. Auto-detects the version source (`plugin.json`, `package.json`, `pyproject.toml`, `Cargo.toml`, plain `VERSION`), and honors `release.versionFile` override.

The checklist file is per-project and tracked by default; see [release checklist](../usage/review-and-ship.md#release-checklist) for the format. When absent, the skill offers to scaffold a starter (under `autonomy.level: off`) or silently skips the gate (under `auto`). Items ending in `(manual)` always interject even in unattended modes.

After publishing, an always-manual gate offers to close any upstream GitHub/GitLab issues that landed in the release but are still open. This covers the direct-push path where work skipped `/rota-ship` (or chose "leave open") and the linked issues never got closed. Candidates come from `rota issues imported --open-only`; the gate is silent when nothing is open. Skipped entirely in `--dry-run` mode.

## /rota-review

Staff-engineer review of a feature branch before it leaves your machine: scopes the diff, pulls relevant `KNOWLEDGE.md` topics, returns PASS / CONCERNS / FAIL with file-and-line evidence. Read-only; no mutations, no commits. On the issue backend, `--queue` instead works through every `needs-review` item's open PR or MR and merges the ones that pass. See [review and ship](../usage/review-and-ship.md) for the full flow.

## /rota-capture --remove

Removes a captured backlog item and cleans up its dependencies in one operation, through `rota item rm`. File backend only; on the issue backend close the issue with `rota item complete <ID> --reason dropped` instead. Strips the item's entry from `BACKLOG.md`, removes `Related:` cross-references that point to it from other items, and deletes any matching detail file (`.rota/bugs/`, `.rota/features/`, `.rota/tasks/`) and plan file (`.rota/plans/`). An item active in `status.json` is refused on apply until its stream is dropped with `rota status rm <branch>`.

Dry-run-by-default: the first pass shows what would change, then an explicit `AskUserQuestion` confirmation gate must be cleared before any writes happen. `ARCHIVE.md` is preserved by default as the historical record; pass `--scrub-archive` to also remove an archived entry and its cross-references there. Accepts one or more comma-separated IDs (e.g. `B07,F03`). If any ID is unknown, the verb exits 3 before any write. See [removing work](../usage/removing-work.md) for the full flow.

## /rota-ship

Finishes a feature branch: runs `/rota-review` (by default), builds a PR body from commit subjects and resolved item IDs, then either opens a PR (GitHub) or MR (GitLab) or merges directly based on `work.mergeStrategy`. Clears the `status.json` entry and closes referenced items on completion. On the issue backend it always opens a PR or MR with `Closes #N` lines and never merges; the merge closes the issues ([issue backend](../usage/issue-backend.md)). See [review and ship](../usage/review-and-ship.md).

Two modes folded in via flags:

- `/rota-ship --undo`: guided rollback of the last `/rota-work` cycle on the base branch. Resets the merge commit and restores the cycle's TODO entries to their type sections in `BACKLOG.md`. Direct-merge cycles only (MVP); PR-mode cycles refused with a manual-recovery pointer. Refuses on cycles that have post-merge commits unless `--allow-post-merge` is passed. Defaults to a dry-run preview; the slash command always asks for explicit confirmation before applying. See [rolling back a cycle](../usage/undo.md) for the full flow.
- `/rota-ship --docs`: public-docs maintainer. Scaffolds `<docs.path>/` on first run (discovery + tailored tree + interactive approval), proposes doc updates in after-work mode, or audits and reorganizes in restructure mode (`/rota-ship --docs restructure`). Auto-fires inline at ship time when `docs.afterWork: true` and the post-cycle trigger condition matches.

## /rota-spike

Throwaway feasibility experiment on a dedicated `spike/<name>` branch that is never merged. Answers a specific yes/no/conditional question and records question, what was tried, findings, and decision in `.rota/spikes/<name>.md`. See [spikes](../usage/spikes.md).

## /rota-vision

Brainstorms a project's bigger vision and breaks it into milestones with explicit dependencies and ready/blocked status, writing `MILESTONES.md` and per-milestone detail files under `.rota/milestones/` (on the issue backend each milestone is also a tracker milestone with a tracking issue). Uses Socratic discovery, web research, and deliberate challenge before proposing milestones. See [vision and plans](../usage/vision-and-plans.md) for the full flow.

## /rota-work

Orchestrated parallel implementation: the orchestrator plans tasks and dispatches worker subagents to implement them, each on a feature branch or isolated worktree. With no item argument it first reconciles the backlog against git state (`rota status show`, `rota backlog drift`, `rota backlog list`, `rota milestone active`), reads any `/rota-pause` handoff note, and suggests the next item. Consults `KNOWLEDGE.md` and any matching `.rota/plans/<key>.md` at the start, then registers progress in `status.json` throughout. See [running work](../usage/running-work.md) for the full flow.
