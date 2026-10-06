---
name: rota-work
description: Use when backlog items already exist and need implementation ("implement 42", "build these", or "implement [B07]" on the file backend), or with no argument to reconcile active work and pick the next item. A parallel round with standing workers is /rota-orchestrate, not this skill; an item not yet captured goes through /rota-capture first.
---

# rota-work

Main-session-driven implementation with per-task verification and commits. Subagents are in-process `Agent` calls that write files; the main session commits.

**Rounds are not this skill** (standing workers, merge gate, relays: `/rota-orchestrate` and the `rota round` verbs). If `work.dispatch` is `"tmux"` or `"herdr"`, this skill still dispatches subagents; say so once and continue, or point the user at `/rota-orchestrate` for a multi-issue round.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator`: main session model: planning and verification (default `opus`)
- `models.worker`: the `standard` tier: implementation subagents (default `sonnet`)
- `work.isolation`: `"branch"` (default) or `"worktree"`
- `work.mergeStrategy`: `"direct"` (default) or `"pr"`

## Flow

Copy this checklist and track your progress:

```
- [ ] Step 1 — Guard
- [ ] Step 2 — Clarify Ambiguous Briefs (only when needed)
- [ ] Step 2.5 — Detect Knowledge-vs-Correction Contradictions
- [ ] Step 3 — Name the Branch
- [ ] Step 4 — Plan Tasks
- [ ] Step 4.5 — Umbrella Pre-Flight
- [ ] Step 5 — Create Branch or Worktree
- [ ] Step 6 — Dispatch Subagents
- [ ] Step 7 — Verify Each Completion
- [ ] Step 7.5 — Commit per Task (main session)
- [ ] Step 8 — Sequential Waves
- [ ] Step 8.5 — Sweep Tool-Generated Siblings
- [ ] Step 9 — Close the Items
- [ ] Step 10 — Merge or PR
- [ ] Step 11 — Update Status and Report
- [ ] Step 12 — After-Work QA
- [ ] Step 13 — After the Cycle
```

## No-Argument Mode (reconcile, suggest, then work)

With no item, ID or brief, read [`no-argument-mode.md`](no-argument-mode.md) and follow it: it reconciles active streams, suggests one item and continues into Step 1.

## Preview Mode (`--preview <target>`)

`--preview <target>` (flag anywhere in the args) prints a read-only approach peek and stops: no writes, no commits, no status. Procedure in [`references/work-preview.md`](references/work-preview.md); context per [`references/context-load-protocol.md`](references/context-load-protocol.md) and [`references/knowledge-consult.md`](references/knowledge-consult.md).

## Step 1 — Guard

```bash
rota git guard clean --context "/rota-work"
```

Exit 0: continue. Exit 3: not a repo, surface and stop. Exit 1 (dirty tree): when every dirty path is a tool-generated sibling, sweep them into a `chore:` commit and continue; any user change stops with the guard's message. Procedure and sibling list: [`sibling-sweep.md`](sibling-sweep.md).

## Step 2 — Clarify Ambiguous Briefs (only when needed)

Only when the brief is too thin to plan concrete tasks (scope hits 2+ incompatible areas, a requirement could yield opposite implementations, captured items imply different orderings), ask one `AskUserQuestion` of 1-3 questions before touching code. Otherwise proceed. Don't ask to confirm understanding, for preferences inferable from `KNOWLEDGE.md` or the codebase, or for style inside an agreed scope.

Each question gets a short `header`, options that map to concrete plans with the likeliest marked `(Recommended)`, and `multiSelect: true` for conflicting items. On ambiguity, default to Recommended and state it in the dispatch brief.

## Step 2.5 — Detect Knowledge-vs-Correction Contradictions

When Step 2 produced a non-Recommended answer that pushes back on a stated assumption, read [`contradictions.md`](contradictions.md). Skip silently otherwise.

## Step 3 — Name the Branch

Pick a descriptive branch name (`rota/quick-switch`). Step 5 creates the branch and registers it once. When items carry `Repos:` (umbrella mode) or the backend is issues, read [`umbrella-and-issue-mode.md`](umbrella-and-issue-mode.md) at each step it names.

## Step 4 — Plan Tasks

**Plan-as-artifact check (first, including resume).** For every item or slice, read [`plan-artifact.md`](plan-artifact.md) and resolve its supported backend key before decomposing. An issue plan needs no milestone. Use the stored tasks, constraints and verify steps when a plan exists.

1. **Consult knowledge and decisions** with the canonical K+D pattern (`references/knowledge-consult.md`), topics inferred from the planned work. Run `rota glossary read <terms in the item>…` too; call out drift from a canonical term. Carry matches into Step 6 briefs as `**Known gotchas:**` (relevant bullets only) and `**Hard boundaries:**` (full entries: rule, *Why*, **Forbids**, **Permits**). If a planned task would violate a decision, **stop and surface it** before dispatching. Run `rota map stats --cap` (one-line nudge; never blocks).

   > **REQUIRED — Register hits on consumed bullets.** After writing the briefs, apply *Hit-register after consumption* in `references/knowledge-consult.md`: one `rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"` per bullet that landed in a brief's `**Known gotchas:**`, all in one parallel batch. Bullets pruned before the briefs earn no credit.

