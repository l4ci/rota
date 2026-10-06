# Autopilot and solo mode

Loaded by `skills/rota-orchestrate/SKILL.md` section 3 (autopilot) and the Solo mode section.

## Contents

- [Autopilot](#autopilot)
- [Solo mode](#solo-mode)

## Autopilot

With `round.autopilot` on, run `rota round watch --autopilot` in place of the plain watch. Each wake it ticks:

- repairs safe drift;
- gates and merges finished PRs (never where `ship.mergeApproval` asks for a person, never without a pass);
- assigns ready candidates to idle slots at the default tier.

It returns only for `autopilot.needsYou` items you have not seen: a blocked, limited or dead slot, a failed gate, drift it won't repair, a merge a policy leaves to you. Those stay yours: answer, escalate, bounce or fix as usual.

- It never answers a `ROTA-BLOCKED`, so don't read a quiet watch as "no questions".
- It is still the one watch the Stop hook wants, and a wind-down stops it.
- `rota round tick` runs one pass by hand.

## Solo mode

`rota round start` picks the host once per round. With `work.dispatch` unset or `subagent` it uses herdr inside a herdr pane, tmux inside tmux, and otherwise solo; `rota round status` shows which. Under solo each worker is a subagent you launch, working in its slot's worktree. That needs a harness whose subagent can be pinned to a directory: only Claude Code (its `Agent` tool) qualifies, and everything below says `Agent` for that. In Codex, Hermes or opencode, if `rota round status` reports solo, don't start workers: tell the maintainer to run the round in tmux or herdr (`rota config set work.dispatch tmux`, then relaunch `rota orchestrate`).

- **Launch.** `rota round assign` starts nothing: it returns `data.brief` and `data.worktree`. Launch one `Agent` per assignment, in the background so the workers run at once, with the brief as the prompt and an opening line telling it to work only in that worktree. A worker that edits your checkout instead has broken the round; reset its work before assigning again.
- **Collect.** When an `Agent` returns, record what it said: `rota round report <slot> --state done --pr <url>`, or `blocked`, `dead`, `limited`, with `--evidence` quoting its last line. `rota round wait` doesn't block under solo; the `Agent` completion is your wait. Then review and gate as usual.
- **No panes.** `worker dispatch`, `--relay`, `poll` and `session` refuse. To answer a blocked worker, launch a fresh `Agent` on the same worktree with the brief and your signed answer. There is nothing to kill: report a runaway `dead` and reclaim the slot.
- **Unchanged.** Gates, merge policy, escalations, `reconcile`, `reap` and wind-down work as in tab mode. An escalation reaches the maintainer only as the thread comment; there is no notification.
- **Limits.** Every subagent shares your account, rate window and context. One `limited` stops them all and you with them, so keep to two or three slots and ask each `Agent` for a short result (PR URL, one line). Claude only: a Codex subagent cannot be given the worktree, so `--kind codex` refuses.
