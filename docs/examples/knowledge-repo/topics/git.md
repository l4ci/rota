# Git

- **Worktrees share one object store** — `git worktree add` is cheap, but a `gc` in one worktree affects all of them. <!-- 2026-10-08 source:rota -->
- **Fast-forward only on pull** — `git merge --ff-only @{u}` never rewrites local commits; a diverged branch fails instead of merging. <!-- 2026-09-21 source:web-shop -->
