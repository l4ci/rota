# Isolation patterns

Used by `/rota-work` Step 5 (the single primary consumer today). The reference enumerates the 4 isolation patterns + the umbrella-mode worktree variant, plus the isolation-guard contract that fires when ≥2 commit-producing parallel workers race on a shared `.git/index`.

`/rota-work` isolates each cycle from main by creating a feature branch or a separate worktree; the choice depends on `work.isolation` in `.rota/config.json` (`"branch"` or `"worktree"`).

## Decision table

| Scope | Isolation | Pattern |
|---|---|---|
| Single-repo | branch | `git checkout -b <branch>` + `rota status add <branch> --items <items>` |
| Single-repo | worktree | `git branch <branch>` + `git worktree add .claude/worktrees/<branch>` + `rota status add <branch> --items <items> --worktree <path>` |
| Umbrella (sub-repo) | branch | `(cd <repo> && git checkout -b <branch>)` + `rota status add <branch> --items <items> --repo <repo>` |
| Umbrella (sub-repo) | worktree (Layout B) | `(cd <repo> && git branch <branch>)`, `rota git worktree-path <branch> --repo <repo>`, `git -C <repo> worktree add "$WT" <branch>`, `rota status add <branch> --items <items> --worktree "$WT" --repo <repo>` |
| Umbrella (multi-repo) | branch | `rota git branch <branch> --repos <csv>` + `rota status add <branch> --items <ids-csv> --repos <csv>` |

`rota git branch` precheck refuses ALL repos if the branch exists in ANY one — no partial creation. Multi-repo workers are safe under either isolation mode (see *Cross-repo parallelism* below).

## Per-pattern code

The table is the contract; these are the invocations spelled out for the worker brief.

**Single-repo, branch:**

```bash
git checkout -b <branch>
rota status add <branch> --items <ID>[,<ID>...]
```

**Single-repo, worktree:**

```bash
git branch <branch>
git worktree add .claude/worktrees/<branch> <branch>
rota status add <branch> --items <ID>[,<ID>...] --worktree .claude/worktrees/<branch>
```

**Umbrella sub-repo, Layout B worktree:**

```bash
(cd <repo> && git branch <branch>)
WT=$(rota git worktree-path <branch> --repo <repo>)
git -C <repo> worktree add "$WT" <branch>
rota status add <branch> --items <ID>[,<ID>...] --worktree "$WT" --repo <repo>
```

Full umbrella branch-creation ceremony (single + multi) lives in `references/umbrella-mode.md`; do not duplicate it here.

## Isolation guard — when it fires

Under the F11 default (write-only workers, orchestrator commits), the guard is defense-in-depth — workers don't write index entries, so the race condition that motivated the guard is contained at the architectural level. The guard still fires fatally when:

- `≥2` commit-producing parallel workers are dispatched, AND
- `work.isolation == "branch"`, AND
- they share one `.git/index` (i.e. they target the same sub-repo, or no umbrella).

The 2026-05-02 DECISIONS entry (*Isolation guard for parallel branch-isolated commit-producing workers*) is the canonical statement — rule, *Why*, **Forbids**, **Permits**. Consult that entry before relaxing or reshaping the guard.

## Cross-repo parallelism is safe by construction

Multi-repo workers under branch isolation are safe because each sub-repo has its own `.git/index`. Two workers committing simultaneously into `web/.git` and `api/.git` cannot race the way two workers committing into a single `.git/index` can. The guard scopes to within-one-repo by design — it intentionally does NOT block cross-repo parallel commits.

This is why multi-repo waves dispatched by `/rota-work` (one branch, N sub-repos, M workers) run in parallel under either isolation mode without further accommodation.

## Umbrella mechanics

This reference covers only the isolation-and-worktree-creation aspects of umbrella mode. The broader umbrella concept — the registry (`.rota/repos.json`), resolution verbs (`rota repo which`, `rota repo resolve`), the `Repos:` field on TODO items, walk-up convenience, merge/PR `--repo` plumbing — lives in `references/umbrella-mode.md`. Cite it from call sites that need both halves.

## What this reference does NOT cover

- **The tmux worker backend** (`work.dispatch: "tmux"`) — see `references/tmux-dispatch.md`. That path does not use these patterns at all: `rota worker pool` owns one worktree per slot, and `work.isolation` stops applying because every slot has its own index by construction.
- **The umbrella-mode concept, registry, and resolution verbs** — see `references/umbrella-mode.md`.
- **Worker dispatch under each isolation mode** (Skill-tool shape, parallel batching, worker-brief construction) — see `/rota-work` Step 6 inline.
- **The full F11 write-only-workers default** (why workers don't commit, how the orchestrator collects diffs and commits) — see `KNOWLEDGE.md` 2026-05-07 entry.
