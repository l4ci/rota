# Round worker and issue-mode branches

Loaded by `SKILL.md` when the branch is a round worker's (`<agent>/<issue>-<slug>`, or the brief says it is a round slot) or the backlog backend is issues (`references/issue-mode.md`). Step labels match `SKILL.md`.

## Round worker skips

- **Step 3 (review):** review is the orchestrator's seat, so a worker never runs `/rota-review` on its own branch (`skills/references/worker-contract.md`). Skip Step 3's routing and treat `REVIEW_CHOICE` as unset.
- **Step 3.5 (second opinion):** skipped; the orchestrator's merge gate is the second check (`docs/contributing/rounds.md`).
- **Step 3.75 (QA):** skipped; `test.e2e` runs on the merged tree at the gate (`rota worker gate`, or once per `rota worker train`), not per branch.
- **Step 3.8 (UAT):** skipped; round PRs stay unattended and `rota worker gate` never runs it.
- **Step 8 (file mode):** a round worker skips it (workers never edit tracked `.rota/`); the orchestrator completes the items when it merges the PR.
- **Step 8.5:** skipped; the learning pass runs at Step 4 instead (below).
- **Step 8.6 (docs after-work):** runs, commits its edits on the worker branch and ships them in the same PR (`/rota-docs` D-A5 applies the drafts without a picker). Scaffold and restructure never run here.

- **Step 4 (PR body):** run the learning pass of `skills/references/worker-contract.md` first (post each learning with `--kind learning`), then end the body with `Learnings: none` or `Learnings: <n> posted`. This is before Step 6a, so the PR opens with the line already in it.
- **Before the push (Step 6a):** merge the moved base first (`git fetch origin <base>`; when the base is past your merge base, `git merge origin/<base>`, never a rebase), resolve conflicts, re-run the targeted checks and record proof at the merged head. The same holds before every relay-fix push (`skills/references/worker-contract.md`).
- **Before reporting done (issue backend):** follow `skills/references/acceptance-marks.md` for the lead and every open bundle member.

## Issue backend

- **Step 5:** there is no question: go to Step 6a, never direct-merge. If `work.mergeStrategy` is `"direct"`, say so in one line (*"`work.mergeStrategy` is `direct` but the issue backend always opens a PR; ignoring it."*) so the mismatch is visible, then go on.
- **Step 6a:** add `--items <ID1>,<ID2>` (qualified `<repo>:<ID>` in an umbrella) so the PR closes them, then `rota item state <ID> --to needs-review` per item. Do not call `rota item release`: the claim stays until the PR merges. Shipping never merges here. The merge owner is the orchestrator in a round (`rota worker gate`), otherwise whoever runs `/rota-review --queue`. Skip Steps 6b, 6c and 8.
- **Step 6c:** skipped. GitHub and GitLab close the issues when the PR merges. Jira and Linear do not: `rota worker gate` closes the issue the merged PR names, unless it is labelled `partial-slice`.
- **Step 8:** skip. Closing happens at the merge (`rota worker gate` in a round, `rota ship pr-merge` from the queue). On Jira or Linear, a PR merged from the queue leaves its issue open: close it with `rota issues close <KEY> --commit <sha>`. Use `rota item complete` only with `--reason handed-off|blocked|dropped`.

## Umbrella

Pass `--repo "$REPO"` to `rota verdict route`, `rota review scope`, `rota review brief`, `rota ship pr|merge` and `rota status rm`. Without it on Step 7 the active entry leaks into the next `/rota-work`.
