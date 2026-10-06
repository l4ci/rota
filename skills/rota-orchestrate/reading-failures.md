# Reading failures

Loaded by `skills/rota-orchestrate/SKILL.md` section 4 when a slot is `dead`, `unknown`, stalled or stuck, a suite is red, or the base breaks.

**Dead vs stalled.** `dead`: no live agent (tab gone or process exited). `stalled`: live agent, nothing moved for `round.stallMinutes` (no commit, edit or state change). Stalled is usually a long test run, and a worker waiting on your escalation is never stalled. Read the pane before acting on either.

Three verbs move an assigned issue:

- **`rota round return`** is the worker's own verb for giving an issue back (blocked, wrong premise). You don't use it. The branch is pushed and kept, the claim is released, and the issue is a candidate again. A fresh `assign` starts clean; it does not continue the pushed branch.
- **`rota round transfer <issue> --to <slot>`** continues the pushed branch in another slot, with the handoff comment named in the brief. Use it when the work is good and the worker is the problem (dead, wrong account, out of quota). `--to human` labels the issue `needs-human` and dispatches nothing: use it when the next step is a person's call.
- **`rota round reclaim <slot>`** frees a `dead` or `stalled` slot and does not reassign; `assign` or `transfer` is your next call. Reclaiming a stalled slot kills its pane, so read the pane first and decide it is hung. A healthy slot needs `--force`, which you almost never want.

All three push the branch before moving the slot off it, so no work is lost. `rota reap` never reclaims a stalled slot and never kills a live agent.

**Default for a stuck worker** (`idle` with no PR and no question, `dead`, or `stalled` past `round.stallMinutes`): read the pane once. If the agent is alive and working, wait. Otherwise `rota round reclaim <slot>`, then `rota round transfer <issue> --to <free slot>`. Use `--to human` only when the next step is a person's call.

**`unknown`.** The host reports a state `rota` can't classify. Never treat it as finished. Wait through one more `wait`; if the slot is still `unknown`, read its pane; if the pane shows a prompt or a stopped agent, run `herdr agent explain` on it, then treat the slot as `dead` or `blocked` accordingly.

**Red tests.** Before you bounce a PR for a red suite, rerun the failing test alone. Passes alone, fails under load: a flake; note it, don't send the worker back. Fails alone: real. A green suite that surprises you is worth one rerun.

**Green branch, red base.** A branch can pass and still break the base once merged: the gate's `verify-failed` verdict. The base is the problem now: stop assigning, find which merge broke it, fix or revert before any other merge. Tell the maintainer.
