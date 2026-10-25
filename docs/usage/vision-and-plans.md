# Vision and plans

rota supports planning above the day-to-day backlog. `/rota-vision` frames milestones so the project has a clear direction. `/rota-plan` locks an implementation approach for a slice or item before code lands. Together they keep the orchestrator executing your written intent instead of decomposing ad-hoc from an empty context.

## /rota-vision: brainstorm milestones

`/rota-vision` is a brainstorming skill. It runs Socratic discovery (a couple of questions tailored to whether you are creating a roadmap from scratch or editing an existing one), optionally pulls grounded findings from web research (it asks first), pushes back on your scope and ordering, and proposes milestones with explicit dependencies. You iterate until the breakdown feels right.

Before it asks anything, `/rota-vision` looks for a concept, spec or PRD markdown file in the repo (or a README that is mostly a plan) that no milestone covers yet. When it finds one it says so and offers to seed milestones from it, with title, goal, acceptance and dependencies drawn from the document; you confirm before anything is written. No flag is needed. Each seeded plan carries a `Source: <path>#<heading>` line, so a later run knows the document is covered and proposes only what existing milestones do not. With no such document and no milestones, it runs the interview described above.

When the session ends, `/rota-vision` writes two things:

- `.rota/MILESTONES.md`: a short vision paragraph and the Active list, regenerated with `rota milestone index`.
- One plan per milestone with the goal, capabilities, acceptance criteria, rationale and risks. On the file backend that is `.rota/milestones/M01.md`, `M02.md`, …; on the [issue backend](issue-backend.md) it is the body of the milestone's tracking issue next to a native tracker milestone.

Run `/rota-vision` whenever the conversation is about strategy rather than tactics: *"plan the next quarter"*, *"what's the bigger picture"*, *"create a roadmap"*, *"brainstorm milestones"*. Re-running it on a project that already has milestones enters edit mode automatically.

## Milestones: the four statuses

Each milestone carries one of four statuses:

| Status | Meaning |
|--------|---------|
| `planned` | Not yet started; waiting on a dependency or just queued |
| `active` | In flight; work is happening against this milestone |
| `shipped` | Complete; unblocks any milestone that lists it as a dependency |
| `archived` | Abandoned or superseded; does **not** unblock dependents |

Multiple milestones can be `active` simultaneously when their dependencies allow. [`/rota-work` (no argument)](picking-work.md) prefers items tagged to active milestones within each priority and size band, so the active set scopes work without being a hard wall. P0 bugs always jump the queue regardless of milestone, and general-backlog items without a tag still surface.

When an active milestone has no open items remaining, `/rota-work` (no argument) surfaces an empty-active notice so you know the milestone is ready to close. Run `rota milestone status <MID> --to shipped` to flip its status, which immediately unblocks any milestone that listed it as a dependency. Shipping is refused (exit 4, nothing changes) while the milestone has open items or unticked `- [ ]` criteria in its body; the failure lists them, `rota milestone check <MID>` shows the breakdown, and `--force` ships anyway.

Marking a milestone `shipped` immediately unblocks anything that depended on it. Marking it `archived` does not. Use `archived` for milestones you are intentionally dropping, not for ones that finished.

## Milestone branches

On a project with an `origin` remote, each active milestone has its own branch, `milestone/<id>`, where its work collects until the milestone ships. `rota milestone status <MID> --to active` cuts it from the base branch and pushes it, and `--to shipped` deletes it. When the branch exists on origin, `--to archived --branch keep|delete` renames it to `archive/milestone/<id>` or removes it, and `--to planned` deletes it unless it holds commits not on the base.

In a [round](parallel-rounds.md), the milestone's issues go to that branch:

- `rota round assign` cuts the worker's branch from `origin/milestone/<id>` and names the base in the brief. The worker's PR targets the milestone branch, and `rota git base` and `rota ship` follow it.
- Untagged issues, issues of a milestone that is not active, and projects without an `origin` keep the round's base. So does an active milestone whose branch is missing on origin, with a warning that names it.
- If an issue's milestone tag changes after it was assigned, `rota round reconcile --apply` retargets its PR to the new base and tells the worker to merge that base before the PR merges. A milestone that goes active does not move issues already assigned under the same tag.

Land milestone PRs with `rota worker gate <PR> --base milestone/<id>` or `rota worker train <PRs> --base milestone/<id>`. A train on a milestone base first merges the project base into the milestone and tests the result with the members. It pushes that sync merge only when the train passes, and a conflict stops the train with nothing pushed.

