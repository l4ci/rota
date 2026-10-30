---
skill: rota-ship
lane: ship
order: 55
situation: Done
summary: Review, then open the PR or merge, linking items by ID.
not_when:
  - use: rota-review
    when: you want a verdict only and nothing integrated
  - use: rota-release
    when: you are cutting a tagged version, not landing a branch
before: [rota-review, rota-work]
after: [rota-release]
manual_only: false
flags:
  - flag: "--undo"
    does: guided rollback of the last cycle on the base branch; dry-run preview first, asks before applying
  - flag: "--docs"
    does: deprecated alias for /rota-docs
example:
  source: getting-started.md
  reviewed: false
  steps:
    - you: /rota-ship
    - agent: |
        /rota-review: PASS
        PR body built from commit subjects and resolved IDs: B03
        Merged into main with --no-ff; branch deleted.
  changes:
    - "B03 under ## Completed in BACKLOG.md, stamped with the fix commit's hash"
    - main carries the fix as one revertable unit
    - status.json entry cleared
---
# /rota-ship

Finishes a feature branch: runs `/rota-review` (by default), builds a PR body from commit subjects and resolved item IDs, then either opens a PR (GitHub) or MR (GitLab) or merges directly based on `work.mergeStrategy`. `rota ship finish` clears the `status.json` entry, closes referenced items on completion and reports the learn, docs and release nudges. On the issue backend it always opens a PR or MR with `Closes #N` lines and never merges; the merge closes the issues ([issue backend](../usage/issue-backend.md)). See [review and ship](../usage/review-and-ship.md).

`--undo` is the one flag mode:

- `/rota-ship --undo`: guided rollback of the last `/rota-work` cycle on the base branch. Resets the merge commit and restores the cycle's TODO entries to their type sections in `BACKLOG.md`. Direct-merge cycles only (MVP); PR-mode cycles refused with a manual-recovery pointer. Refuses on cycles that have post-merge commits unless `--allow-post-merge` is passed. It defaults to a dry-run preview and always asks for explicit confirmation before applying. See [rolling back a cycle](../usage/undo.md) for the full flow.
