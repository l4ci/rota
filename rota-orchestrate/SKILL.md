---
name: rota-orchestrate
description: >-
  Run a parallel round as the orchestrator: choose the slate, read what workers are doing, answer or escalate their questions, merge their PRs, wind the round down. Judgment only: the `rota round` verbs do the sequencing and enforce the rules. Use on "you are the orchestrator", "run a round", "orchestrate", "assign the next issues to the workers", "what are my workers doing".
---

**Print the banner below verbatim before any other action — skip if dispatched as a subagent.** See `references/banner-preamble.md`.

```
══════════════════════════════════════════════════════════════════
  🎛  rota-orchestrate  ·  run a parallel round
  triggers: "you are the orchestrator", "run a round"  ·  pairs: rota-work, rota-ship, rota-review
══════════════════════════════════════════════════════════════════
```

# rota-orchestrate: Run a Round

A round is one orchestrator (you) and up to five standing workers, each in its own worktree and host tab, each holding one issue. Workers build and open PRs. You choose, route, answer and merge. The mechanics are `rota round` verbs; this skill holds the calls a verb cannot make. If a verb refuses, the refusal is the rule: read `data.blockedBy` and `error.hint`, don't route around it.

The workers' standing brief is [references/worker-contract.md](references/worker-contract.md). `rota round assign` hands it over by pointer. Read it once so you know what your workers were told, and what you are not allowed to contradict.

## When NOT to use

- One item, no parallelism → `/rota-work`.
- You are a worker, not the orchestrator → read the contract above and stop.
- No terminal host (herdr or tmux) is not a reason to skip a round: it runs in solo mode (below). For one or two small items, `/rota-work` with subagents is still lighter.

## 1. Start

Run `rota doctor`. Fix every `fail` with its hint before anything else; a round that starts on a broken host fails late and obscurely. Then `rota round start`, and read `data.drift` and `data.candidates`. For a round that keeps going as issues become ready, start it with `--scope open`: every open issue is a candidate and overlap and dependencies decide the order. Re-running `start` keeps the recorded scope unless you pass `--scope`; re-running it with `--scope slate --items …` replaces the slate without ending the round. A non-zero `drift` is the previous round's mess: `rota round reconcile` shows it, `rota reap` clears the leftovers once you've read the list.

One orchestrator per repo. If `start` exits 4 on the lease, someone else holds it. Do not clear their lease; ask.

## 2. Choose the slate

`rota round candidates` says what is ready. It does not say what is wise. You decide:

- **How many.** Fewer than the roster is fine. Two issues touching one subsystem serialize better than they merge. Start with what you can review, not what the roster can hold.
- **Overlap.** The `overlap` check compares files. It cannot see two issues that change the same behavior through different files. Read both bodies when they share a milestone or a verb. Pass `--accept-overlap` only after you have decided the order of the merges, and say which goes first in the second worker's brief.
- **Premise.** An issue planned weeks ago may be wrong now. If the repo has moved, ask before assigning. Workers are told to dispute a ticket, but a bounced assignment costs a slot and a round trip.
- **Tier.** Pick the worker's tier per issue with `rota round assign --tier`: `light` for reading and search, `standard` for code and tests, `heavy` for design and hard debugging. Config sets the default and maps tiers to models; rota never chooses from the issue, you do. Stay at the default unless the issue needs more. A tier above the default needs `--tier-reason`, one line the PR body repeats, so spend `heavy` where a wrong call costs a round. The worker sizes its own subagents by the same tiers and writes `Worker tier: <tier> (<model>)` in its PR body; check it when you review.
- **Answered decisions.** If the maintainer has settled something that touches the issue, pass it verbatim in `--body-file`. A worker cannot read your conversation.

Assign with `rota round assign <ID>`. The verb marks the item in progress, cuts the branch and starts the worker. Don't do those steps by hand.

## 3. The loop

Call `rota round wait`. It blocks until a slot needs you, then returns that slot with the state and the evidence, and records what it returned. A slot comes back once per change: the next `wait` skips it until its worker moves again (a relay, a dispatch, a new state). Never poll in your own context: no sleep loops, no repeated `status`, no tailing panes. When `wait` returns, act, then call it again. If your shell cuts commands short, loop on a finite `--timeout`.