## Breaking a milestone into issues

When a milestone or Major feature splits into three or more issues, the pieces need a shared shape, or they drift apart. The milestone's `## Capabilities` lists the contracts it changes, one short name per bullet, tagged `(new)`, `(modified)` or `(removed)`. Its `## Acceptance` criteria are checkboxes, numbered `AC-1`, `AC-2` and so on.

Each child issue carries a `## Change` section. Its first line is `ADDED`, `MODIFIED`, `REMOVED` or `RENAMED`, then the capability. A rename is written `RENAMED <old> -> <new>`, and the check validates the old name. The milestone lists the old name, tagged `(removed)` when nothing else keeps it or `(modified)` otherwise. It also lists the new name, tagged `(new)`, when a sibling issue touches it under that name. A `Serves: M08 AC-2` line cites the milestone criterion the issue delivers. A REMOVED change also needs a `Reason:` and a `Migration:` line.

The issue's `## Touches` copies the capability names. Two siblings that touch one capability then clash in `rota round candidates` without anything more declared, so the orchestrator sees it before assigning both. `## Out of scope` names the siblings, so each worker knows where its slice ends.

```markdown
## Change

MODIFIED rota round candidates
Serves: M08 AC-2

## Acceptance

- [ ] WHEN two open issues touch the same capability THEN only one is offered per round

## Touches

- rota round candidates

## Out of scope

Output format of the candidates list (#861).

## Depends on

#858
```

`rota milestone check M08` holds the breakdown together. It lists criteria no feature serves, which is a warning, or a failure with `--strict`. It also flags features with no `## Change`, a `## Change` whose capability is not listed, citations of criteria the milestone lacks, Touches entries that are not listed capabilities, and REMOVED changes without a Reason or Migration. With fewer than three feature issues it reports that it is below threshold and checks nothing. Bugs and tasks are ignored.

Milestone bodies written before this shape say `## Acceptance criteria`. `rota migrate milestones` previews the rename and the checkbox prefixes, and `--apply` writes them. A second run changes nothing.

`/rota-plan` in slice mode writes this shape, and its confirm list is where you approve it before the issues are filed.

## /rota-plan: write the implementation plan

`/rota-plan` writes an agreed implementation note for a milestone slice or a single backlog item before [`/rota-work`](running-work.md) runs. On the file backend the plan lives at:

- `.rota/plans/M01-S01.md` for a slice of milestone work
- `.rota/plans/M01-B07.md` for a single item that warrants its own plan (a milestone tag is optional; the key is the item ID as given)

