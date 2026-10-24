# rota or a bare session

You can run a team of subagents from a plain Claude Code session with no rota and no skills. This page lines that up against what a rota round does, so you can decide which you need. It is the October 2026 comparison re-judged after six follow-ups landed (#897 to #902). The dated baseline is [Plain orchestrator comparison, October 2026](../contributing/plain-orchestrator-comparison-2026-10.md).

## The short version

rota wins where structure matters: the merged-tree gate before merge, measured proof rows, typed worker states, signed provenance, resumability, per-slot port and database isolation, bounce accounting. A bare session has no answer to any of those.

The baseline listed three places where the bare session came out ahead, plus its lower reading cost. Two of the three are closed, one is narrowed, and reading cost still holds:

- **Spec review on every PR.** Closed. `light` review depth now runs the Spec reviewer (#898).
- **Review weight follows the diff.** Closed. A one-way door, files outside the issue's named set or a matching risk path raise the depth (#897).
- **Behaviour exercised after a merge.** Narrowed. An issue can carry `## Try it` commands that the gate runs on the merged tree (#899). It is opt-in per issue, and `/rota-qa` is still off here (`ship.qa: false`, no `.rota/qa/`).
- **Reading cost.** Still holds, and the follow-ups added a little (`bounce-cap.md`, the scope-scout rules).

## The bare session, in short

Five roles, model fixed per role. The orchestrator is the session itself: it writes briefs, assigns, merges and never writes feature code inline. Haiku `Explore` agents scout files first. Opus plans only when an issue has a real design question. Sonnet implements, one issue per fresh agent in its own worktree, and the fix loop continues the same agent. A fresh reviewer reads the diff against the acceptance criteria and reruns the verify command. A cheap runner exercises the built thing on `main` after each merge.

## Side by side

"Changed" names the issue that moved the row since the baseline. Verdicts are against the merged tree and this repo's `.rota/config.json` (`ship.review: light`, `ship.qa: false`).

| Dimension | Bare session | rota | Stronger | Changed |
|---|---|---|---|---|
| Workers | Ephemeral subagents, one per issue; `SendMessage` continues one for a fix | Standing slots with worktree, tab and account, parked between issues; each runs its own explorer, implementer and reasoner subagents | rota | no change |
| Model routing | Fixed per role | Tier per issue (`--tier`, `--tier-reason`), harness and model from labels, several accounts | rota | no change |
| The brief | Hand-written in the prompt, lost once sent | The issue is the spec; `assign` points at the standing contract and quotes `## Out of scope`, `## Touches` and `## Try it`; the worker reads the whole thread and must dispute a wrong ticket; the PR lists the questions the worker answered itself under `## Assumptions` | rota | #902 adds `## Assumptions`; #899 adds `## Try it` |
| Worker self-check | Run the verify command, paste the output | Targeted checks only; RED and PASS rows measured by `rota proof record`; a deterministic pre-PR list; hook guard on forbidden git commands | rota | no change |
| Full suite | After every merge, on `main` | Once, before merge, on a scratch merged tree under a machine-wide lock; parallel full suites banned | rota | no change |
| Review: who | One fresh reviewer, model by risk | Spec and Standards in parallel at `full`, worse verdict wins; knowledge and decisions in the brief | rota | no change |
| Review: depth | From what the diff touches | `ship.review` sets the default, `lightBelow` and a `labels` map adjust it, and a one-way `Door:` line, files outside the issue's named set or a `paths` glob match raise it to `full` with no label. Here the default is `light` | **rota** (was bare) | #897 |
| Review: criteria check | A reviewer checks every PR against the acceptance criteria | The Spec reviewer checks the diff against the issue. `light` runs Spec on the `standard` tier and skips Standards, so every PR gets the criteria check; the gate, scaffolding scan and doclint cover the mechanical half of Standards | **rota** (was bare) | #898 |
| Review: evidence | Reviewer reruns the verify command | Reviewer is read-only; execution belongs to the gate and the proof rows; criteria coverage comes from `sha:check` proof | even | no change |
| Fix loop | Same implementer; cap 2, then the user | Relay to the holding slot or `round transfer`; findings are claims to verify; one counter across gate, relay and manual bounces; at the cap, audit the acceptance criteria first and hand ambiguous ones to a human, escalate the tier only when they are checkable | rota | #901 |
| Re-review | Same reviewer, scoped to findings plus a regression pass | `--since <sha>`, every earlier finding ADDRESSED or NOT ADDRESSED, a Deferred list that never moves the verdict | even | no change |
| QA | Cheap runner on `main` after every merge, always on | `/rota-qa` strategies, executable checks, an audit and a recorded verdict; opt-in and off here. Separately, an issue's `## Try it` commands run on the merged tree after `test.full` and `test.e2e`, in a scrubbed environment, with the transcript attached to the PR | **bare** (narrowed) | #899 |
| Waiting | Background agents and completion notices | `round watch`, typed events, eight slot states, autopilot | rota | no change |
| Provenance | None | Signed briefs and relays, HMAC for Codex, `## Approvals`, `## Rulings`, a `Door` line; the gate fails inflation and deflation | rota | no change |
| Parallel safety | Scout files, serialize overlaps, worktree per writer | File overlap plus declared `## Touches` scopes, per-slot ports and database suffix, reserved smoke sections, `best-of:2`; a `light` explorer scouts candidates that declare no scope and its inferred scope is compared warn-only | rota | #900 |
| Resumability | Branches and verdict files; the session's context is a cache | Lease, `workers.json`, ledger, handoff note, `reconcile` and `reap`, wind-down summary | rota | no change |
| Reading cost | One page of rules | SKILL.md, its references, the contract and `rounds.md`, loaded per step | **bare** | the new references add to it |

### What is left in the bare session's favour

- **After-merge behaviour.** The gate's `## Try it` run is only as good as the commands the issue author wrote, and an issue without the section gets none. A bare session's runner exercises every merge. Run `/rota-qa first-run` to scaffold strategies and set `ship.qa` if you want always-on coverage.
- **Reading cost.** A bare session fits its rules on one page. rota's are spread over many files and loaded per step.

One correction to the bare design: running the suite on `main` after every merge is the weaker rule. rota's merged-tree gate stops the red instead of reporting it.

## Does a parked slot start a fresh session?

Yes. A worker slot that finishes an issue is parked, not kept warm. The next task dispatch closes the slot's previous tab (`/exit`, then `tab close`) and opens a new one, so the next issue starts a fresh harness session with none of the earlier issue's context. The herdr dispatch notes call a fresh session per task "the only trustworthy reset". On herdr the workspace is recreated too. Only a `--relay` to a slot that is still working an issue reaches the same session, which is how review feedback arrives. A dispatch is refused (exit 4) if the reset guard finds the slot still holding work, or if `round.commands.claude` carries a resume flag. Exit 5 means the host is unavailable, the old session would not close, or a startup dialog refused input. Source: [`skills/references/herdr-dispatch.md`](../../skills/references/herdr-dispatch.md) and `internal/worker/dispatch.go`.

So stale context does not build up across issues. What carries over is the worktree on disk, the slot's registry entry and the knowledge files, none of which sit in the model's context.

## Related

- [Parallel rounds](parallel-rounds.md): the round, the gate and solo mode
- [Review and ship](review-and-ship.md): review depth and `ship.review`
- [Product QA](qa.md): `/rota-qa` and the `ship.qa` gate
