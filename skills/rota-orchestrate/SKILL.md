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

Run `rota doctor`. Fix every `fail` with its hint first. Then `rota round start`, and read `data.drift` and `data.candidates`. For a round that keeps going as issues become ready, start it with `--scope open`: every open issue is a candidate and overlap and dependencies decide the order. Re-running `start` keeps the recorded scope unless you pass `--scope`; `--scope slate --items …` replaces the slate without ending the round. If `data.handoff` is present, a prior orchestrator paused: read that note (`data.handoff.path`) before choosing the slate, since it holds the review queue, the agreed merge order and the open maintainer questions. Once you have read it, run `rota round start --consume-handoff` (a rejoin, so it changes nothing else) to archive it; never delete it by hand. Non-zero `drift` is the previous round's mess: `rota round reconcile` shows it, `rota reap` clears the leftovers once you've read the list.

One orchestrator per repo. If `start` exits 4 on the lease, someone else holds it. Do not clear their lease; ask.

## 2. Choose the slate

`rota round candidates` says what is ready, not what is wise. You decide:

- **Autonomy.** Read `rota config show autonomy.level` ([`references/autonomy.md`](references/autonomy.md)). `off` reads as the bullets below say. Under `roundtrip` or `auto` you settle the Premise, a `too-vague.` scout reply, an `ambiguous.` subagent report, the tier and brainstorm, plan and spike calls yourself, and state each in one line. Apply the planning dial (`references/planning-dial.md`) per candidate: a level 2 or higher item gets a `heavy`-tier design pass first, with the design landing as a note on the issue, then the build. `roundtrip` runs the current slate to wind-down without the tick, then stops; `auto` runs the tick (section 3). You escalate only what `escalations-and-provenance.md` reserves for the human.
- **How many.** Fewer than the roster is fine. Two issues touching one subsystem serialize better than they merge. Start with what you can review.
- **Scope scouts.** `rota round candidates --json` marks each candidate's `scopeSource`: `touches`, `subsystem`, `inferred` or `none`. For every `none` candidate that no slot holds, dispatch one `light` explorer (`rota-explorer`) with the issue number and this return shape: at most ten bullets, one per symbol, endpoint, config key or test the issue would change, nothing else, then one last line: `scoped.` (bullets above are the scope), `too-vague.` (the issue names nothing concrete enough to scope) or `no-match.` (nothing in the code matches it). Branch on that last line, not on the prose: `scoped.` records the reply before you assign; `too-vague.` records nothing, so leave the item unscoped and, under `off`, ask for a sharper `## Touches` (or hold it from the round), under `roundtrip` or `auto` decide which and report it in one line; `no-match.` records nothing, so check the ticket against the code before assigning it. The same rule holds for the other `rota-*` subagents, whose reports end `done.` (go on), `too-big.` (split the brief and redispatch) or `ambiguous.` (under `off`, settle the reading yourself or ask, never guess; under `roundtrip` or `auto`, settle it and report the reading in one line). Record a `scoped.` reply: `rota round scope <ID> --body-file <path>` (it keeps at most ten entries). The record is a comment of kind `scope`; the declared `## Touches` is never edited and wins once it exists. Overlap compares inferred entries too, but a clash that involves one only warns, even under `round.scopeOverlap: block`, and its detail says `inferred`. Read the clash yourself before you trust or ignore it. Do not scout an issue already in flight.
- **Overlap.** The `overlap` check compares files. It can't see two issues that change the same behavior through different files. Read both bodies when they share a milestone or a verb. Pass `--accept-overlap` only after you've decided the merge order, and say which goes first in the second worker's brief.
- **Premise.** An issue planned weeks ago may be wrong now. If the repo has moved, ask before assigning under `off`; under `roundtrip` or `auto`, decide and report it in one line.
- **Tier.** Pick the worker's tier per issue with `rota round assign --tier`: `light` for reading and search, `standard` for code and tests, `heavy` for design and hard debugging. Config sets the default and maps tiers to models; rota never chooses from the issue, you do. Stay at the default unless the issue needs more. A tier above the default needs `--tier-reason`, one line the PR body repeats; spend `heavy` where a wrong call costs a round. The worker sizes its own subagents by the same tiers and writes `Worker tier: <tier> (<model>)` in its PR body; check it in review.
- **Harness and model.** The issue picks them: `harness:claude|codex` and `model:<id>` labels (file backend: `Harness:`/`Model:` fields), read by `rota round assign`. Precedence: harness `--kind` > label > slot kind > `claude`; model `--model` > label > tier map > harness default. Pass `--kind`/`--model` only to override a label or fill a gap, and say why in `--body-file`. A bad label (unknown harness, two of a kind, a model the launch line cannot take) refuses with exit 4 and names the label: fix the issue, don't override around it. `rota round candidates` shows each item's pick.
- **Answered decisions.** If the maintainer has settled something that touches the issue, pass it verbatim in `--body-file`. A worker cannot read your conversation.

