# Architecture

Everything Claude reads or mutates lives under `.rota/` in your project. Git is the source of truth; `status.json` is just a cache, and `/rota-work` (no argument) reconciles drift between the two whenever it runs. The layout below is the file backend (`backlog.backend: "file"`, the default). Under `"issues"` the backlog, milestones and item designs and plans live on GitHub or GitLab instead; see [issue backend](../usage/issue-backend.md).

## `.rota/` layout

```
.rota/
├── BACKLOG.md        # file backend: bugs, features, tasks, recent completions
├── KNOWLEDGE.md      # durable learnings, grouped by topic
├── DECISIONS.md      # hard-boundary decisions with explicit forbids/permits
├── MILESTONES.md     # milestone overview (vision paragraph as intro)
├── ARCHIVE.md        # completions older than 5 days
├── counters.json     # auto-incrementing IDs
├── config.json       # models, isolation, merge, verify, umbrella
├── status.json       # active work streams (keyed by branch, or (branch, repo) in umbrella mode)
├── repos.json        # umbrella mode only — registered sub-repos
├── workers.json      # round state: slots, escalations, usage-limit log (gitignored)
├── verdicts.json     # recorded review, second-opinion and QA verdicts (gitignored)
├── bugs/ features/ tasks/   # overflow detail files (file backend)
├── designs/          # /rota-brainstorm output (<ID>.md, file backend)
├── milestones/       # one detail file per milestone (M01.md, M02.md, ...)
├── plans/            # /rota-plan output (M01-S01.md slice plans, M01-B07.md item plans)
├── spikes/           # /rota-spike findings — one file per spike, branch lives in git
└── handoff/          # /rota-pause notes, and the orchestrator's handoff (<base>.md)
```

`rota` verbs collapse multi-step agent logic into single subprocess calls. Per-invocation context stays smaller and the output format stays consistent. In umbrella mode the same `.rota/` lives at the umbrella root and coordinates work across sub-repos; see [umbrella mode](../usage/umbrella-mode.md).

## Round state outside `.rota/`

A [parallel round](../usage/parallel-rounds.md) keeps its per-repo state in the git common directory (`git rev-parse --git-common-dir`, which is `.git` in a plain checkout). Every worktree of the repo shares it, so the orchestrator and every worker see the same files, and none of it is tracked. It lives under `<git-common-dir>/rota/`:

```
<git-common-dir>/rota/
├── round-lease.json     # who the orchestrator is: pid, start time, pane, round number
├── session/<id>.json    # one per Claude session: context %, rate limits, last refresh
├── keepalive.json       # the keepalive supervisor's state (rota keepalive run)
├── limit-watch.json     # the running usage-limit watcher, if any
└── codex/<slot>/        # state of each Codex worker slot (its prompt key)
```

| File | Written by | Read by |
|---|---|---|
| `round-lease.json` | `rota round start` (or the supervisor); released by `rota round wind-down`, cleared when stale by `rota reap --kind lease` | every round verb, the hooks, `rota keepalive status` |
| `session/<id>.json` | the statusline (`rota statusline dump`) on every refresh; files idle for 24 hours are dropped by the next dump | `rota hook stop`, `rota limit watch` |
| `keepalive.json` | `rota keepalive run` on every transition | `rota keepalive status`, the Stop hook (the usage hold) |
| `limit-watch.json` | `rota limit watch`, or the supervisor's own watcher | `rota limit status`, a second `watch` (to refuse it) |
| `codex/<slot>/` | `rota round assign --kind codex` | `rota worker prompt-check`, relays to the slot |

The lease is what makes a session "the orchestrator". The Stop and SessionStart hooks act only for the session that holds it, so installing them wide leaves workers alone. Under `rota keepalive run` the supervisor holds the lease for its whole life, across restarts, and hands the pid to the orchestrator it starts through `ROTA_ROUND_HOLDER_PID`.

The slot state directories sit outside `.worktrees/`, so `rota reap` never sees them. Codex workers log in through the default Codex home (or a `work.codexAccounts` home), never a per-slot one.

The registry the round writes inside the project is `.rota/workers.json`: see [`.rota/` folder](rota-folder.md#workersjson-round-state). The orchestrator's handoff is `.rota/handoff/<base>.md`, delivered to the next session by the SessionStart hook.

## Drift detection

`rota version --drift` compares the project's recorded `rota.version` against the installed binary. On drift, rerun `rota init` to re-stamp the project. The pre-rename `hvSkills.version` is read as a fallback and moved by `rota init` / `rota config fill`.

## Related

- [How rota works](../how-it-works.md): system diagram and lane overview
- [Slash commands](slash-commands.md): every `/rota-*` command
- [`rota` verb reference](cli-helpers.md): every verb
- [`.rota/` folder reference](rota-folder.md): per-file detail