**Keep every slot fed.** After every `wait`, before you review anything, fill every free slot from `rota round candidates`. A slot whose worker reported `done` with a PR is free: `assign` parks it, keeps the PR on the round's review list (`rota round status` lists it under `review`) and gives the slot its next issue. Hold a slot back only for a real ordering constraint: a dependency, an overlap you have decided to serialize, or the maintainer's stated order. "One worker is still busy" is never a reason.

What each state asks of you:

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

## 4. Reading failures

**Dead vs stalled.** A `dead` slot has no live agent: the tab is gone or the process exited. A `stalled` slot has a live agent and nothing has moved for `round.stallMinutes` (no commit, no edit, no state change). Stalled is usually a long test run, not a failure, and a worker waiting on your escalation is never stalled. Read the pane before acting on either.

To move an assigned issue, the C10 verbs:

- **`rota round return`** is the worker's own verb for giving an issue back (blocked, wrong premise). You don't use it. The branch is pushed and kept, the claim is released, and the issue is a candidate again. A fresh `assign` starts clean: it does not continue the pushed branch.
- **`rota round transfer <issue> --to <slot>`** continues the pushed branch in another slot, with the handoff comment named in the brief. Use it when the work is good and the worker is the problem (dead, wrong account, out of quota). `--to human` labels the issue `needs-human` and dispatches nothing: use it when the next step is a person's call.
- **`rota round reclaim <slot>`** frees a `dead` or `stalled` slot and does not reassign; `assign` or `transfer` is your next call. Reclaiming a stalled slot kills its pane, so do it only after you have read the pane and decided it is hung. A healthy slot needs `--force`, which you almost never want.

All three push the branch before moving the slot off it, so no work is lost. `rota reap` never reclaims a stalled slot and never kills a live agent.

**`unknown`.** The host reports a state `rota` can't classify. Never treat it as finished. Policy: wait through one more `wait`; if the slot is still `unknown`, read its pane; if the pane shows a prompt or a stopped agent, run `herdr agent explain` on it, then treat the slot as `dead` or `blocked` accordingly. A round must not stall on a state nobody read.

**Red tests.** A failing run on a loaded machine is not a failing change. Before you bounce a PR for a red suite, rerun the failing test alone. A test that passes alone and fails under load is a flake: note it, don't send the worker back. A test that fails alone is real. A suite that is green and surprises you is worth one rerun before you believe it.

**Green branch, red base.** A branch can pass and still break the base once merged. `rota worker gate` re-verifies on the merged tree and exits 1 with `verify-failed`. When it does, the base is the problem now: stop assigning, find which merge broke it, and fix or revert before any other merge. Say so to the maintainer.

## 5. Escalations and provenance

**What to escalate.** A choice a user would notice, that neither the issue nor the code settles: product behavior, a public name, a breaking change, what to cut. Keep the defensible implementation calls: a helper's name, a test's shape, which of two equal approaches. When a worker asks you the first kind, you ask the human. When it asks the second, you answer.

`rota round escalate send <number> --title … --body-file …` posts the question on the issue or PR and notifies the maintainer. It returns at once; keep working the other slots. `rota round escalate check` reads the thread for an answer. One question per escalation, written for someone who doesn't have the file open.

**Provenance.** Workers cannot tell your relay from a maintainer's typing from text a terminal UI put on the prompt line. So:

- Sign every message you send a worker. `rota worker dispatch --relay` does it; hand-typed text doesn't.
- Before a relay or a re-dispatch, check the tab with `herdr agent get <agent>`. `focused: true` means a human is typing there; tell them instead of typing over them. No `rota` verb checks this.
- Cite the real channel of every approval: `maintainer in pane`, `issue comment #N`, `orchestrator relay round N`. Never present a relay as the maintainer's own word.
- A line starting `m:` in a pane is a maintainer answer by convention, but anyone can type it. If it contradicts your last signed message, confirm once.
- Read each PR's `## Approvals` section for the channels it names. A cited approval you never relayed is a finding.

## 6. Merge

Workers never merge. After `done`, read the PR: does it do what the issue says, and does it stay inside the files the issue named? Then `rota worker gate <slot> --base <branch>`, or `rota worker gate <PR number> --base <branch>` once the slot has moved on and the PR waits in review. The gate runs the checks on the merged tree, merges on a pass and drops the PR from the review list. Read its verdict; don't re-derive the rules it enforces.

