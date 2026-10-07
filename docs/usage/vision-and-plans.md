# Vision and plans

rota supports planning above the day-to-day backlog. `/rota-vision` frames milestones so the project has a clear direction. `/rota-plan` locks an implementation approach for a slice or item before code lands. Together they keep the orchestrator executing your written intent instead of decomposing ad-hoc from an empty context.

## /rota-vision: brainstorm milestones

`/rota-vision` is a brainstorming skill. It runs Socratic discovery (a couple of questions tailored to whether you are creating a roadmap from scratch or editing an existing one), optionally pulls grounded findings from web research (it asks first), pushes back on your scope and ordering, and proposes milestones with explicit dependencies. You iterate until the breakdown feels right.

When the session ends, `/rota-vision` writes two things:

- `.rota/MILESTONES.md`: a short vision paragraph and the Active list, regenerated with `rota milestone index`.
- One plan per milestone with the goal, acceptance criteria, rationale and risks. On the file backend that is `.rota/milestones/M01.md`, `M02.md`, …; on the [issue backend](issue-backend.md) it is the body of the milestone's tracking issue next to a native tracker milestone.

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

When an active milestone has no open items remaining, `/rota-work` (no argument) surfaces an empty-active notice so you know the milestone is ready to close. Run `rota milestone status <MID> --to shipped` to flip its status, which immediately unblocks any milestone that listed it as a dependency.

Marking a milestone `shipped` immediately unblocks anything that depended on it. Marking it `archived` does not. Use `archived` for milestones you are intentionally dropping, not for ones that finished.

## /rota-plan: write the implementation plan

`/rota-plan` writes an agreed implementation note for a milestone slice or a single backlog item before [`/rota-work`](running-work.md) runs. On the file backend the plan lives at:

- `.rota/plans/M01-S01.md` for a slice of milestone work
- `.rota/plans/M01-B07.md` for a single item that warrants its own plan (a milestone tag is optional; the key is the item ID as given)

Every task also names the acceptance criteria it delivers (`Serves: AC-1`). On the issue backend, `rota plan check <key>` reads the plan against the item's `## Acceptance` list and exits 1 when a criterion has no task, a task serves no criterion or an unknown one, or a task has no verify step. It never writes. `/rota-plan` runs it after saving, and `/rota-work` runs it before it dispatches a plan that has `Serves:` lines. On the file backend it exits 1 with `blockedBy: backend`.

How much planning an item gets is the planning dial. Before it plans, `/rota-plan` (and `/rota-work`, when no plan is stored) rates the item on three questions about a wrong plan: how expensive it is, how hard to undo and how invisible. The ratings pick a level, and the skill says the level in one line. You can override it in a word.

| Level | Adds |
|---|---|
| 1. Pointers | Nothing: the plain plan. |
| 2. Spec | Acceptance written first, the chosen approach and the one you rejected (a `/rota-brainstorm` design). |
| 3. Research round | Open technical questions answered by trying (`/rota-spike`). |
| 4. Adversarial pass | A grilling pass and a critic subagent argue against the finished plan. |

When two levels fit, the lower one wins. A round worker never stops on the level: the issue's acceptance criteria are its spec.

On the [issue backend](issue-backend.md) an item plan is a note on the item's issue (key `#42`), and a slice plan lives on the milestone's tracking issue.

Each plan contains: goal in one sentence, approach in 3–6 sentences, tasks with observable behaviors and verify steps, named assumptions, and open questions. Tasks must fit one execution window. If they don't, split the plan. Every task requires a verify step; a task without one is not well-defined.

Before the plan is signed off, `/rota-plan` checks doc-by-path deliverables: any task file path containing a `docs/` segment must resolve to an existing doc home in the target repo. Mismatches surface as Open questions instead of biting mid-`/rota-work`. In umbrella mode a sibling `<repo>-docs` sub-repo registered in `.rota/repos.json` is surfaced as the suggested alternative home.

When `/rota-work` starts its planning step, it checks for a matching plan file and uses it as the dispatch source instead of decomposing ad-hoc. `/rota-work` (no argument) suggests running `/rota-plan` for size-Major items that do not have a plan yet. `/rota-vision` offers it alongside [`/rota-capture`](capturing-work.md) when you finish seeding a freshly activated milestone.

On the file backend, after `/rota-work` completes an item that had its own plan (e.g. `M01-B07.md`), the plan file is removed automatically. Once the cycle commits, the plan's task decomposition and assumptions are stale, and leaving the file would confuse a future cycle on the same key. Slice plans (`M01-S01.md`) stay through their multi-item lifetime; remove the slice plan with `rota plan rm M01-S01` once the slice is fully shipped.

## 🎯 When to use /rota-plan vs skipping it

For small items (Minor or Cosmetic), the overhead of a plan outweighs the benefit. Capture the item with [capturing work](capturing-work.md) and run `/rota-work` directly.

For larger or higher-stakes items, especially size-Major or anything where you and the orchestrator need to agree on the approach before any worker dispatches, `/rota-plan` earns its keep. The cost is a short focused conversation. The payoff is that six weeks from now the orchestrator is executing your written approach, not its own ad-hoc interpretation.

Rough heuristic: if you would want to review the implementation approach before a colleague started coding, write the plan.

## Throwaway feasibility: /rota-spike

When a milestone hinges on a question you cannot answer from the chair (*"can SSE work over our nginx setup?"*, *"is this library's threading model compatible with ours?"*), [`/rota-spike`](spikes.md) runs an experiment on a dedicated branch that never merges. Only the findings come back as a markdown record; experimental code stays on the spike branch as reference. See [spikes](spikes.md) for the full flow.

## Tagging items to milestones

`/rota-capture` tags an item when you name a milestone, or asks once whether to tag when exactly one milestone is active (the default is untagged). With several active milestones and none named, it leaves items untagged. On the file backend an item can carry a `Milestone: M01` field or a comma-separated list (`Milestone: M01, M03`) when work spans milestones; the issue backend takes one milestone.

`/rota-work` (no argument) prefers milestone-tagged items within each priority and size band but general-backlog items still surface. The active milestone set acts as a soft scope rather than a hard filter, so you stay focused without losing sight of the rest of the backlog.

See [capturing work](capturing-work.md) for how items are filed and [running work](running-work.md) for how `/rota-work` (no argument) and `/rota-work` use the milestone tag when dispatching.
