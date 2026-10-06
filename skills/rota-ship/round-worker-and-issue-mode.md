# Round worker and issue-mode branches

Loaded by `SKILL.md` when the branch is a round worker's (`<agent>/<issue>-<slug>`, or the brief says it is a round slot) or the backlog backend is issues (`references/issue-mode.md`). Step labels match `SKILL.md`.

## Round worker skips

- **Step 3 (review):** review is the orchestrator's seat, so a worker never runs `/rota-review` on its own branch (`skills/references/worker-contract.md`). Skip Step 3's routing and treat `REVIEW_CHOICE` as unset.
- **Step 3.5 (second opinion):** skipped; the orchestrator's merge gate is the second check (`docs/contributing/rounds.md`).
- **Step 8 (file mode):** a round worker skips it (workers never edit tracked `.rota/`); the orchestrator completes the items when it merges the PR.
- **Steps 8.5 and 8.6:** skipped; the orchestrator runs learn and docs once per round.

## Issue backend

- **Step 5:** there is no question: go to Step 6a, never direct-merge. If `work.mergeStrategy` is `"direct"`, say so in one line (*"`work.mergeStrategy` is `direct` but the issue backend always opens a PR; ignoring it."*) so the mismatch is visible, then go on.
- **Step 6a:** add `--items <ID1>,<ID2>` (qualified `<repo>:<ID>` in an umbrella) so the PR closes them, then `rota item state <ID> --to needs-review` per item. Do not call `rota item release`: the claim stays until the PR merges. Shipping never merges here. The merge owner is the orchestrator in a round (`rota worker gate`), otherwise whoever runs `/rota-review --queue`. Skip Steps 6b, 6c and 8.
- **Step 6c:** skipped (the tracker issues close when the PR merges).
- **Step 8:** skip. Closing happens at the merge (`rota worker gate` in a round, `rota ship pr-merge` from the queue). Use `rota item complete` only with `--reason handed-off|blocked|dropped`.

## Umbrella

Pass `--repo "$REPO"` to `rota verdict route`, `rota review scope`, `rota review brief`, `rota ship pr|merge` and `rota status rm`. Without it on Step 7 the active entry leaks into the next `/rota-work`.