2. Identify tasks from the loaded plan, preserving its key and task numbers; only decompose ad hoc when no plan exists. Include files, what changes and acceptance criteria.
3. **Resuming** (a branch with commits past the base): apply the resume rule in [`references/task-ledger.md`](references/task-ledger.md) to those tasks, skip committed task IDs and plan only the unfinished tasks. Do not silently re-decompose or renumber a saved plan.
4. **Absorb file collisions** before grouping: any two tasks whose modified-file sets intersect, and shared-symbol changes that disjoint file sets hide. Rules and the rename check in [`references/work-wave-planning.md`](references/work-wave-planning.md).
5. Group into dependency waves: wave 1 is independent files (parallel); wave 2+ depends on earlier output.

## Step 4.5 — Umbrella Pre-Flight

Skip when `rota repo umbrella` exits 1 (single-repo). When it exits 0, read [`umbrella-and-issue-mode.md`](umbrella-and-issue-mode.md), *Step 4.5*, and carry the resolved repo set into Steps 5 and 10.

## Step 5 — Create Branch or Worktree

Use the Step 3 name; `references/isolation-patterns.md` has the pattern per `work.isolation`. Single-repo, branch isolation:

```bash
git checkout -b <branch>
rota status add <branch> --items <ID>[,<ID>...]
```

The only `rota status add` of the cycle: create the branch or worktree first, then register once (`--worktree <path>` for a worktree). Idempotent on `(branch, repo)`.

When umbrella, read *Step 5* in `umbrella-and-issue-mode.md` (registration, branches). When `backlog.backend` is `"issues"`, read the same section for the claim flow before planning tasks.

The main session stays at the repo root; subagents `cd` into their assigned directory first and use absolute paths.

## Step 6 — Dispatch Subagents

Dispatch one `standard`-tier subagent per independent task, all in one message; don't announce. Subagents write files and never stage or commit (no `.git/index` races). Fill the brief from [`dispatch-brief.md`](dispatch-brief.md). Umbrella: read *Step 6* in `umbrella-and-issue-mode.md`.

## Step 7 — Verify Each Completion

Verify silently. Trust the diff, not the subagent's narrative.

1. `git status --porcelain`, then `git diff` for the files the subagent reported.
2. Read the modified files: do they match the brief?
3. Structural checks: grep for expected patterns, no regressions.
4. **Rename validation.** Re-run `git grep -l "<old-name>" -- <scope>`; files outside the subagent's set get a fix-up dispatch before staging.
5. Claim-weight check on gap-fills, and treat a subagent's dispute of its brief as a FAIL on the plan (see `references/work-wave-planning.md`, *Verifying a completion*).
6. When `test.fast` is set, run `rota test run fast`; a FAIL is a FAIL. Use it as the PASS proof `--check`.

**PASS** → move on. **FAIL** → dispatch a fix agent and re-verify; repeat until PASS, and surface only persistent failures. Commit (Step 7.5) only on PASS.

**Record proof** (facts about what ran, not acceptance). Per task:

- **PASS:** one row per item the task resolves: `rota proof add <ID> --check "<verify command or grep>" --result PASS --evidence "<output line or path>" [--sha <task-commit>]`.
- **Persistent FAIL:** record it with `--result FAIL`.
- **Behavior task:** first record the subagent's reported RED run: `rota proof add <ID> --check "<test command>" --result FAIL --evidence "<failing line>" --sha <sha before the change>`, then the PASS row after. A subagent that reports no RED gets a fix dispatch (a FAIL above).
- **Docs or skill-only task:** no RED. Put `no test seam: docs/skill change` in the PASS row's `--check`.
- **Acceptance:** `rota item complete` (Step 9) writes it and exits 4 when an item has no proof. Never pass `--no-proof` on your own; an unproven item stays open and is surfaced.

Issue mode keeps the rows in the item's proof note.

## Step 7.5 — Commit per Task (main session)

One commit per task, sequential, in dispatch order, staging only that task's files:

```bash
git add <task-N-files>
git commit -m "<suggested-message-from-task-N-brief>" -m "Task: <key>/<N>"
```

- Every task commit ends with the `Task: <key>/<N>` trailer ([`references/task-ledger.md`](references/task-ledger.md)); a resumed session skips finished tasks by it.
- Stage exactly the files named in the task's brief. Never `git add -A` or `git add .`.
- Use the brief's suggested message verbatim, unless a FAIL→re-dispatch loop changed what landed.
- **Same-file carve-out.** Two parallel subagents editing different ranges of one file get ONE commit naming both task IDs; granularity lives in the message.
- Worktree isolation: commit against the worktree's index (`git -C <worktree-path>`). Umbrella: *Step 7.5* in `umbrella-and-issue-mode.md`.


## Step 8 — Sequential Waves

Wait for wave 1 to complete, verify and commit (Step 7.5), then dispatch wave 2 with updated context.

## Step 8.5 — Sweep Tool-Generated Siblings

