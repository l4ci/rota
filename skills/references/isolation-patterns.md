# Isolation patterns

Used by `/rota-work` Step 5: the isolation patterns plus the umbrella-mode worktree variant.

`/rota-work` isolates each cycle from main with a feature branch or a separate worktree, per `work.isolation` in `.rota/config.json` (`"branch"` or `"worktree"`).

## Decision table

| Scope | Isolation | Pattern (commands below) |
|---|---|---|
| Single-repo | branch | Single-repo, branch |
| Single-repo | worktree | Single-repo, worktree |
| Umbrella (sub-repo) | branch | Umbrella sub-repo, branch |
| Umbrella (sub-repo) | worktree (Layout B) | Umbrella sub-repo, Layout B worktree |
| Umbrella (multi-repo) | branch | Umbrella multi-repo, branch |

Multi-repo workers are safe under either isolation mode (see *Cross-repo parallelism* below). Umbrella patterns run from the umbrella root (the orchestrator stays there to read and write `.rota/`); workers `cd` into the sub-repo path before any git operation.

## Per-pattern code

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

**Umbrella sub-repo, branch:**

```bash
(cd <repo> && git checkout -b <branch>)
rota status add <branch> --items <ID>[,<ID>...] --repo <repo>
```

**Umbrella sub-repo, Layout B worktree:**

```bash
(cd <repo> && git branch <branch>)
WT=$(rota git worktree-path <branch> --repo <repo>)
git -C <repo> worktree add "$WT" <branch>
rota status add <branch> --items <ID>[,<ID>...] --worktree "$WT" --repo <repo>
```

`rota git worktree-path` produces the canonical Layout B path `<umbrella>/.claude/worktrees/<repo>/<branch>`: use it for both `worktree add` and `rota status add`. `rota ship merge` and `rota ship pr` remove that worktree themselves before they integrate the branch.

**Umbrella multi-repo, branch:**

```bash
rota git branch <branch> --repos <csv>
rota status add <branch> --items <ID>[,<ID>...] --repos <csv>
```

`rota git branch` is atomic: a precheck refuses (exit 4) for ALL repos if the branch exists in ANY one, before any branch is written. Its `--repos` takes no spaces after commas, so drop them from the `Repos:` value first.

## Cross-repo parallelism is safe by construction

Each sub-repo has its own `.git/index`, so multi-repo waves (one branch, N sub-repos, M workers) run in parallel under either isolation mode. Within one repo `/rota-work` workers never stage or commit (the orchestrator commits per task, Step 7.5), so branch isolation has no shared-index race either.
