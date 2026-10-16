# Reading failures

Loaded by `skills/rota-orchestrate/SKILL.md` section 4 when a slot is `dead`, `unknown`, `unverifiable`, stalled or stuck, a suite is red, or the base breaks.

**Dead vs stalled.** `dead`: no live agent (tab gone or process exited). `stalled`: live agent, nothing moved for `round.stallMinutes` (no commit, edit or state change). Stalled is usually a long test run, and a worker waiting on your escalation is never stalled. Read the pane before acting on either. A worker that ran `rota worker send <slot> --heartbeat` is alive, not necessarily progressing: it restarts the clock and changes no state, so a slot that heartbeats for hours with no commit is still yours to read. A `blocked` or `question` event on `round status` (`event`) is the worker's own record; read it before the pane. Known gap (#707): an old session's `worker send` after a redispatch is guarded only by the dispatch clearing the event; if a fresh slot reads blocked with a question you never saw, check the pane.

Three verbs move an assigned issue:

- **`rota round return`** is the worker's own verb for giving an issue back (blocked, wrong premise). You don't use it. The branch is pushed and kept, the claim is released, and the issue is a candidate again. A fresh `assign` starts clean; it does not continue the pushed branch.
- **`rota round transfer <issue> --to <slot>`** continues the pushed branch in another slot, with the handoff comment named in the brief. Use it when the work is good and the worker is the problem (dead, wrong account, out of quota). `--to human` labels the issue `needs-human` and dispatches nothing: use it when the next step is a person's call.
- **`rota round reclaim <slot>`** frees a `dead` or `stalled` slot and does not reassign; `assign` or `transfer` is your next call. Reclaiming a stalled slot kills its pane, so read the pane first and decide it is hung. A healthy slot needs `--force`, which you almost never want.

All three push the branch before moving the slot off it, so no work is lost. `rota reap` never reclaims a stalled slot and never kills a live agent.

**`unverifiable`.** The host could not be asked or the pane could not be read, and the row's evidence says which: `host-unreachable` (herdr down, socket error), `stale-handle` (the tab or pane handle no longer exists, as after a layout split) or `pane-unreadable`. It is not `dead`: contact loss is not process death, and a live worker was once reported dead this way (#190). `dead` needs positive proof the session exited. The verbs act on that: `round wait` and `round watch` keep waiting through it, `rota reap` leaves the slot alone, and `rota round reclaim` exits 4 with `blockedBy: unverifiable` and the reason. The one allowed action: inspect (open the tab, read the pane yourself, restore the host), then wait for the slot to answer again or, once you are sure the agent is gone or hung, `rota round reclaim <slot> --force`. A forced reclaim kills nothing, since the host will not answer, so close any pane you find still running first.

**Default for a stuck worker** (`idle` with no PR and no question, `dead`, or `stalled` past `round.stallMinutes`): read the pane once. If the agent is alive and working, wait. Otherwise `rota round reclaim <slot>`, then `rota round transfer <issue> --to <free slot>`. Use `--to human` only when the next step is a person's call.

**`unknown`.** The host reports a state `rota` can't classify. Never treat it as finished. Wait through one more `wait`; if the slot is still `unknown`, read its pane; if the pane shows a prompt or a stopped agent, read the explain excerpt in the row's evidence (or run `herdr agent explain` on it for the full text), then treat the slot as `dead` or `blocked` accordingly.

**Red tests.** Before you bounce a PR for a red suite, rerun the failing test alone. Passes alone, fails under load: a flake; note it, don't send the worker back. Fails alone: real. A green suite that surprises you is worth one rerun.

**Green branch, red base.** A branch can pass and still break once merged with the base: the gate's `verify-failed` verdict. The gate verifies a scratch merge before landing, so `data.changed` is false: the base is untouched and the slot goes back. Only with `data.changed` true (a `VERIFY-AGAIN` run on the landed base) is the base the problem: stop assigning, find which merge broke it, fix or revert before any other merge. Tell the maintainer.
