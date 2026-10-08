# The `.rota/` folder

`rota init` creates this folder once per project. Everything inside is Markdown or JSON, and most of it is tracked by default. Only a handful of machine-specific or transient paths are gitignored. Use the skills or `rota` verbs to update tracked content; reach for hand-editing only when investigating or fixing something that drifted.

## Overview

| File | Purpose |
|------|---------|
| `BACKLOG.md` | File-backend backlog: bugs, features, tasks, and recent completions. Under `backlog.backend: "issues"` the tracker holds the backlog instead and this file is unused ([issue backend](../usage/issue-backend.md)) |
| `KNOWLEDGE.md` | Durable learnings grouped by topic: gotchas, conventions, constraints |
| `DECISIONS.md` | Hard-boundary decisions with explicit forbids/permits. Active commitments future work must respect |
| `MILESTONES.md` | Milestone overview: one short section per milestone, with a vision intro paragraph and an active list |
| `MAP.md` + `map/<subsystem>.md` | Project map: AI-facing narratives describing one coherent area each. Source-of-truth for the `## Project Map` block in `CLAUDE.md`. Hand-authored; `touched:` auto-bumped by cycle skills (`/rota-work`, `/rota-debug`). |
| `knowledge-tier.json` | Sidecar for `KNOWLEDGE.md`: each bullet's tier (`provisional`, `confirmed`, `deprecated`) and hit count. Tracked. Written by `rota knowledge`; in umbrella mode each sub-repo keeps its own copy beside its scoped `KNOWLEDGE.md` |
| `issue-map.json` | Maps old file-backlog IDs to issue numbers. Tracked. Written by `rota migrate issues`, which resumes from it after an interrupted run; `rota round candidates` reads it |
| `test-ledger.json` | Known-red tests the merge gate and `rota worker train` may pass over. Tracked, created by hand when the first entry is needed (a missing file means no exclusions); an expired entry fails the gate. Check it with `rota test ledger check` ([format](../usage/configuration.md)) |
| `counters.json` | Auto-incrementing IDs for each item type |
| `config.json` | Model selection, isolation mode, merge strategy, ship/learn/refactor gates, autonomy level (team-shared defaults) |
| `config.local.json` | _(gitignored)_ Per-developer config overrides, deep-merged on top of `config.json` by `rota`. Use for `autonomy.level`, model preferences, or any setting that varies per machine. |
| `status.json` | _(gitignored)_ Active work streams: which items are being worked on, on which branch/worktree (per-developer) |
| `repos.json` | _(gitignored, umbrella mode only)_ Sub-repo registry with absolute paths (machine-specific) |
| `bugs/` | Overflow detail files for large bug reports (file backend) |
| `features/` | Overflow detail files for large feature specs |
| `tasks/` | Overflow detail files for large task descriptions |
| `milestones/` | One detail file per milestone (`M01.md`, `M02.md`, …) with full plan: goal, acceptance, rationale, risks, research findings, notes |
| `designs/` | `/rota-brainstorm` designs, one `<ID>.md` per item (file backend; under the issue backend a note on the issue) |
| `plans/` | Implementation plans keyed by `<milestone>-<unit>.md` (slices: `M01-S01.md`; items: `M01-B07.md`) |
| `spikes/` | Spike findings: one Markdown file per spike. The experimental code lives on the `spike/<name>` git branch and is never merged |
| `handoff/` | _(gitignored)_ `/rota-pause` notes: one file per branch capturing hypothesis, next step, mid-edit files; consumed by `/rota-work` (no argument). `handoff/<base>.md` is also where a round's orchestrator writes its handoff before a restart. Per-developer scratch. |
| `review/` | _(gitignored)_ `rota review package` output: one `<branch>.md` per branch with its commits, `--stat` and full diff, read by the `/rota-review` reviewer. Regenerated on every call |
| `qa-runs/` | _(gitignored)_ Timestamped `/rota-qa` run artifacts. Bulky, regeneratable from the strategy in `qa/<target>.md` |
| `verdicts.json` | _(gitignored)_ Typed review, second-opinion, QA and debug verdicts (`rota verdict add`, `rota debug verdict`). Per-developer: `/rota-ship` routes on it. |
| `gate-audit.jsonl` | _(gitignored)_ One JSON line per manual gate a human cleared: gate, verb, target, time, the quoted answer and the autonomy level. Written by the gated `rota` verbs (`rota gate list`). |
| `ledger.jsonl` | _(gitignored)_ The round event log, one JSON line per event: `ts`, `kind` (`assign`, `done`, `blocked`, `bounce`, `transfer`, `pick`, `park`, `gate`, `merge`, `limited`), `round` (the held lease's round, read from the git common dir by any process; 0 when no lease is held), and when known `issue`, `slot`, `account`, `harness`, `pr` and a `detail` object (`headroom` on assign and done, `verdict` on gate, `resetsAt` on limited). Appended by the round and worker verbs (a write that fails prints one line to stderr and never fails the verb), read by `rota round summary`. Per-developer, never trimmed. |
| `train-cache.json` | _(gitignored)_ `rota worker train` verdict cache: each verify keyed by tier, base sha and ordered member heads, plus members earlier named culprit. Per-developer, regenerated by the next train. |
| `workers.json` | _(gitignored)_ Round state ([details below](#workersjson-round-state)): the worker slots, escalations, the usage-limit log and the round's host and scope. Written by `rota round` and `rota worker`. Per-developer runtime state. |
| `RELEASE.md` | Release checklist: `- [ ]` items `/rota-release` walks as gates before bumping version. Tracked, shared with the team. |
| `ARCHIVE.md` | Completed items older than 5 days, moved here automatically |

## BACKLOG.md: active backlog

On the file backend (`backlog.backend: "file"`, the default) `BACKLOG.md` is the source of truth for everything in flight. It holds open bugs, features, and tasks organised by type, plus a "recently completed" section at the bottom. [`/rota-capture`](../usage/capturing-work.md) appends new items, and [`/rota-work` (no argument)](../usage/picking-work.md) reads it to suggest what to work on next. With `backlog.backend: "issues"` the open issues on GitHub or GitLab are the backlog and `BACKLOG.md` stays unused; see [issue backend](../usage/issue-backend.md).

A typical entry looks like:

```
- [ ] B03: login redirect loops after OAuth token refresh
```

Edit this file by hand whenever you want: reorder items, bump priorities, or delete things no longer relevant. The skills re-read it on every invocation, so any manual change takes effect immediately.

## KNOWLEDGE.md: durable learnings + glossary

`KNOWLEDGE.md` stores durable project knowledge: gotchas, team conventions, architectural constraints, and anything else you don't want to rediscover later. Entries sit under free-form topic headings. [`/rota-learn`](../usage/learning.md) appends new learnings at the end of a session.

One topic is special-cased: `## Glossary` holds domain-terminology entries as nested bullets (`- **<term>** — <definition>` with indented `**Aliases:**` / optional `**Not:**` / date stamp). The Glossary topic is pinned at `rota init` time; entries are written via `/rota-learn --term <name>` (`rota glossary write`) and read via `rota glossary read <term>`. The F03 tier lifecycle skips Glossary, since terms are canonical, not probationary.

See [../usage/learning.md](../usage/learning.md) for how to capture and review knowledge.

`rota init` inserts a managed block in `CLAUDE.md` (or `AGENTS.md` when present) that lists the current topics (`Glossary` surfaces here like any other topic). That block keeps knowledge visible to the model across context clears without re-reading the full file.

## DECISIONS.md: hard-boundary decisions

`DECISIONS.md` records hard boundaries the project has committed to. It is the sibling of `KNOWLEDGE.md`, but where knowledge is passive (gotchas, conventions), decisions are active commitments with explicit `forbids:` and `permits:` clauses. [`/rota-decide`](../usage/decisions.md) writes new entries; [`/rota-work`](../usage/running-work.md), [`/rota-debug`](../usage/debugging.md), [`/rota-plan`](../usage/vision-and-plans.md), [`/rota-refactor`](slash-commands.md#rota-refactor), [`/rota-review`](../usage/review-and-ship.md), and [`/rota-vision`](../usage/vision-and-plans.md) consult them as constraints.

A companion managed block in `CLAUDE.md` lists the current decision topics so the model can pull only the relevant entries on demand.

See [../usage/decisions.md](../usage/decisions.md) for the full capture flow and the difference between decisions and learnings.

## MILESTONES.md: milestone overview

`MILESTONES.md` lists the milestones, each with a one-paragraph overview and status, opened by a short vision intro paragraph as preamble. `/rota-vision` writes the initial version, and you update it as the project evolves.

See [../usage/vision-and-plans.md](../usage/vision-and-plans.md) for how milestones work with planning and implementation skills.

A companion managed block in `CLAUDE.md` lists active milestones so `/rota-work` (no argument) and [`/rota-pause`](../usage/pausing-and-resuming.md) can scope their suggestions to what is in progress.

## MAP.md: project map

`MAP.md` is an AI-facing index of project subsystems. It holds a brief summary for each named area; full narratives live in `map/<subsystem>.md` and are loaded on demand via `rota map query <name>`. Write `.rota/map/<name>.md` files by hand as you discover subsystems: one file per coherent area, with `subsystem:`/`summary:`/`touched:` frontmatter and free-form body sections (Purpose, Entry points, Key files / dirs, Conventions, Notes / gotchas). Cycle skills (`/rota-work`, `/rota-debug`) bump `touched:` post-cycle when their changes overlap a subsystem's key files or entry points, and regenerate the always-on `## Project Map` block in `CLAUDE.md` via `rota map index`. When subsystems drift or duplicate, edit or retire `.rota/map/<name>.md` entries directly; the index verb picks up the change on the next run.

A managed `## Project Map` block in `CLAUDE.md` surfaces the thin summary so the model can orient without loading detail files.

## counters.json: auto-incrementing IDs

`counters.json` tracks the highest ID assigned for each item type so that IDs never collide across sessions.

```json
{ "bugs": 3, "features": 7, "tasks": 12, "milestones": 2, "since_refactor": { "features": 1, "bugs": 0 } }
```

You should not need to edit this by hand. If you ever manually delete items from `BACKLOG.md`, the counters are safe to leave as-is; IDs are never reused.

## config.json: settings

`config.json` stores project-level preferences: which model to use, whether branch isolation is on, how merges are handled, the ship/learn/refactor gate thresholds, and the autonomy level for orchestration.

See [../usage/configuration.md](../usage/configuration.md) for the full list of options and how to change them with `rota config set`.

## status.json: active work streams

`status.json` records which items are currently being worked on and which git branch or worktree each one lives in. It is written when work starts and cleared when work completes.

See [../usage/picking-work.md](../usage/picking-work.md) for how `/rota-work` (no argument) uses this file to orient the model after a context clear.

## bugs/, features/, tasks/: overflow detail files

When a bug report, feature spec, or task description is too long to fit inline in `BACKLOG.md`, the overflow content goes into a separate file in the matching subdirectory (e.g. `bugs/B03.md`). The `BACKLOG.md` entry links to it. This keeps `BACKLOG.md` scannable while preserving full detail.

Create these files by hand, or let `/rota-capture` handle it when you supply a long description.

## milestones/: per-milestone plans

Each milestone gets its own detail file (`milestones/M01.md`, `milestones/M02.md`, …) containing the full plan: goal, acceptance criteria, rationale, risks, research findings, and working notes. `/rota-vision` creates an initial file for each milestone it defines.

## plans/: implementation plans

`plans/` holds the output of `/rota-plan`: one Markdown file per planning unit, named after the milestone and item it covers (`M01-S01.md` for a slice, `M01-B07.md` for a specific bug). Plans are consumed by `/rota-work` when it orchestrates implementation.

See [../usage/vision-and-plans.md](../usage/vision-and-plans.md) for the full planning workflow.

## spikes/: feasibility findings

`spikes/` stores the written findings from [`/rota-spike`](../usage/spikes.md) runs: one Markdown file per spike summarising what was learned, what was tried, and what the recommendation is. The throwaway experimental code lives on its own `spike/<name>` git branch and is never merged.

## handoff/: pause notes

When you run `/rota-pause`, the current state of the session (active hypothesis, next planned step, files mid-edit, gotchas just discovered, uncommitted-work strategy) is written to `handoff/<branch>.md`. `/rota-work` (no argument) reads any matching note for an active branch and uses it to restore intent that pure git state can't carry across `/clear` or a fresh session.

Notes are scoped per branch and overwritten by subsequent `/rota-pause` runs on the same branch. `rota status rm <branch>` deletes the note when the stream ends.

See [../usage/pausing-and-resuming.md](../usage/pausing-and-resuming.md) for the pause/resume flow.

## workers.json: round state

`workers.json` is the registry behind [parallel rounds](../usage/parallel-rounds.md). `rota round` and `rota worker` read and write it under a lock; you should not need to edit it. Top-level keys:

| Key | Holds |
|---|---|
| `slots` | one entry per roster slot: `name`, `branch`, `worktree`, `base`, `state`, the issue it holds (`task`) and its claim (`claimId`), `pr`, the host handle, the account (`configDir`, `account`), the tier and the harness kind |
| `escalations` | questions put to you with `rota round escalate send`: id (`e1`, `e2`, ...), the issue or PR thread, `pending` or `answered`, and the answer |
| `limits` | the usage-limit log: one entry per limit, with the session, the reset time, whether it sleeps or switches, and its status |
| `host` | `herdr`, `tmux` or `solo`, fixed by `rota round start` for the life of the round |
| `round`, `scope`, `slate` | the round number, the scope it started with, and the issue IDs for a `slate` scope |

`rota round status` and `rota round reconcile` show what it holds against the host, git and the tracker. A slot that is gone from the host but still registered is drift; `reconcile --apply` repairs the safe kinds (including parking a slot whose PR already merged, so `watch` stops looping on it) and `rota reap` removes what nothing owns.

### Beside `.rota/`: the lease, session files and keepalive.json

A few round files do not live in `.rota/`. They sit in `<git-common-dir>/rota/` so every worktree of the repo shares them: the round lease (`round-lease.json`), one session file per Claude session (`session/<id>.json`), the keepalive supervisor's state (`keepalive.json`), the usage-limit watcher's marker (`limit-watch.json`) and one state directory per Codex slot (`codex/<slot>/`, its prompt key). None is tracked and `rota init` adds no ignore line for them. See [architecture](architecture.md#round-state-outside-rota).

The orchestrator hooks `rota hook install` writes are not in `.rota/` either: by default they go to `.claude/settings.local.json`, which Claude Code treats as per-developer.

## ARCHIVE.md: old completions

Completed items are moved from `BACKLOG.md` to `ARCHIVE.md` automatically after they have been in the completed section for more than five days. This keeps `BACKLOG.md` short without losing history. You can read `ARCHIVE.md` at any time; no skill reads it during normal operation.

## What's tracked and what's gitignored

The backlog is shared by default: state travels with the repo so collaborators see the same `BACKLOG.md`, learn from the same `KNOWLEDGE.md`, and respect the same `DECISIONS.md`. A few paths stay gitignored because they're machine-specific, per-developer, transient, or regenerated on demand:

| Path | Why ignored |
|------|-------------|
| `.rota/status.json` | Per-developer active-work tracking, branch-aware |
| `.rota/repos.json` | Umbrella sub-repo registry with absolute paths (machine-specific) |
| `.rota/config.local.json` | Per-developer config overrides (see below) |
| `.rota/handoff/` | Per-developer `/rota-pause` scratch notes |
| `.rota/review/` | Per-branch review packages (commits, stat, diff); regenerated by `rota review package` |
| `.rota/qa-runs/` | Bulky timestamped `/rota-qa` artifacts; regeneratable |
| `.rota/verdicts.json` | Per-developer recorded verdicts that `/rota-ship` and `/rota-debug` route on |
| `.rota/gate-audit.jsonl` | Per-developer log of manual-gate approvals; tracked, it would dirty the base branch on every merge or release push |
| `.rota/ledger.jsonl` | Per-developer round event log; machine-local timings and account headroom |
| `.rota/train-cache.json` | Per-developer merge train verdict cache; keyed by local shas, so tracked it would only churn |
| `.rota/workers.json` | Per-developer worker slot registry (tab handles, account config dirs, claims); machine-specific |
| `.rota/**/*.lock` | Transient advisory lockfiles guarding sidecar read-modify-write |

`rota init` writes these under a `# ── rota ──` header in your project's `.gitignore`. It also adds `.worktrees/` once: worker worktrees (worker-pool slots and parallel rounds) live in `<project>/.worktrees/<name>`, and a nested checkout must stay out of `git status`.

### `config.local.json`: per-developer overrides

Drop a JSON file at `.rota/config.local.json` to override any setting from `.rota/config.json` for your machine only. `rota` deep-merges it on top of the shared config: nested keys merge recursively; scalars and arrays replace. Example:

```json
{
  "autonomy": { "level": "off" },
  "models": { "worker": "haiku" }
}
```

This overrides `autonomy.level` and `models.worker` while inheriting every other key (including `models.orchestrator`) from the shared `config.json`. Typical uses: per-developer autonomy preferences, model selection for cost or latency, or experimenting with a setting before committing it to the team default.

### Opting out: fully private backlog

If you'd rather keep the whole `.rota/` folder private (solo development, or experimentation that isn't ready to share), add a blanket `.rota/` line to `.gitignore` before your first commit. The pattern is supported but is no longer the default.
