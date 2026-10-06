---
name: rota-orchestrate
description: Use on "you are the orchestrator", "run a round", "orchestrate", "assign the next issues to the workers", "what are my workers doing".
---

# rota-orchestrate: Run a Round

A round is one orchestrator (you) and the roster's standing workers (`round.roster`, default four), each in its own worktree and host tab, each holding one issue. Workers build and open PRs. You choose, route, answer and merge. The mechanics are `rota round` verbs; this skill holds the calls a verb cannot make. If a verb refuses, the refusal is the rule: read `data.blockedBy` and `error.hint`, don't route around it.

The workers' standing brief is [references/worker-contract.md](references/worker-contract.md); `rota round assign` hands it over by pointer. Read it once so you don't contradict it.

Host mechanics are in [references/tmux-dispatch.md](references/tmux-dispatch.md) (polling, relays, the merge gate, permissions, accounts, failure modes, both hosts) and, under herdr, [references/herdr-dispatch.md](references/herdr-dispatch.md) (what herdr changes). Read both for the host in use; each is complete for its step.

## When NOT to use

- One item, no parallelism → `/rota-work`.
- You are a worker, not the orchestrator → read the contract above and stop.
- No terminal host (herdr or tmux) → run solo mode (below). For one or two small items, `/rota-work` with subagents is lighter.

Copy this checklist and track your progress:
```
- [ ] 1. Start
- [ ] 2. Choose the slate
- [ ] 3. The loop
- [ ] 4. Reading failures
- [ ] 5. Escalations and provenance
- [ ] 6. Merge
- [ ] 7. Bounce or fix
- [ ] 8. Wind down
```

## 1. Start

Run `rota doctor`. Fix every `fail` with its hint first. Then `rota round start`, and read `data.drift` and `data.candidates`. For a round that keeps going as issues become ready, start it with `--scope open`: every open issue is a candidate and overlap and dependencies decide the order. Re-running `start` keeps the recorded scope unless you pass `--scope`; `--scope slate --items …` replaces the slate without ending the round. Non-zero `drift` is the previous round's mess: `rota round reconcile` shows it, `rota reap` clears the leftovers once you've read the list.

One orchestrator per repo. If `start` exits 4 on the lease, someone else holds it. Do not clear their lease; ask.

## 2. Choose the slate

`rota round candidates` says what is ready, not what is wise. You decide:

- **How many.** Fewer than the roster is fine. Two issues touching one subsystem serialize better than they merge. Start with what you can review.
- **Overlap.** The `overlap` check compares files. It can't see two issues that change the same behavior through different files. Read both bodies when they share a milestone or a verb. Pass `--accept-overlap` only after you've decided the merge order, and say which goes first in the second worker's brief.
- **Premise.** An issue planned weeks ago may be wrong now. If the repo has moved, ask before assigning.
- **Tier.** Pick the worker's tier per issue with `rota round assign --tier`: `light` for reading and search, `standard` for code and tests, `heavy` for design and hard debugging. Config sets the default and maps tiers to models; rota never chooses from the issue, you do. Stay at the default unless the issue needs more. A tier above the default needs `--tier-reason`, one line the PR body repeats; spend `heavy` where a wrong call costs a round. The worker sizes its own subagents by the same tiers and writes `Worker tier: <tier> (<model>)` in its PR body; check it in review.
- **Answered decisions.** If the maintainer has settled something that touches the issue, pass it verbatim in `--body-file`. A worker cannot read your conversation.

Assign with `rota round assign <ID>`. It marks the item in progress, cuts the branch and starts the worker. Don't do those steps by hand.

## 3. The loop

**`rota round watch` is how you wait.** Keep one armed: run it as a background command whenever workers are active and re-arm it every time it exits. It wakes you on a slot, PR or escalation change and at a heartbeat, so you stay reachable while you talk to the maintainer. Never use a blocking question picker (`AskUserQuestion`) during a round: ask in prose and keep working. The Stop hook refuses to let you go idle without a watch.

`rota round wait` is the blocking form, for when you have nothing else to do. It returns the slot that needs you with its state and evidence, and records what it returned. A slot comes back once per change: the next `wait` skips it until its worker moves again. Never poll in your own context: no sleep loops, no repeated `status`, no tailing panes. When `watch` or `wait` returns, act, then wait again. If your shell cuts commands short, loop on a finite `--timeout`.