When the toolchain left untracked siblings after the task commits, sweep them per [`sibling-sweep.md`](sibling-sweep.md), *Step 8.5*. Otherwise skip.

## Step 9 — Close the Items

**Issue backend:** skip; the issue closes when its PR merges (`umbrella-and-issue-mode.md`, *Step 9*).

**File backend:** follow [`file-backend-close.md`](file-backend-close.md) per resolved item: it tombstones the item's plan, runs `rota item complete <ID> --commit <commit-hash>` (the only write to the backlog), then commits `.rota/BACKLOG.md` and the removed plan by name.

## Step 10 — Merge or PR

`work.mergeStrategy` picks `rota ship merge` (direct, the default and when unset) or `rota ship pr`. Don't ask. Invocations (umbrella `--repo`, exit-4 verdict, merge-approval handling) are in `rota-ship` Steps 6a/6b. Opening a PR is a manual gate. The issue backend forces the PR path: read *Step 10* in `umbrella-and-issue-mode.md` and `references/issue-mode.md`.

## Step 11 — Update Status and Report

```bash
rota status rm <branch>                  # umbrella: add --repo <repo>
```

Umbrella: see *Step 11* in `umbrella-and-issue-mode.md`. Then one compact summary, no plan recap or verification list: `Done — merged <branch> into main.`, one `- #<n> <title> — <what changed>` line per item, `Commit: <sha>`.

## Step 12 — After-Work QA

When `rota config show qa.afterWork` is `true`, read [`after-work-qa.md`](after-work-qa.md). `false` (default) → skip silently.

## Step 13 — After the Cycle

One line, only when `references/post-cycle-trigger-gate.md` fires: *"Run `/rota-learn` to save what this cycle taught; `/rota-decide`, `/rota-ship --docs` and `/rota-refactor` are there when you want them."* Never auto-invoke any of them. If the cycle touched files in a `.rota/map/<name>.md` entry's `Key files / dirs`, bump its `touched:` and run `rota map index`.

## Gates: Thought → Reality

| Thought | Reality |
|---|---|
| "The subagent said it passed." | Step 7 trusts the diff: `git status`, `git diff` and the read files, never the subagent's narrative. |
| "The check is obvious, skip the proof row." | `rota item complete` exits 4 without one. An unproven item stays open and is surfaced. |
| "The new test passes, that's enough." | A test that never failed may not test the change. A behavior task needs a RED run first, recorded as a FAIL row, then the PASS row. |
| "Pass `--no-proof`, the code is fine." | Never on your own. The row records what ran; the flag records that nothing did. |
| "A subagent disputing its brief is noise." | A dispute is a FAIL on the plan. Fix the plan, then re-dispatch. |
| "Open the PR now, the user wants it shipped." | Opening a PR is a manual gate whatever `autonomy.level` says (Step 10, `/rota-ship` Step 6a). |
| "Run the full smoke after each task." | Per-task checks stay structural. The full suite runs in `/rota-ship` and `/rota-review`. |

## Key Principles

- **No noise.** Report results, not process.
- **The main session owns `.rota/` state** (`status.json`, backlog via `rota item` verbs).
- **Never work directly on main.**

## References

- [`references/knowledge-consult.md`](references/knowledge-consult.md): canonical K+D query pattern and hit-register rule.
- [`references/context-load-protocol.md`](references/context-load-protocol.md): shared parallel context load (Preview Mode).
- [`references/work-preview.md`](references/work-preview.md): `--preview` procedure.
- [`references/task-ledger.md`](references/task-ledger.md): `Task:` trailer and resume rule.
- [`references/work-wave-planning.md`](references/work-wave-planning.md): file collisions, brief-writing rules, verifying a completion.
- [`references/isolation-patterns.md`](references/isolation-patterns.md): branch, worktree and umbrella layouts.
- [`references/issue-mode.md`](references/issue-mode.md): issue backend: claim, proof note, PR path.
- [`references/umbrella-mode.md`](references/umbrella-mode.md): umbrella registry and `Repos:` semantics.
- [`references/post-cycle-trigger-gate.md`](references/post-cycle-trigger-gate.md): when the Step 13 learn nudge fires.
- [`references/worker-contract.md`](references/worker-contract.md): round-worker rules and review-feedback handling.
- [`plan-artifact.md`](plan-artifact.md): Step 4 backend keys, saved tasks and resume identity for item and slice plans.
- [`no-argument-mode.md`](no-argument-mode.md): reconcile, suggest, then work.
- [`sibling-sweep.md`](sibling-sweep.md): Step 1 / 8.5 tool-generated sibling sweep.
- [`contradictions.md`](contradictions.md): Step 2.5 contradiction logging.
- [`umbrella-and-issue-mode.md`](umbrella-and-issue-mode.md): umbrella and issue-backend branches of Steps 3–11.
- [`dispatch-brief.md`](dispatch-brief.md): Step 6 brief template.
- [`file-backend-close.md`](file-backend-close.md): Step 9 on the file backend.
- [`after-work-qa.md`](after-work-qa.md): Step 12.
