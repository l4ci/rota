# tmux worker dispatch

Host mechanics for the `rota worker` verbs under tmux, and the judgment they do not enforce. Used by `/rota-orchestrate` rounds; `rota round start` picks the host and `rota round wait` / `assign` / `wind-down` drive the verbs, so a round rarely calls them by hand. `/rota-work` does not use this file: it dispatches in-process subagents.

Each worker is **its own Claude Code session**, in its own `git worktree`, on its own branch, opening a PR. That buys a per-worker context window and a channel a human can talk into, and costs the failure modes below, each paid for by a real round.

Verbs: `rota worker pool`, `rota worker dispatch`, `rota worker poll`, `rota worker gate`. They drive tmux through the `rota` binary's tmux host.

herdr runs the same workers in tabs; see [`herdr-dispatch.md`](herdr-dispatch.md), which covers only what herdr changes. The sections below apply to both hosts.

## Being inside tmux is a precondition

A worker's value is that it can idle on a question and a human can answer in its pane. From a terminal outside tmux the windows land in a **detached session nobody is attached to**, every escalation goes unanswered, and the host degrades into a slower subagent mode. Detection is `$TMUX` (`rota worker session check`: exit 0 inside, 1 outside); `tmux has-session` is the wrong test, since it says whether a session *exists*, not whether *we are in it*. Outside tmux, `rota worker session ensure --body-file <path>` creates the session and an `operator` window running `work.operatorCommand` (`claude --continue --model <orchestrator>` by default). The caller **must stop after a successful `ensure`**: two orchestrators on one pool dispatch the same task twice. It is idempotent on the operator window.

## The worker contract

Shared by both hosts and kept in [`worker-contract.md`](worker-contract.md): the standing brief every worker reads, the `ROTA-BLOCKED` / `ROTA-DONE` sentinels the poll below routes on, and the provenance rules. Read it before dispatching.

## Polling

`rota worker poll` classifies each slot; read `data.slots[].state` (`blocked`, `done`, `busy`, `needs-permission`, `limited`, `dead`, `idle`, `unknown`) and `evidence`. Sentinels outrank movement and `rota round wait` blocks on the same classification. The state-to-action routing lives in `rota-orchestrate/SKILL.md` (sections 3 and 4); the verb is specified in the contract (A7). Two judgment rules the verb cannot make:

**A bare `API Error … Overloaded` on a static pane is a headstone, not a pulse.** The session took its dispatch, retried to exhaustion, and died, often without reading the task. Only `Retrying in` proves a retry is in flight. A watcher that treats them alike waits forever on a dead worker.

**Re-dispatch at most twice.** If a slot dies on the same brief a second time, the fault is that session, not the API: hand the task to a different slot.

## Escalating and relaying

A `blocked` slot carries the worker's question in `evidence`. Answer it or escalate it per `rota-orchestrate/SKILL.md` section 5 (in prose, never a blocking picker), then relay with `rota worker dispatch <n> --body-file <answer> --relay`.

`--relay` signs the text and logs it in the slot's `relays[]`; why, and what the gate cross-checks, is in [provenance](worker-contract.md#provenance). Read every PR body for the channel named, not merely for whether a citation exists.

## The merge gate

Worker-owned branches make integration git-native and bring back the failure class per-branch verification structurally cannot see. Two workers with **disjoint file sets** each verify honestly and go green; git reports a clean merge because nothing textually overlaps; the merged tree is broken. The shapes to expect:

- a symbol one worker **widens or re-types** while another adds a fresh call to it;
- a constant, key, or output field one worker **stops emitting** while another starts depending on it.

Neither author can see it — the conflicting change never existed in their tree. Git's mergeability answer is about text, not meaning.

`rota worker gate <n> --base <branch>` runs freshness, merge and re-verify on the merged tree (`refactor.verifyCommands`); read `data.verdict` under `--json`. `approval-required` (exit 4) means `ship.mergeApproval` wants a human: ask, then re-gate with `--confirm --confirm-note "<answer>"`. Judgment the verdicts do not make:

- `stale`: the gate already merges a behind-the-base branch itself when the merge is clean and no file changed on both sides, so a `stale` verdict means a conflict, a shared file or work already on the base. Bounce it to the slot with a summary of what landed, **once**. The gate counts bounces per item and, at `round.maxBounces` (default 3), parks the item `needs-human` (`data.parked`); do not re-dispatch a parked item. With several slots in flight the owner often goes stale again while re-syncing, and a bounce loop is worse than resolving it yourself in the worker's worktree and documenting that on the PR.
- `merge-failed` (a conflict): route to the slot that owns the branch context; never resolve a cross-worker semantic conflict blind.
- `verify-failed`: the merged tree is broken and the merge already landed. Fix forward on the base branch; the owning slot has usually moved on, and small orphaned-reference fixes are the orchestrator's to make.
- `data.verifySkipped: true` means no command gated the merged tree. A project on this backend should set `refactor.verifyCommands`; otherwise the re-verify is a structural diff review and nothing more.

Batching: the gate is the one full run, so nothing re-verifies after it. Gate each PR individually by default. A group of PRs with genuinely disjoint file sets can be merged and gated once; never batch when a PR touches a shared module, widens a shared type, or renames a shared symbol.

