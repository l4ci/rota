# Plain orchestrator comparison, October 2026

What a Claude Code session with no rota and no skills would do to run a team of subagents, lined up against what rota's rounds do. Written 2026-10-19 by the maintainer's orchestrator session (Fable 5.1) from its own first-principles design and a read of `skills/rota-orchestrate`, `skills/references/worker-contract.md`, `skills/rota-review`, `skills/rota-qa`, `docs/contributing/rounds.md` and this repo's `.rota/config.json`. The six suggestions became issues #897 to #902, in the order below; #903 re-ran this comparison once they landed and moved the result into the user docs.

This page is the dated baseline the follow-ups were measured against and is not updated. The re-run after #897 to #902 landed is [rota or a bare session](../usage/rota-vs-plain-session.md), and it answers the open question at the bottom of this page.

## Summary

rota wins where structure matters: the merged-tree gate before merge, measured proof rows, typed worker states, provenance, resumability, per-slot port and database isolation, bounce accounting. A plain session has no answer to any of those.

The plain default wins in three places, and for one reason: it has no config, so nothing can be switched off. Every PR gets a model check against its acceptance criteria. Behaviour is exercised after every merge. Review weight follows what the diff touches, not what someone labelled.

## The plain default, in short

Five roles, model fixed per role. The orchestrator (the session itself) writes briefs, assigns, merges and never writes feature code inline. Haiku `Explore` agents scout files before a brief is written. Opus plans only when an issue has a real design question. Sonnet implements, one issue per fresh agent in its own worktree, and the fix loop continues the same agent. A fresh reviewer, never a fork of the orchestrator, reads the diff against the acceptance criteria and reruns the verify command; Sonnet for mechanical diffs, Opus when invariants are involved, two independent reviewers for irreversible changes. A cheap runner exercises the built thing on `main` after each merge.

The brief is the whole interface to an implementer: goal, acceptance criteria quoted from the issue, in- and out-of-scope files, the verify command, and a report shape that demands pasted test output, the criteria it did not satisfy and the questions it had to guess.

## Side by side

