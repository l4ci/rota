# Merge train and approval policy

Loaded by `skills/rota-orchestrate/SKILL.md` section 6 when several PRs wait, or when `ship.mergeApproval` requires approval.

With several PRs waiting, merge them as one train: `rota worker train <slot|PR>... --base <branch>` in landing order. One verify covers all of them. On a red train it names the `culprit`: send that PR back, then re-run the train without it, or pass `--land-green` to land the members that verified before it. `base-moved` means nothing landed; re-run.

Merge policy comes from config (`ship.mergeApproval`). With the default, the gate merges a passing PR. When policy requires approval (all PRs, or PRs touching listed paths):

- **Unattended:** pass `--escalate` to `worker gate`, or to `ship pr-merge` for a PR you merge by number. The verb refuses with exit 4 and posts the approval request on the PR thread (or the slot's issue) with `rota round escalate send`, once however often you re-gate; `data.escalation.id` names it. Note the id against the slot and keep working other slots. `rota ship merge` has no thread and keeps `--confirm`.
- **Reading the answer:** `rota round escalate check` says when the maintainer has answered; re-run with `--approval <id>`, and the audit line quotes the answer.
- **Declined:** an answer that doesn't read as approval (`approve`, `approved`, `yes`, `lgtm`, `ship it`) holds the merge. `approval declined` means the slot is held, never retried, and you tell the maintainer with `data.answer`; `approval pending` means `check` has not seen an answer yet. The verb audits the reply verbatim.
- **Interactive:** ask the maintainer in prose (no blocking picker, see section 3) and pass `--confirm --confirm-note` with the answer verbatim. Never write a note the human didn't say.

**Completing items.** On the issue backend the merge closes the issue (`Closes #N`); do nothing more. In file mode you complete the PR's items after the gate merges it, the way `/rota-ship` Step 8 does (proof row first, then complete with the merge sha): workers never edit tracked `.rota/`, so `/rota-ship` Step 8 is skipped for them.