With `research.packageCheck` set to `true` (default `false`, see [configuration](configuration.md#researchpackagecheck)), `/rota-plan` also checks each new package it would recommend against its registry, which **makes network calls**. A `missing` package is dropped; a `suspect` one is listed with its reason and enters the plan only after you say yes.

Every task also names the acceptance criteria it delivers (`Serves: AC-1`). On the issue backend, `rota plan check <key>` reads the plan against the item's `## Acceptance` list and exits 1 when a criterion has no task, a task serves no criterion or an unknown one, or a task has no verify step. When the item has `decision` comments, it numbers them D1, D2 oldest first, prints each with its first 60 characters, and also exits 1 when a decision is cited by no task's `Honors: D1, D2` line or a task cites a `D<n>` the item lacks. An item with no decision comment gets no new output. It never writes. It also prints advisory `warn:` lines (and lists them in `data.warnings`) for a plan with more than `plan.maxTasks` tasks (default 3) and for each task whose `Files:` line names more than `plan.maxFilesPerTask` paths (default 5), each with a suggestion to split; they never change the exit code, and `0` turns a warning off (see [plan keys](configuration.md#plan-keys)). `/rota-plan` runs it after saving, and `/rota-work` runs it before it dispatches a plan that has `Serves:` lines. On the file backend it exits 1 with `blockedBy: backend`.

How much planning an item gets is the planning dial. Before it plans, `/rota-plan` (and `/rota-work`, when no plan is stored) rates the item on three questions about a wrong plan: how expensive it is, how hard to undo and how invisible. The ratings pick a level, and the skill says the level in one line. You can override it in a word.

| Level | Adds |
|---|---|
| 1. Pointers | Nothing: the plain plan. |
| 2. Spec | Acceptance written first, the chosen approach and the one you rejected (a `/rota-brainstorm` design). |
| 3. Research round | Open technical questions answered by trying (`/rota-spike`). |
| 4. Adversarial pass | A grilling pass and a critic argue against the finished plan. The critic is a subagent of the planning harness, or a read-only run on another harness when `plan.criticHarness` is set. |

### A critic from another model

A critic from the planner's own model shares its blind spots. Set `plan.criticHarness` to `claude`, `codex` or `opencode` and the level-4 critic runs on that harness instead: headless, read-only, with the same brief and the same inputs (the item and the saved proposal, nothing else). Unset, the critic is the same-harness subagent as before. Levels 1-3 never read the key, and level 3 with a `decision` comment keeps the same-harness critic.

```bash
rota config set plan.criticHarness codex
```

If that harness is not installed or the run fails, `/rota-plan` runs the same-harness critic and prints one line saying so; the critic is never skipped silently. The `## Critic findings` section names the harness that produced the findings. The critic runs once per plan; it does not loop. See [Codex workers](codex-workers.md#a-codex-plan-critic).

When two levels fit, the lower one wins. A round worker never stops on the level: the issue's acceptance criteria are its spec.

On the [issue backend](issue-backend.md) an item plan is a note on the item's issue (key `#42`), and a slice plan lives on the milestone's tracking issue.

Each plan contains: goal in one sentence, approach in 3–6 sentences, tasks with observable behaviors and verify steps, named assumptions, and open questions. Tasks must fit one execution window. If they don't, split the plan. Every task requires a verify step; a task without one is not well-defined. In a plan with two or more tasks, the first is the tracer: the thinnest end-to-end path through every layer the change touches. It runs alone as the first wave. If its verify fails or its RED run does not turn green, `/rota-work` dispatches no other task and reports the failure; a round worker stops with `ROTA-BLOCKED`.

Before the plan is signed off, `/rota-plan` checks doc-by-path deliverables: any task file path containing a `docs/` segment must resolve to an existing doc home in the target repo. Mismatches surface as Open questions instead of biting mid-`/rota-work`. In umbrella mode a sibling `<repo>-docs` sub-repo registered in `.rota/repos.json` is surfaced as the suggested alternative home.

When `/rota-work` starts its planning step, it checks for a matching plan file and uses it as the dispatch source instead of decomposing ad-hoc. `/rota-work` (no argument) suggests running `/rota-plan` for size-Major items that do not have a plan yet. `/rota-vision` offers it alongside [`/rota-capture`](capturing-work.md) when you finish seeding a freshly activated milestone.

On the file backend, after `/rota-work` completes an item that had its own plan (e.g. `M01-B07.md`), the plan file is removed automatically. Once the cycle commits, the plan's task decomposition and assumptions are stale, and leaving the file would confuse a future cycle on the same key. Slice plans (`M01-S01.md`) stay through their multi-item lifetime; remove the slice plan with `rota plan rm M01-S01` once the slice is fully shipped.

## When to use /rota-plan vs skipping it

For small items (Minor or Cosmetic), the overhead of a plan outweighs the benefit. Capture the item with [capturing work](capturing-work.md) and run `/rota-work` directly.

For larger or higher-stakes items, especially size-Major or anything where you and the orchestrator need to agree on the approach before any worker dispatches, `/rota-plan` earns its keep. The cost is a short focused conversation. The payoff is that six weeks from now the orchestrator is executing your written approach, not its own ad-hoc interpretation.

Rough heuristic: if you would want to review the implementation approach before a colleague started coding, write the plan.

## Throwaway feasibility: /rota-spike

When a milestone hinges on a question you cannot answer from the chair (*"can SSE work over our nginx setup?"*, *"is this library's threading model compatible with ours?"*), [`/rota-spike`](spikes.md) runs an experiment on a dedicated branch that never merges. Only the findings come back as a markdown record; experimental code stays on the spike branch as reference. See [spikes](spikes.md) for the full flow.

## Tagging items to milestones

`/rota-capture` tags an item when you name a milestone, or asks once whether to tag when exactly one milestone is active (the default is untagged). With several active milestones and none named, it leaves items untagged. On the file backend an item can carry a `Milestone: M01` field or a comma-separated list (`Milestone: M01, M03`) when work spans milestones; the issue backend takes one milestone.

`/rota-work` (no argument) prefers milestone-tagged items within each priority and size band but general-backlog items still surface. The active milestone set acts as a soft scope rather than a hard filter, so you stay focused without losing sight of the rest of the backlog.

See [capturing work](capturing-work.md) for how items are filed and [running work](running-work.md) for how `/rota-work` (no argument) and `/rota-work` use the milestone tag when dispatching.