| Dimension | Plain default | rota | Stronger |
|---|---|---|---|
| Workers | Ephemeral subagents, one per issue; `SendMessage` continues one for a fix | Standing slots with worktree, tab and account, parked between issues; each worker runs its own `rota-explorer` / `rota-implementer` / `rota-reasoner` subagents | rota |
| Model routing | Fixed per role | Tier per issue (`--tier light\|standard\|heavy`, `--tier-reason` above default), harness and model from issue labels, several accounts | rota |
| The brief | Hand-written in the prompt, lost once sent | The issue is the spec; `assign` points at the standing contract and quotes `## Out of scope` and `## Touches`; the worker reads the whole thread and must dispute a wrong ticket | rota |
| Worker self-check | Run the verify command, paste the output | Targeted checks only; RED/PASS rows measured by `rota proof record`; deterministic pre-PR list (scaffolding scan, stale records, file scope, done gate, `Closes #N`); hook guard on forbidden git commands | rota |
| Full suite | After every merge, on `main` | Once, before merge, on a scratch merged tree under a machine-wide lock; never re-run after merge; parallel full suites banned | rota |
| Review: who | One fresh reviewer, model by risk | Spec (orchestrator model) and Standards (standard tier) in parallel, worse verdict wins; KNOWLEDGE and DECISIONS in the brief | rota |
| Review: depth | From what the diff touches | Policy of default, `lightBelow` line count and a `labels` map; this repo runs `light`, Standards only, Spec skipped | plain |
| Review: evidence | Reviewer reruns the verify command | Reviewer is read-only; execution is the gate's and the proof rows'; AC coverage from `sha:check` proof | even |
| Fix loop | Same implementer; cap 2, then the user | Relay to the holding slot or `round transfer`; findings are claims to verify; counter across gate, relay and manual bounces; cap 3, then a stronger tier or human | rota |
| Re-review | Same reviewer, scoped to findings plus a regression pass | `--since <sha>`, every earlier finding ADDRESSED or NOT ADDRESSED, a Deferred list that never moves the verdict | even |
| QA | Cheap runner on `main` after every merge, always on | `/rota-qa` strategies, executable checks, Opus audit, recorded verdict; here `ship.qa: false`, `.rota/qa/` empty, round PRs skip it by rule | plain |
| Waiting | Background agents and completion notices | `round watch` armed and re-armed; typed events; slot states done, idle, blocked, needs-permission, limited, dead, unknown, unverifiable; autopilot | rota |
| Provenance | None | Signed briefs and relays, HMAC for Codex, `## Approvals`, `## Rulings`, `Door` line, gate fails inflation and deflation | rota |
| Parallel safety | Scout files, serialize overlaps, worktree per writer | File overlap plus declared `## Touches` scopes (#575, `round.scopeOverlap: warn`), per-slot ports and DB suffix, reserved smoke sections, best-of:2 | rota |
| Resumability | Branches and verdict files; the session's context is a cache | Lease, `workers.json`, ledger, handoff note on `round start`, `reconcile` and `reap`, wind-down summary | rota |
| Reading cost | One page of rules | SKILL.md, nine references, the contract and `rounds.md`, loaded per step | plain |

## Where the plain default is stronger

- Every PR is checked against its acceptance criteria by a model. rota does this at `full` depth only; this repo runs `light`, which keeps Standards and drops Spec.
- Behaviour is exercised after every merge. rota's QA is richer, opt-in, unscaffolded here, and skipped for round PRs.
- Risk is read off the diff. A migration gets a heavier review whether or not someone remembered `risk:high`, and whether or not the diff is short.

One correction to the plain design itself: a suite on `main` after every merge is the weaker rule. rota's merged-tree gate stops the red instead of reporting it.

## Suggestions

Ranked by how much of the plain default's advantage each closes, cheapest first. Size: S under a day, M a few days, L a round or more.

1. **Let the diff raise its own review depth** (S). `rota review depth` reads labels and changed lines (`internal/config/reviewdepth.go`, #581). The worker already writes `Door: one-way` and the gate already diffs file scope. A one-way door, files outside the issue's named set, or a touched path matching a configured risk glob should promote `light` to `full` without a label.
2. **Make `light` keep Spec, not Standards** (S). When only one reviewer runs, the question that matters for a round is whether the diff builds what the issue says. gofmt, vet, the scaffolding scan and doclint already cover the mechanical half of Standards; spec drift is covered by nothing else. Run Spec on the `standard` tier at `light` to keep the cost.
3. **A cheap post-gate behaviour run for round PRs** (M). Not `/rota-qa`. An issue carries the commands a user would run and what to expect; the gate runs them on the merged tree after `test.full`, attaches the transcript to the PR, and fails only on a crash or an explicit mismatch. It is the one check that catches "tests green, feature does not do the thing."
4. **Scout the candidates that declare no scope** (S). Scope overlap works when an issue carries `## Touches` or a Subsystem; the rest fall back to file paths. A `light` explorer per undeclared candidate, recorded as inferred scope and compared warn-only, gives the check what Haiku scouts give the plain default.
5. **At the bounce cap, audit the acceptance criteria before buying a bigger model** (S). The default at `round.maxBounces` is a transfer to a stronger tier. Three misses on one issue more often mean the criteria are ambiguous than that Sonnet is too weak. Audit the criteria first; route to human when they are not checkable as written; escalate tier only when they are.
6. **Record the questions the worker answered itself** (S). `## Rulings` holds own calls with their cost. The softer class, questions the issue was silent on that the worker answered from the code, goes unrecorded and surfaces in review. One PR-body section in the contract; no verb change.

## Checked and still open

Checked after the first draft: depth already takes diff size (#581); overlap already takes declared scopes (#575); `round.autopilot` is off here; `assign` carries settled answers into the brief through `--settled-file` (formerly `--body-file`). Still unverified: whether a parked slot's next dispatch starts a fresh harness session or continues the old one, which decides whether stale context accumulates across issues.
