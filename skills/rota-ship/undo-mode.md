# Undo Mode (`--undo`)

Inverse of a `/rota-work` cycle: `rota ship undo` resets the most recent `merge: …` commit on the base branch and restores the resolved items to BACKLOG. Previews unless given `--apply`; refuses PR-mode cycles (the merge happened upstream), post-merge commits without `--allow-post-merge`, a dirty tree, a non-base branch and a non-`merge: ` subject. A different cycle: `rota ship undo --cycle <hash>`, run by the user directly.

Phases: *Preview*, *Confirm*, *Apply*, *Report*. Track them with the host's task tool if it has one.

**U1 — Preview.** `rota ship undo`, then show the plan verbatim. Exit 3: no cycle, say so and stop. Exit 4: surface the verb's message verbatim and stop. A dirty tree gets *"Working tree is dirty — commit, stash, or discard before /rota-ship --undo can run."* If post-merge commits block it, name `--allow-post-merge` (discards them) but never pass it unasked.

**U2 — Confirm.** One `AskUserQuestion` with the plan above it, header `"Apply"`, *"Apply this rollback plan?"*: `"Apply (Recommended)"` (resets the base branch, restores the entries) / `"Cancel"` (print *"No changes."*, stop). Only an explicit yes applies.

> **Manual gate — destructive reset.** The gate always asks. `rota gate list` has no entry for it and the verb enforces nothing beyond the `--apply` preview split, so this confirmation is the only guard before `git reset --hard`, which is unrecoverable past the reflog window.

**U3 — Apply.** `rota ship undo --apply` (exit 5: the reset happened but restoring an item failed; tell the user which). Print the verb's summary line. Terminal: no `/rota-learn`, no docs. The user reruns `/rota-work` to see the restored backlog.

Not for PR-mode cycles (`gh pr close` for open PRs, `git revert` for merged ones), more than one cycle at once (invoke twice), or edits to what landed (`/rota-capture` then `/rota-work`).
