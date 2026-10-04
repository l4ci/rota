# Isolation patterns

Used by `/rota-work` Step 5. The isolation patterns plus the umbrella-mode worktree variant.

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

## Cross-repo parallelism is safe by construction

Each sub-repo has its own `.git/index`, so multi-repo waves (one branch, N sub-repos, M workers) run in parallel under either isolation mode. Within one repo `/rota-work` workers never stage or commit (the orchestrator commits per task, Step 7.5), so branch isolation has no shared-index race either.

## Umbrella mechanics

This reference covers only the isolation-and-worktree-creation aspects of umbrella mode. The broader umbrella concept — the registry (`.rota/repos.json`), resolution verbs (`rota repo which`, `rota repo resolve`), the `Repos:` field on TODO items, walk-up convenience, merge/PR `--repo` plumbing — lives in `references/umbrella-mode.md`. Cite it from call sites that need both halves.

## Not covered here

- **Rounds.** Standing workers each own a worktree provisioned by `rota round start`; see `/rota-orchestrate`.
- **The umbrella concept, registry and resolution verbs**: `references/umbrella-mode.md`.
- **Worker briefs and dispatch**: `/rota-work` Step 6.
