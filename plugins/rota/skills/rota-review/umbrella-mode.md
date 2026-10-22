# Umbrella mode (Steps 2 and 8)

**Step 2.** When the branch lives in a sub-repo, pass `--repo <name>`: `rota review scope --json --repo <name> <branch>`. Get `<name>` from `data.repo` of `rota status show <branch> --json`, or from `rota repo which` inside the sub-repo's worktree. Backlog lookups stay umbrella-flat; no repo flag needed for intent matching.

**Step 6.** Pass the same flag: `rota review scaffolding --repo <name> --base <base> <branch>`.

**Step 8.** Add `--repo <name>` to both `rota verdict add` calls.
