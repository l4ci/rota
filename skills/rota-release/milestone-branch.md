# Releasing a milestone through its branch (Steps 3b and 12b)

Applies when the milestone has a branch: `git ls-remote --exit-code --heads origin milestone/<MNN>` exits 0. No `origin`, or no such branch: skip this file, the release runs from trunk as a plain one. Never applies to a release with no milestone.

## Step 3b — Merge the branch to the base through a PR

Runs after Step 3 and before Step 4. `<base>` is the project base branch (`git.baseBranch`, else main, master, trunk). The Step 1 guard still holds: clean tree, on `<base>`, HEAD pushed.

1. **Sync.** `git fetch origin`, `git switch milestone/<MNN>`, `git merge --ff-only origin/milestone/<MNN>`, then `git merge origin/<base>`. Releases that ran earlier are in `<base>`, so this is how the second milestone picks up the first. A conflict: `git merge --abort`, switch back to `<base>`, report the paths and stop; resolving it is a fix PR to the milestone branch.
2. **Gate.** `rota test run full`, then `rota test run e2e` (each skipped when its `test.*` list is empty). On a red run, switch back to `<base>`, show the failing output and stop: nothing is pushed or merged. Say the fix goes in as a PR to `milestone/<MNN>` (`rota round assign`, or a branch off it), then the release is rerun.
3. **Push.** `git push origin milestone/<MNN>` (the sync merge), then `git switch <base>`.
4. **Open the PR.** Forge CLI (`gh pr create` / `glab mr create`), head `milestone/<MNN>`, base `<base>`, title `Merge milestone <MNN>: <title>`, body the `rota release notes --from issues <MNN>` draft. Reuse an open PR for the same head. GitHub Actions or the forge's CI now run on it.
5. **Wait for CI.** `gh pr checks <pr> --watch` (or the forge equivalent). Any failure: stop with the failing checks named; nothing merges. Fix as in step 2.
6. **Ask.** Header `"Merge milestone"`, *"CI is green on `<pr-url>`. Merge `milestone/<MNN>` into `<base>` with a merge commit?"*, options `Merge (Recommended)` / `Abort release`. Always asks, in every autonomy mode: it publishes to the base branch.
7. **Merge.** `gh pr merge <pr> --merge` (`glab mr merge <mr>`; never squash or rebase). Then `git pull --ff-only origin <base>`. `<base>` now gains exactly one merge commit of the branch.

`--dry-run`: run steps 1-2 only up to reading state (`git ls-remote`, `git log --oneline origin/<base>..origin/milestone/<MNN>`), print the rest, merge nothing. Resume after a stop by rerunning: a branch already merged to `<base>` skips to Step 4.

Steps 4 to 11b then run on `<base>` as for any release.

## Step 12b — Clean up the branch (after Step 12)

1. **Labels.** Each issue merged into the branch carries `on-milestone/<MNN>`. List them with the forge CLI (`gh issue list --state all --label on-milestone/<MNN> --json number --jq '.[].number'`), then `rota issues label <key> --remove on-milestone/<MNN>` for each. A failure: report the key and continue; the rest are independent.
2. **Branch and status.** `rota milestone status <MNN> --to shipped` marks the milestone shipped and deletes `milestone/<MNN>` on origin. Step 12's `rota release close-milestone` may already have set the status; rerun is a no-op that still deletes the branch. Exit 4 (open child or unticked criterion): show it, ask whether to rerun with `--force`.

Out of scope here: staging or environment branches, and the branch-delete mechanics of status changes (`rota milestone status`).