Assign with `rota round assign <ID>`. It marks the item in progress, cuts the branch and starts the worker. Don't do those steps by hand. Several small issues that share files or a subsystem can go to one worker as a bundle: `rota round assign <lead> --bundle <ID>,<ID>` (at most `round.bundleMax` members). Bundle only related issues; rota does not judge that. The bundle lands or bounces as a unit; drop a member with `rota round transfer <member> --to human`.

Work another tool already started (a Codex worktree, an agent-team branch, a cloud session's branch) joins the round with `rota worker adopt <branch|worktree> --issue <N>`. Adopt it when the maintainer points at it, or when `round reconcile` lists an `unregistered-branch` (`round.adoptPattern` names which branches count). It runs the overlap check; read an exit 4 (`overlap`, `registered`, `held`) as you would on `assign`. The slot has no host: do not dispatch to it, wait on it or reclaim it. `round status` derives its state from the PR, and you gate it like any other (`rota worker gate <slot>`). After the merge it is released and its worktree and branch stay; leave them for the maintainer unless they ask for `--prune`.

## 3. The loop

**`rota round watch` is how you wait.** Keep one armed: run it as a background command whenever workers are active and re-arm it every time it exits. It wakes you on a slot, PR or escalation change and at a heartbeat, so you stay reachable while you talk to the maintainer. Never use a blocking question picker during a round: ask in prose and keep working. The one exception is a maintainer-invoked [`/rota-answer`](../rota-answer/SKILL.md) (section 5). The Stop hook refuses to let you go idle without a watch.

`rota round wait` is the blocking form, for when you have nothing else to do. It returns the slot that needs you with its state and evidence, and records what it returned. A slot comes back once per change: the next `wait` skips it until its worker moves again. Never poll in your own context: no sleep loops, no repeated `status`, no tailing panes. When `watch` or `wait` returns, act, then wait again. If your shell cuts commands short, loop on a finite `--timeout`.

**Autopilot.** Under `autonomy.level: auto`, run `rota round watch --autopilot` in place of the plain watch, and read [`solo-and-autopilot.md`](solo-and-autopilot.md) for what each tick does and what still comes back to you. Under `off` and `roundtrip`, `rota round tick` and `watch --autopilot` refuse with the hint `rota config set autonomy.level auto`.

**Keep every slot fed.** After every `wait`, before you review anything, fill every free slot from `rota round candidates`. A PR in review does not hold a slot. Once a worker reports `done` with a PR, the slot is free: `assign` parks it, keeps the PR on the round's review list (`rota round status` lists it under `review`) and gives the slot its next issue. Review and merge that PR while the worker builds the next one. Hold a slot back only for a real ordering constraint: a dependency, an overlap you decided to serialize, or the maintainer's stated order. One more: a gate or train is running, since `round assign` and `round transfer` refuse (`blockedBy: gate-running`) until it exits (section 6); fill the slot right after the verdict. "One worker is still busy" is never a reason. A possible bounce is not a reason either: holding the slot so the same worker can fix its PR costs a quarter hour of idle worker per PR, and a bounce on a parked PR goes by `rota round transfer` to a free slot with the verdict quoted. `round wait` and `round watch` print `assign before you gate` with the next candidate and the `round assign` command when a done slot returns and one is ready; run it. `rota round summary` reports each slot's idle minutes between done and the next assign.

| State | Do |
|---|---|
| `done` / `idle` with a PR | assign the slot its next issue, then review, gate, merge the PR (section 6) |
| `idle`, no PR | read the pane once. A worker that stopped without a PR or a question is stuck, not finished |
| `blocked` | read the question. Answer, or escalate (section 5) |
| `needs-permission` | decide from the request in the pane. Never approve what you would not run yourself |
| `limited` | the account is out of quota. Wait for the reset, or reassign the issue to a slot on a free account |
| `dead` | see section 4 |
| `unknown` | see section 4 |
| `unverifiable` | the host could not vouch for it: not dead, never reclaimed on that alone. `wait` returns it once after `round.stallMinutes` with the reason; see section 4 |

A wait that times out with every slot busy is fine. A free slot with candidates left is not.

When `rota round status`, `candidates` or `start` shows an `architecture review in N issues` line, read [`architecture-review.md`](architecture-review.md); run `rota round architecture` after every `wait`.

## 4. Reading failures

When a slot is `dead`, `unknown`, `unverifiable` or stalled, a worker is stuck, a suite is red, or a merged branch breaks the base, read [`reading-failures.md`](reading-failures.md). Never treat `unknown` as finished, never treat `unverifiable` as dead, and rerun a red test alone before you bounce for it.

## 5. Escalations and provenance

When a worker is `blocked` on a question, or you relay to a worker or review its approvals, read [`escalations-and-provenance.md`](escalations-and-provenance.md). Escalate only choices a user would notice. When two or more decisions wait on the maintainer, see *Many waiting* in that file (`/rota-answer`). Sign every message you send a worker; cite the real channel of every approval.

## 6. Merge

Workers never merge. Read worker and reviewer output by the read-depth rule in [subagent-dispatch.md](../references/subagent-dispatch.md) (*What stays on the orchestrator*): the headline first, the body only to act on it. After `done`, read the PR: does it do what the issue says, and stay inside the files the issue named? Review every PR that waits, then land them with one train: `rota worker train <PR>... --base <branch>` verifies them in a single run where a gate per PR would pay the 2–3 minute verify each, one after the other. Two or more reviewed PRs on one base is the train; `rota round wait` and `rota round watch` print a `nudge train` line naming the PR numbers and the command when that is so. A single gate is the case of exactly one waiting PR: `rota worker gate <slot> --base <branch>`, or `rota worker gate <PR number> --base <branch>` once the slot has moved on and the PR waits in review. A gate (or a train) runs the checks on a scratch merge, merges on a pass and drops the PR from the review list. Read its verdict; don't re-derive the rules it enforces. For a single PR, loop: gate, fix or bounce what the verdict names, re-gate; the PR is done only on a merge. A red train names its culprit: send that PR back and re-run without it ([`merge-train-and-approval.md`](merge-train-and-approval.md)). What each verdict (`stale`, `merge-failed`, `verify-failed`, `approval-required`) asks of you is in [tmux-dispatch.md](references/tmux-dispatch.md#the-merge-gate).

**Partial slices.** A slice PR on a `partial-slice` issue reports `--outcome done` (this PR's slice complete, remaining slices on the summary's third line) and says `Refs #N`; the gate lands it without a relay, so do not tell the worker to report `partial`.

**Review depth.** Before you review a PR, run `rota review depth <branch> --json`: it reads the labels of the issue behind the branch itself, so `risk:high`, `best-of:2` and `partial-slice` count without a flag. For a bundled branch the output lists `bundle` and the depth is the highest any bundled issue needs; the Spec reviewer checks every bundled issue's acceptance, not only the lead's. Run `/rota-review` at that depth (`full` both reviewers, `light` the Spec reviewer only (Standards when the branch references no issue), `none` none), and record the verdicts; `rota worker gate` resolves the same depth and refuses (`blockedBy: review-missing`) a branch without the verdicts it needs.

**Best-of issues.** An issue labelled `best-of:2` is built by two slots at once (`assign` binds both, one Claude and one Codex when both are configured). The gate refuses either PR until you pick. Wait for both PRs, then compare them in this order: acceptance criteria met (each one, against the issue's own words), then the smaller and simpler diff, then the worker's Rulings and their cost. Run `rota round pick <ID> --pr <N> --reason-file <f>` with the comparison as the reason; it closes the other PR and keeps its branch. Then review and gate the winner as usual. If you cannot separate the two on those three tests, escalate to the maintainer (section 5) with both PR links and what you compared; do not toss a coin. A failed attempt (returned, reclaimed, dead) leaves one PR: `pick` takes it alone.

When two or more PRs wait, or `ship.mergeApproval` requires approval, or you complete items in file mode, read [`merge-train-and-approval.md`](merge-train-and-approval.md).

The merge gate is the only full run: it verified the merged tree, so don't re-run the suite after each merge or before assigning. A `verify-failed` verdict normally lands nothing (bounce and re-gate); with `data.changed` true it landed, so fix forward and re-verify once the fix lands. `rota round wind-down` re-verifies once at the end.

**Run the gate or train in the background.** `rota worker gate` and `rota worker train` take 2–3 minutes, so start each as a background command and keep working; don't sit in it. While it runs, review the next waiting PRs (`/rota-review` is API-bound and overlaps the gate's CPU load fine) and hold `rota round assign` and `rota round transfer` until it exits: both refuse (exit 4, `blockedBy: gate-running`) while the land lock is held, since a switched slot branch reds the gate's checkout guard. Rerun them after the verdict. Read the verdict when the command exits, then act on it as above. A bounce on a PR whose slot has moved on goes by `rota round transfer`; the slot was not held for it: holding a slot for a possible bounce is not an ordering constraint (section 3). Don't overlap it with: a second `gate` or `train` (the machine-wide lock only serializes it, so you gain nothing and the second waits), or any tracked `.rota/` write on the base checkout, such as `rota knowledge tier fold`, a sidecar or an `.rota/` edit (the checkout guard sees the changed tracked file and reds the gate). `rota knowledge hit` is safe during a gate: hits are buffered in the gitignored `knowledge-hits.jsonl`, so they never dirty the tree. Only `tier fold` (which writes the tracked sidecar) must wait: commit pending sidecar changes before you start the gate, and hold tracked writes until it exits.

## 7. Bounce or fix

Bounce when the work is wrong in a way the worker can learn from: it misread the issue, skipped a stated criterion, built the wrong thing. Bounce when in doubt about who is right; the ticket may be the wrong one. A verdict-driven bounce relays and counts only the verdict's blocking findings (`blocking: true`; `blocker` and `major` by default). File the deferred ones with `rota verdict add --file-deferred --pr <pr-ref>` at record time instead of relaying them (one follow-up item per PR; floor `review.fileDeferred`); `rota round bounce` refuses (exit 4, `blockedBy: no blocking findings`) when the PR's current review verdicts hold none, unless you pass `--force`. State the gap in one signed message citing the issue's own words. If its slot still holds the PR, relay to that worker. If the PR waits in review with no slot, `rota round transfer <issue> --to <free slot> --body-file <gap>` puts the branch in that slot and dispatches the follow-up.

Fix it yourself when the gap is small and mechanical: a stale doc line, a missing test for a case the worker covered in code, a merge conflict with a PR you just merged. Push the fix as a separate commit so the PR shows what you changed.

A reviewer's comment on a `done` slot's PR reaches the watch as a `review/<slot>` change (and a tick lists a `review` item). Under `round.reviewLoop: manual` (the default) run `rota round review-relay <slot>`: it counts the bounce itself, relays the comments to the worker as a `REVIEW` relay and marks the slot busy, so do not also run `round bounce`. At the cap it exits 4 and escalates on the PR: go to the cap options in [`bounce-cap.md`](bounce-cap.md). Under `auto` the watch does this itself and you hear about it only at the cap. The loop never merges: gate the PR once the worker is `done` again. Under `manual` the autopilot tick still gates a done PR beside the `review` item, so relay first if the review matters.

When you bounce by hand, run `rota round bounce <issue> --head <pr-head-sha> --slot <slot>` first and read [`bounce-cap.md`](bounce-cap.md): the cap, what to do at it, and re-review.

## 8. Wind down

When the slate is done or the maintainer calls the round: `rota round wind-down`. It re-verifies the base, parks every slot and releases the lease. If a slot still holds work it exits 4 and parks the rest; read which slot and why before deciding. Then run `rota round reconcile` and `rota reap` for what is left, and run `/rota-learn` and `/rota-docs` once for the whole round (workers skip them per PR). Restamp the contract docs on `main` as bookkeeping: run `ROTA_DOC_STAMPS=strict bash test/doclint.sh`, fix each drifted doc against `git diff <sha> HEAD -- <ref>`, then `python3 test/check-doc-stamps.py --restamp <doc>.md…` and commit `docs/contributing/contract/STAMPS` direct to `main` (drift only WARNs, so stamps go stale silently otherwise; see `docs/contributing/rounds.md`). `wind-down` ends with the round's summary table (the same as `rota round summary`; per-issue wall time, bounces, gate outcome and quota share, then per-slot and per-account totals): read it and carry the numbers into your summary, saying `n/a` where a share is unknown rather than guessing. Give the maintainer a short summary: what merged, what bounced, what is open, what drift remains, and the costly worker Rulings (PR, call, cost if wrong).

## Support role

A second machine can run rounds beside main without any link between them (`round.support` names it, `round.role: support` is set locally per machine; see `docs/contributing/rounds.md`). On a support machine you never merge and never write tracked `.rota/` content: those verbs exit 4 `blockedBy: role`. Workers there post learnings as `learning` comments (`rota item comment add <ID> --kind learning`), skip the contract-doc restamp, and leave knowledge to main, which runs `/rota-learn` at wind-down and harvests them. Set `round.support` between rounds: bounce counts are not carried over when the key flips. At the bounce cap the item parks `needs-human` and no review-bounce is posted.

## Solo mode

Read when `rota round status` reports solo (no herdr or tmux host): `solo-and-autopilot.md` (section Solo mode). Workers are subagents you launch; gates, merge policy and wind-down are unchanged.

## Rules that outlive any verb

1. Workers open PRs; only you merge.
2. Verify on the merged tree, not the branch.
3. Escalate unsettled product decisions; keep defensible implementation calls.
4. Cite the channel of every approval. Sign every message.
5. Stage explicit paths. Never stage everything.
6. A lost response from a `--request-id` verb is followed by `rota receipt show <request-id>`, never by a re-run: `completed` means read the stored result, `pending` means replay with the same id and arguments, `absent` means inspect the state first (it is not proof nothing happened).

## References

- [`references/worker-contract.md`](references/worker-contract.md): the workers' standing brief and the provenance rules.
- [`references/tmux-dispatch.md`](references/tmux-dispatch.md): host mechanics: polling, relays, merge gate verdicts, permissions, accounts, failure modes.
- [`references/herdr-dispatch.md`](references/herdr-dispatch.md): what herdr changes.
- [`solo-and-autopilot.md`](solo-and-autopilot.md): autopilot ticks and solo mode.
- [`architecture-review.md`](architecture-review.md): section 3, the periodic architecture review.
- [`reading-failures.md`](reading-failures.md): section 4, dead, stalled, unknown, unverifiable, red tests, red base.
- [`escalations-and-provenance.md`](escalations-and-provenance.md): section 5.
- [`merge-train-and-approval.md`](merge-train-and-approval.md): section 6, trains, approval policy, completing file-mode items.
- [`bounce-cap.md`](bounce-cap.md): section 7, bounce cap and re-review.
