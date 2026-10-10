# Issue-mode milestones and umbrella repos (Steps 3 and 12)

## Step 3 — Milestone Gate (issue mode)

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`): pick the milestone. `--milestone MNN` wins; else the single one from `rota milestone active`. Several active: ask the user. Then `rota release milestone-check <MNN> --json`.

Exit 1 means blocked: show each `data.blocked` entry (open issues labelled `in-progress`, `needs-review` or `changes-requested`) and stop. `data.stillOpen` entries do not block; show them and continue. Other exits (2, 3, 4, 5, 6): stop and report the verb's message.

**Umbrella** (`rota repo umbrella` exits 0): releases run per sub-repo. Pass `--repo <name>` to `release milestone-check`, `release notes --from issues` and `release close-milestone`; without it they exit 2. The milestone reads `shipped` only once every sub-repo's milestone MNN is closed.

## Step 12 — Close Out the Milestone (issue mode)

After Steps 10 and 11, `rota release close-milestone <MNN> --release <new_version> [--repo <name>]` (bare `X.Y.Z`). It marks the milestone's completed issues `released`, closes the milestone and sets it `shipped`; tell the user `data.issues`. Exit 2-6: report the message. Skipped in `--dry-run`. Step 13 does not apply: the tracker is the backlog, nothing is imported.
