# Releasing a milestone through its branch (Steps 3b and 12b)

Applies when the milestone has a branch: `git ls-remote --exit-code --heads origin milestone/<MNN>` exits 0. No `origin`, or no such branch: skip this file, the release runs from trunk as a plain one. Never applies to a release with no milestone.

## Step 3b — Merge the branch to the base through a PR

Runs after Step 3 and before Step 4. `<base>` is the project base branch (`git.baseBranch`, else main, master, trunk). The Step 1 guard still holds: clean tree, on `<base>`, HEAD pushed.

`--dry-run`: read-only. Run `git ls-remote` and `git log --oneline origin/<base>..origin/milestone/<MNN>`, print steps 1-7 as what would happen, and run no switch, merge, push or test.

0. **Resume.** After `git fetch origin`, `git merge-base --is-ancestor origin/milestone/<MNN> origin/<base>`: exit 0 means the branch is already merged, so skip to Step 4.
1. **Sync.** `git switch -C milestone/<MNN> origin/milestone/<MNN>` (resets any local copy a stopped run left behind), then `git merge origin/<base>`. Releases that ran earlier are in `<base>`, so this is how the second milestone picks up the first. A conflict: `git merge --abort`, switch back to `<base>`, report the paths and stop; resolving it is a fix PR to the milestone branch.
2. **Gate.** `rota test run full`, then `rota test run e2e` (each skipped when its `test.*` list is empty). On a red run, switch back to `<base>`, show the failing output and stop: nothing is pushed or merged. Say the fix goes in as a PR to `milestone/<MNN>` (`rota round assign`, or a branch off it), then the release is rerun.
3. **Push.** `git push origin milestone/<MNN>` (the sync merge), record `GATED=$(git rev-parse HEAD)`, then `git switch <base>`. Trains keep landing on the branch during a round; this SHA is what the gate and CI judge.
4. **Open the PR.** Forge CLI (`gh pr create` / `glab mr create`), head `milestone/<MNN>`, base `<base>`, title `Merge milestone <MNN>: <title>`, body the `rota release notes --from issues <MNN>` draft. Reuse an open PR for the same head. CI now runs on it.
5. **Wait for CI.** `gh pr checks <pr> --watch` (or the forge equivalent). "No checks reported" can mean checks have not registered yet: retry a few times, 10 s apart. Still none: say the repo reports no CI and continue on the local gate result. Any failure: stop with the failing checks named; nothing merges. Fix as in step 2.
6. **Ask.** Header `"Merge milestone"`, *"CI is green on `<pr-url>` at `<GATED short sha>`. Merge `milestone/<MNN>` into `<base>` with a merge commit?"*, options `Merge (Recommended)` / `Abort release`. Always asks, in every autonomy mode: it publishes to the base branch.
7. **Merge, pinned to the gated head.** `gh pr merge <pr> --merge --match-head-commit "$GATED"` (GitLab: `glab mr merge <mr> --yes --auto-merge=false --sha "$GATED"`; the project must merge with a merge commit, never squash or rebase). The head moved since the gate (the forge refuses on the mismatch): stop, tell the user to rerun, merge nothing. The forge refuses for another reason (protected base, merge commits disabled): report its reason and stop. Then `git pull --ff-only origin <base>` and check `git merge-base --is-ancestor "$GATED" origin/<base>`; a non-zero exit means the forge did not make a merge commit of the gated head: stop and report. `<base>` now gains exactly one merge commit of the branch.

`rota ship pr-merge` is not used here: it merges item PRs and checks item proof, neither pins a head SHA.

Steps 4 to 11b then run on `<base>` as for any release.

## Step 12b — Clean up the branch (after Step 12)

Runs when `milestone/<MNN>` still exists on origin or issues still carry `on-milestone/<MNN>`, so a resumed release cleans up too.

1. **Labels.** Each issue merged into the branch carries `on-milestone/<MNN>`. List them with the forge CLI (`gh issue list --state all --limit 1000 --label on-milestone/<MNN> --json number --jq '.[].number'`; GitLab: `glab issue list --all --per-page 100 --label on-milestone/<MNN>`, paging until empty), then `rota issues label <key> --remove on-milestone/<MNN>` for each. A failure: report the key and continue; the rest are independent.
2. **Branch and status.** `rota milestone status <MNN> --to shipped` marks the milestone shipped and deletes `milestone/<MNN>` on origin. Step 12's `rota release close-milestone` may already have set the status; rerun is a no-op that still deletes the branch. Exit 4 (open child or unticked criterion): show it, ask whether to rerun with `--force`.

Out of scope here: staging or environment branches, and the branch-delete mechanics of status changes (`rota milestone status`).