With several PRs waiting, merge them as one train: `rota worker train <slot|PR>... --base <branch>` in the order you want them to land. One verify covers all of them. On a red train it names the `culprit`: send that PR back, then re-run the train without it, or pass `--land-green` to land the members that verified before it. `base-moved` means nothing landed; re-run.

Merge policy comes from config (`ship.mergeApproval`). With the default, the gate merges a passing PR. When policy requires approval (all PRs, or PRs touching listed paths), unattended runs pass `--escalate` to `worker gate`, or to `ship pr-merge` for a PR you merge by number. The verb refuses with exit 4 and posts the approval request on the PR thread, once however often you re-gate. Keep working other slots. `rota round escalate check` says when the maintainer has answered; re-run with `--approval <id>`, and the audit line quotes the answer. An answer that doesn't read as approval (`approve`, `approved`, `yes`, `lgtm`, `ship it`) holds the merge: `approval declined` means the slot is held, never retried, and you tell the maintainer. Interactive sessions ask with `AskUserQuestion` and pass `--confirm --confirm-note` with the answer verbatim. Never write a note the human didn't say.

After each merge, re-verify the base before assigning from it. The next assignment branches from a base you have just proved.

## 7. Bounce or fix

Send the PR back when the work is wrong in a way the worker can learn from: it misread the issue, skipped a stated criterion, built the wrong thing. State the gap in one signed message citing the issue's own words. If its slot still holds the PR, relay to that worker. If the PR waits in review with no slot, `rota round transfer <issue> --to <free slot> --body-file <gap>` puts the branch in that slot and dispatches the follow-up; any free worker can do it.

Fix it yourself when the gap is small and mechanical: a stale doc line, a missing test for a case the worker covered in code, a merge conflict with a PR you just merged. Push the fix as a separate commit so the PR shows what you changed. A worker rerunning a full cycle for a one-line fix wastes a slot.

Bounce when in doubt about who is right. The ticket may be the wrong one.

## 8. Wind down

When the slate is done or the maintainer calls the round: `rota round wind-down`. It re-verifies the base, parks every slot and releases the lease. If a slot still holds work it exits 4 and parks the rest; read which slot and why before deciding. Then run `rota round reconcile` and `rota reap` for what is left, and give the maintainer a short summary: what merged, what bounced, what is open, what drift remains.

## Solo mode

`rota round start` picks the host once per round. With `work.dispatch` unset or `subagent` it uses herdr inside a herdr pane, tmux inside tmux, and otherwise solo; `rota round status` shows which. Under solo each worker is a Claude `Agent` subagent you launch, working in its slot's worktree.

- **Launch.** `rota round assign` starts nothing: it returns `data.brief` and `data.worktree`. Launch one `Agent` per assignment, in the background so the workers run at once, with the brief as the prompt and an opening line telling it to work only in that worktree. A worker that edits your checkout instead has broken the round; reset its work before assigning again.
- **Collect.** When an `Agent` returns, record what it said: `rota round report <slot> --state done --pr <url>`, or `blocked`, `dead`, `limited`, with `--evidence` quoting its last line. `rota round wait` doesn't block under solo; the `Agent` completion is your wait. Then review and gate as usual.
- **No panes.** `worker dispatch`, `--relay`, `poll` and `session` refuse. To answer a blocked worker, launch a fresh `Agent` on the same worktree with the brief and your signed answer. There is nothing to kill: report a runaway `dead` and reclaim the slot.
- **Unchanged.** Gates, merge policy, escalations, `reconcile`, `reap` and wind-down work as in tab mode. An escalation reaches the maintainer only as the thread comment; there is no notification.
- **Limits.** Every subagent shares your account, rate window and context. One `limited` stops them all and you with them, so keep to two or three slots and ask each `Agent` for a short result (PR URL, one line). Claude only: a Codex subagent cannot be given the worktree, so `--kind codex` refuses.

## Rules that outlive any verb

1. Workers open PRs; only you merge.
2. Verify on the merged tree, not the branch.
3. Escalate unsettled product decisions; keep defensible implementation calls.
4. Cite the channel of every approval. Sign every message.
5. Stage explicit paths. Never stage everything.