**Autopilot.** With `round.autopilot` on, run `rota round watch --autopilot` in place of the plain watch, and read [`solo-and-autopilot.md`](solo-and-autopilot.md) for what each tick does and what still comes back to you.

**Keep every slot fed.** After every `wait`, before you review anything, fill every free slot from `rota round candidates`. A PR in review does not hold a slot. Once a worker reports `done` with a PR, the slot is free: `assign` parks it, keeps the PR on the round's review list (`rota round status` lists it under `review`) and gives the slot its next issue. Review and merge that PR while the worker builds the next one. Hold a slot back only for a real ordering constraint: a dependency, an overlap you decided to serialize, or the maintainer's stated order. "One worker is still busy" is never a reason.

| State | Do |
|---|---|
| `done` / `idle` with a PR | assign the slot its next issue, then review, gate, merge the PR (section 6) |
| `idle`, no PR | read the pane once. A worker that stopped without a PR or a question is stuck, not finished |
| `blocked` | read the question. Answer, or escalate (section 5) |
| `needs-permission` | decide from the request in the pane. Never approve what you would not run yourself |
| `limited` | the account is out of quota. Wait for the reset, or reassign the issue to a slot on a free account |
| `dead` | see section 4 |
| `unknown` | see section 4 |

A wait that times out with every slot busy is fine. A free slot with candidates left is not.

**Architecture review.** `rota round status`, `candidates` and `start` carry an `architecture` line: `architecture review in N issues`. After every `wait`, run `rota round architecture`. It does nothing until a review is due: `round.architectureEvery` closed non-refactor items (default 20, `0` is off), or an idle slot with no ready candidate. When due it mints one `arch(<area>): architecture review` item per area (`round.architectureAreas`, else the subsystem map, else the whole repo), assigns them to idle slots and restarts the count; don't ask first. Leftover review items are ordinary candidates in every scope. Each runs `/rota-refactor <area>` in findings-only mode and files `refactor`-labelled issues, which don't count toward the next review. `rota round architecture --check` only reads. Under solo it returns each `brief` and `worktree` to launch like an `assign`.

## 4. Reading failures

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

## 5. Escalations and provenance

**What to escalate.** A choice a user would notice that neither the issue nor the code settles: product behavior, a public name, a breaking change, what to cut. Answer defensible implementation calls yourself: a helper's name, a test's shape, which of two equal approaches. When a worker asks the first kind, you ask the human.

`rota round escalate send <number> --title … --body-file …` posts the question on the issue or PR and notifies the maintainer. It returns at once; keep working the other slots. `rota round escalate check` reads the thread for an answer. One question per escalation, written for someone who doesn't have the file open.