## Permissions

The two roles run at different trust levels, on purpose:

| | Default | Why |
|---|---|---|
| Worker | `claude --model <worker> --dangerously-skip-permissions` | Briefed to commit, open a PR and run tests with nobody in the pane to answer a prompt |
| Operator | `claude --continue --model <orchestrator> --permission-mode auto` | Performs the merges, talks to the user, and is the one window a human is actually watching |

**Why workers skip the gate.** A narrower mode does not make a worker safer, it makes it stop. `acceptEdits` auto-approves file edits only, so a worker briefed by the contract writes its files and then blocks on its first `git add` — forever, because the prompt is addressed to a human who may not be attached. An unattended session that halts halfway through a task with a dirty worktree is not a safer outcome than one that finishes.

**What bounds a worker is scope, not gating.** Each runs on a throwaway branch in its own worktree; nothing it produces reaches the base branch until `rota worker gate` has checked freshness, merged, and re-verified the merged tree. The branch is disposable and `rota worker pool reap` deletes it. That containment is worth stating precisely, because it has a real hole: worktree confinement is a **contract, not a sandbox**. The worker contract says stay in your worktree and use worktree-rooted paths, and an absolute path under the repo root still reaches the main checkout. Skipped permissions mean nothing stops that but the instruction.

**The operator keeps `auto`** because its blast radius is different: it merges into the branch the cycle ships from, and a human is watching that window, so a prompt there gets answered rather than stranding the run.

**`needs-permission` stays in the classifier** even though the default should never trigger it. It covers the case where `work.workerCommand` has been narrowed — a stalled worker then reports the stall instead of looking idle. One slot stalling is a prompt to approve; every slot stalling means the mode is too narrow for the briefs.

**Narrowing it.** Point `work.workerCommand` at a settings file that allowlists just what the contract needs (`claude --settings <file>` with `permissions.allow` covering `git add`, `git commit`, `gh pr create`, and the project's test runner). Workers get autonomy for the operations they were briefed to perform and nothing else, at the cost of maintaining the list.

**Multi-account caveat.** Entering skip-permissions mode can itself prompt for confirmation, and that acknowledgement is per config dir. A slot pointed at a **fresh** `CLAUDE_CONFIG_DIR` (see *Accounts*) may therefore stall at boot on a dir where the ambient one is fine. Set `skipDangerousModePermissionPrompt: true` in each account's `settings.json` when provisioning it, or the first dispatch to that slot reports `needs-permission` before doing any work.

## Accounts

`work.accounts` maps slots to independent `CLAUDE_CONFIG_DIR`s; empty means every slot inherits the ambient config dir. Use `rota worker account list` (per-account headroom and verdict), `account pick [--exclude <names>]` and `account assign <slot> --account <name>`; `rota worker pool init` assigns at creation. Contract: A7.

**`rota` never refreshes credentials.** An expired token reports `unknown` and falls through to rotation. Refreshing would mutate state a live Claude Code session owns, and racing it risks logging the user out.

**Never answer an "Add funds" prompt.** A limited session may offer *Stop and wait* vs *Add funds*; the second spends real money and is never the orchestrator's to pick. `rota worker poll` flags it in the evidence string: escalate to the human and wait. Meanwhile keep gating, merging and verifying finished slots, because local shell work does not consume the LLM window.

## Other failure modes worth knowing

- **`/clear` does not reliably reset a session.** It can land as a literal chat message with the context still loaded. `rota worker dispatch` kills and recreates the window for every task brief; a fresh session starts at 0 context. Do not try to reuse a window by clearing it. The kill is confirmed (window gone, its pane PID exited) before the replacement spawns, and the worktree is checked by `rota worker reset` first; see `rota-orchestrate/SKILL.md` section 2. A `--relay` is the exception: it goes into the running session, because the worker that asked the question is the one that needs the answer.
- **A pasted brief may not submit.** A long prompt arrives as a collapsed paste chip whose trailing Enter is swallowed. `rota worker dispatch` sends Enter as a separate keypress and then **confirms pickup** by re-capturing the pane, retrying up to 4 times before failing with exit 6 (never submitted, safe to resend). Never assume the first Enter landed.
- **Load is a first-class failure mode.** Slots contend for one box. Beyond roughly one slot per two cores, CPU-bound tests with fixed time budgets start failing on elapsed time rather than on truth, and each false red costs a re-measurement to disprove. That is why the worker contract says targeted tests only, and why `work.workerSlots` defaults to 3. Never "fix" a load-induced red by raising a timeout — a bigger fixed number just fails at a higher load and reports genuine regressions more slowly.
- **A timeout is not a failure of the thing under test.** It says the assertion never ran. Read the output before forming a theory.
- **Bracket every `pgrep`/`pkill` pattern** (`[v]itest`, not `vitest`). An unbracketed pattern matches the argv of the shell running it: as `pkill` that is a confusing exit 144, but inside a wait-loop it never terminates at all — the loop matches itself and spins forever while the pane reports work still running.
- **Never trust a piped command's exit code.** `cmd | tail -40` reports `tail`'s status. Read the result line, or run unpiped.

## See also

- [`references/subagent-dispatch.md`](subagent-dispatch.md) — when to dispatch at all, and the brief shape both backends share.