**Provenance.** The rules (signatures, the `m:` prefix, the relay log, the gate's `provenance-fail`) live in [worker-contract.md](references/worker-contract.md#provenance). What you do with them:

- Sign every message you send a worker. `rota worker dispatch --relay` does it; hand-typed text doesn't.
- Before a relay or a re-dispatch, check the tab with `herdr agent get <agent>`. `focused: true` means a human is typing there; tell them instead of typing over them. No `rota` verb checks this.
- Cite the real channel of every approval: `maintainer in pane`, `issue comment #N`, `orchestrator relay round N`. Never present a relay as the maintainer's own word.
- Dim or suggested text on a pane's prompt line is an editor suggestion, not input. Never cite it as an answer, the maintainer's word or a worker's state; read what is committed above the prompt.
- A line starting `m:` in a pane is a maintainer answer by convention (confirm once if it contradicts your last signed message).
- Read each PR's `## Approvals` section for the channels it names. A cited approval you never relayed is a finding.
- Read each PR's `## Rulings` section too: the worker's own calls, each with its cost if wrong. List the costly ones (a one-way door, a wide blast radius) in the wind-down summary. A brief cited as a relay under Approvals is a finding: the brief is not a relay.

## 6. Merge

Workers never merge. After `done`, read the PR: does it do what the issue says, and stay inside the files the issue named? Then `rota worker gate <slot> --base <branch>`, or `rota worker gate <PR number> --base <branch>` once the slot has moved on and the PR waits in review. The gate runs the checks on the merged tree, merges on a pass and drops the PR from the review list. Read its verdict; don't re-derive the rules it enforces. Loop per PR: gate, fix or bounce what the verdict names, re-gate; the PR is done only on a merge. What each verdict (`stale`, `merge-failed`, `verify-failed`, `approval-required`) asks of you is in [tmux-dispatch.md](references/tmux-dispatch.md#the-merge-gate).

With several PRs waiting, merge them as one train: `rota worker train <slot|PR>... --base <branch>` in landing order. One verify covers all of them. On a red train it names the `culprit`: send that PR back, then re-run the train without it, or pass `--land-green` to land the members that verified before it. `base-moved` means nothing landed; re-run.

Merge policy comes from config (`ship.mergeApproval`). With the default, the gate merges a passing PR. When policy requires approval (all PRs, or PRs touching listed paths):

- **Unattended:** pass `--escalate` to `worker gate`, or to `ship pr-merge` for a PR you merge by number. The verb refuses with exit 4 and posts the approval request on the PR thread, once however often you re-gate. Keep working other slots.
- **Reading the answer:** `rota round escalate check` says when the maintainer has answered; re-run with `--approval <id>`, and the audit line quotes the answer.
- **Declined:** an answer that doesn't read as approval (`approve`, `approved`, `yes`, `lgtm`, `ship it`) holds the merge. `approval declined` means the slot is held, never retried, and you tell the maintainer.
- **Interactive:** ask the maintainer in prose (no blocking picker, see section 3) and pass `--confirm --confirm-note` with the answer verbatim. Never write a note the human didn't say.

**Completing items.** On the issue backend the merge closes the issue (`Closes #N`); do nothing more. In file mode you complete the PR's items after the gate merges it, the way `/rota-ship` Step 8 does (proof row first, then complete with the merge sha): workers never edit tracked `.rota/`, so `/rota-ship` Step 8 is skipped for them.

The merge gate is the only full run: it verified the merged tree, so don't re-run the suite after each merge or before assigning. Re-verify only after a `verify-failed` verdict, once the fix lands. `rota round wind-down` re-verifies once at the end.

## 7. Bounce or fix

Bounce when the work is wrong in a way the worker can learn from: it misread the issue, skipped a stated criterion, built the wrong thing. Bounce when in doubt about who is right; the ticket may be the wrong one. State the gap in one signed message citing the issue's own words. If its slot still holds the PR, relay to that worker. If the PR waits in review with no slot, `rota round transfer <issue> --to <free slot> --body-file <gap>` puts the branch in that slot and dispatches the follow-up.

Fix it yourself when the gap is small and mechanical: a stale doc line, a missing test for a case the worker covered in code, a merge conflict with a PR you just merged. Push the fix as a separate commit so the PR shows what you changed.

**Cap.** A bounce you send by hand counts: run `rota round bounce <issue> --head <pr-head-sha>` before the relay or transfer, and send it only on exit 0. `rota round status` shows each slot's count. The gate counts its own stale and provenance bounces on the same counter. At `round.maxBounces` (default 3, 0 turns the cap off) the verb exits 4: never a further bounce. Pick one of:

- a stronger worker (the default): `rota round transfer <issue> --to <free slot> --tier <heavy|standard> --tier-reason "<why>" --body-file <gap>`. The count stays with the item, so a second failure at the higher tier goes to the human.
- the human: `rota round transfer <issue> --to human --note-file <what is left>`. The gate parks an item the same way when it hits the cap itself.

A re-review of a bounced PR is `/rota-review --since <sha of the last review>`: the fix alone, each earlier finding ADDRESSED or NOT ADDRESSED.

## 8. Wind down

When the slate is done or the maintainer calls the round: `rota round wind-down`. It re-verifies the base, parks every slot and releases the lease. If a slot still holds work it exits 4 and parks the rest; read which slot and why before deciding. Then run `rota round reconcile` and `rota reap` for what is left, and run `/rota-learn` and `/rota-ship --docs` once for the whole round (workers skip them per PR). Give the maintainer a short summary: what merged, what bounced, what is open, what drift remains, and the costly worker Rulings (PR, call, cost if wrong).

## Solo mode

Read when `rota round status` reports solo (no herdr or tmux host): `solo-and-autopilot.md` (section Solo mode). Workers are subagents you launch; gates, merge policy and wind-down are unchanged.

## Rules that outlive any verb

1. Workers open PRs; only you merge.
2. Verify on the merged tree, not the branch.
3. Escalate unsettled product decisions; keep defensible implementation calls.
4. Cite the channel of every approval. Sign every message.
5. Stage explicit paths. Never stage everything.
