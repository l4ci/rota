---
name: rota-work
description: Use when backlog items already exist and need implementation ("implement 42", "build these", or "implement [B07]" on the file backend), or with no argument to reconcile active work and pick the next item. A parallel round with standing workers is /rota-orchestrate, not this skill; an item not yet captured goes through /rota-capture first.
---

# rota-work

Orchestrator-driven implementation with per-task verification and commits. Workers are in-process `Agent` subagents that write files; the orchestrator commits.

**Rounds are not this skill.** Standing workers in their own worktrees and host tabs (tmux or herdr), PRs behind a merge gate, relays, slot reclaim: that is `/rota-orchestrate` and the `rota round` verbs. If `work.dispatch` is `"tmux"` or `"herdr"`, this skill still dispatches subagents; say so once and continue, or point the user at `/rota-orchestrate` for a multi-issue round.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model for planning and verification (default `opus`)
- `models.worker` — model for implementation subagents (default `sonnet`)
- `work.isolation` — `"branch"` (default) or `"worktree"`
- `work.mergeStrategy` — `"direct"` (default) or `"pr"`

## Flow

```
Guard → Clarify (if needed) → Status → Plan → Isolate → Dispatch → Verify → Commit → Close → Merge/PR → Status
```

Use it when the user describes a task, feature or list of improvements with enough spec to act on, decomposable into 2+ independent pieces.

## No-Argument Mode (reconcile, suggest, then work)

`/rota-work` with no item, ID or brief reconciles what is in flight, shows the backlog, suggests one item and continues into Step 1 with it.

**1. Reconcile active streams.** `rota status show` lists them. Git is the source of truth over `status.json`. For each stream: a branch that no longer exists is dropped with `rota status rm <branch> [--repo <repo>]`; otherwise note whether it has commits past the base (`rota git base`) and whether `rota status handoff <branch> [--repo <repo>]` returns a `/rota-pause` note (read its Stage, Next planned step and Current hypothesis). A note for a paused orchestrator also carries round state: surface it and point at `/rota-orchestrate`, which resumes from `rota round status`. Resolve each stream with `AskUserQuestion`, Recommended first:

- handoff present: resume with the note as the brief (Recommended); leave it for later; abandon.
- commits, no handoff: ship via `/rota-ship` (Recommended); resume; leave as-is.
- no commits, no handoff: resume (Recommended); abandon; leave as-is.

Resume continues on the existing branch. For a stream with commits, read the task ledger (`references/task-ledger.md`) and carry only the unfinished tasks into Step 4; a handoff note's *Next planned step* never re-dispatches a task the ledger shows finished. Abandon is `git branch -D <branch>` plus `rota status rm`, and removes that stream's handoff note. A note is deleted only when its stream is resumed or abandoned.

**2. Orient.** Run `rota backlog archive --days 5` (silent), `rota milestone active` and one `rota backlog ids --milestone <MID>` per active milestone, then `rota backlog list`. Print the list in full, every row and section: the table is the point of the mode and is exempt from length limits. Prefix it with `Active milestones: <ids and titles>` when there are any. Advisories, never blocking: for each ID in `rota backlog drift --json` print `<ID> looks shipped on <hash> but still open`, and suggest `rota proof add` then `rota item complete <ID> --commit <hash>` (or `--no-proof`), never auto-complete. Print `stale: map=N, knowledge=M, todo=K` from `rota backlog stale` (zero kinds dropped) and `empty-active: <MID>` for an active milestone with no open items. `rota backlog drift` is file-backend only; skip it on the issue backend.

**3. Suggest one item.** Order: P0 bugs; clusters holding a blocking bug; quick wins (Cosmetic, P2); the highest-impact P1; blocking tasks (`Related:`); Minor features; Major features only when nothing else is pending or the user asks. Milestone bias at every level except P0: items tagged to an active milestone first, then untagged, then non-active milestones. Items labelled `changes-requested` (issue backend) rank right after P0. Skip items already active. Print `Suggested next: <ID> Title (tag)` and one sentence why. For a `[Major]` feature or `[P0]` bug with no design (`rota design show <ID>`), add *"consider `/rota-brainstorm <ID>` before this"*.

**4. Confirm.** `AskUserQuestion`: Start (Recommended); Peek approach first (`--preview`, offered for Major, P0/P1 or a batch); Write a plan first (`/rota-plan`, offered for a Major item tagged to a milestone with no plan (`rota plan show`)); Pick different items; Stop here. "Other" text is the item spec.

On a terminal path (Stop here, or an empty backlog) run `rota release pending --json` and, when `shouldNudge` is true, print its `message` as one line. Skip it when work continues and when there is no tag yet. Pass the item's text into Step 1 so it is not re-read.

## Preview Mode (`--preview <target>`)

`/rota-work --preview <target>` (the flag anywhere in the args) prints a read-only approach peek and stops: no writes, no commits, no status. Procedure and peek template in [`references/work-preview.md`](references/work-preview.md).

## Step 1 — Guard

```bash
rota git guard clean --context "/rota-work"
```

Exit 0 = clean, continue. Exit 3 = not a repo, surface and stop. Exit 1 (dirty tree): if every dirty path is a tool-generated sibling (Godot `.gd.uid`, `Package.resolved`, `.DS_Store`...), sweep them into a `chore:` commit and continue; any user change stops with the guard's message. Patterns and commands in [`references/work-toolchain-siblings.md`](references/work-toolchain-siblings.md). On a fresh `git init` with no commits the guard points at a `chore: import initial files` baseline; run it and re-invoke.

## Step 2 — Clarify Ambiguous Briefs (only when needed)

If, and only if, the brief is too thin to plan concrete tasks (missing scope, conflicting requirements, two equally plausible readings), resolve it with one `AskUserQuestion` call of 1-3 questions before touching code. Otherwise skip: the default is to proceed.

Ask when the scope hits 2+ incompatible areas, a requirement is vague enough to yield opposite implementations (*"add sorting"*: which direction, which columns), or captured items imply different orderings. Don't ask to confirm you understood, for preferences inferable from `KNOWLEDGE.md` or the codebase, or for style inside an agreed scope.

Each question gets a short `header`, options that map to concrete plans with the likeliest marked `(Recommended)`, and `multiSelect: true` for conflicting items. On ambiguity, default to Recommended and state it in the dispatch brief.

## Step 2.5 — Detect Knowledge-vs-Correction Contradictions

When Step 2 produced a non-Recommended answer that pushes back on a stated assumption, cache the correction text. After Step 4's K+D query returns, check it against each returned bullet: a fragment of ≥4 contiguous lowercase words from the bullet appearing in the correction (case-insensitive) makes the bullet a candidate. Log each, in parallel (the verb locks its sidecar):

```bash
rota knowledge contradiction add --topic <T> --title <S> --text "<first 200 chars of correction>"
```

`/rota-learn` Step 9 surfaces these at session end and asks per bullet whether to demote. Skip silently when Step 2 didn't run.

## Step 3 — Register in Status

After picking the branch name, single-repo:

```bash
rota status add <branch> --items <ID1>,<ID2>[,...] [--worktree <path>]
```

**Umbrella mode** (items carry `Repos:`, a comma-separated list; read it with `rota item field get <ID> --name repos`). All items in a wave must share the same repo set. One repo: add `--repo <repo-name>`. Several repos register one entry per `(branch, repo)`: `rota status add <branch> --items <ids> --repos <repos-csv> [--worktrees <csv>]`. Validate the names with `rota repo resolve <name>…`; exit 3 names the missing ones: surface them and stop. The call is idempotent on `(branch, repo)`, so call it again with worktree paths once Step 5 creates them.

## Step 4 — Plan Tasks

**Plan-as-artifact check (first).** For an item tagged to a milestone (`Milestone: M01` on `B07` → key `M01-B07`) or a slice (`M01-S01`), run `rota plan show <milestone>-<unit> 2>/dev/null`. If a plan exists, use its decomposition, files, verify steps and assumptions as the dispatch briefs instead of decomposing ad hoc; restate user redlines, and if the conversation contradicts the plan, ask whether to update the plan first (`/rota-plan`) or proceed and ignore it.

1. **Consult knowledge and decisions** with the canonical K+D pattern (`references/knowledge-consult.md`), topics inferred from the planned work. Run `rota glossary read <terms in the item>…` too, and call out synonym or drift when the user's wording deviates from a canonical term. Carry matches into Step 6 briefs as `**Known gotchas:**` (relevant bullets only) and `**Hard boundaries:**` (full entries: rule, *Why*, **Forbids**, **Permits**). Workers treat boundaries as constraints. If a planned task would violate a decision, **stop and surface it** before dispatching. Run `rota map stats --cap` (a one-line stderr nudge at or over the soft cap; never blocks).

   > **REQUIRED — Register hits on consumed bullets.** After writing the briefs, apply *Hit-register after consumption* in `references/knowledge-consult.md`: one `rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"` per bullet that landed in a brief's `**Known gotchas:**`, all in one parallel batch. Bullets pruned before the briefs earn no credit. Silent on success.

2. **Resuming** (a branch with commits past the base): apply the resume rule in [`references/task-ledger.md`](references/task-ledger.md) after decomposing, and plan only the unfinished tasks.
3. Identify discrete tasks: files to create or modify, what changes, acceptance criteria.
4. **Absorb file collisions** before grouping: any two tasks whose modified-file sets intersect, and shared-symbol changes that disjoint file sets hide. Rules and the rename check in [`references/work-wave-planning.md`](references/work-wave-planning.md).
5. Group into dependency waves: wave 1 is independent files (parallel); wave 2+ depends on earlier output.

## Step 4.5 — Umbrella Pre-Flight

Skip when `rota repo umbrella` exits 1 (single-repo). Registry shape and `Repos:` semantics are in `references/umbrella-mode.md`. When it exits 0:

1. **Every item must carry `Repos:`.** Otherwise stop: *"Error: `[<ID>]` lacks a `Repos:` tag. Re-run `/rota-capture` to add it. Cannot route to a sub-repo."*
2. **All items in a wave resolve to the same repo set** (order-independent). Otherwise stop: *"Error: items in this wave target different sub-repo sets: `<set-a>` vs `<set-b>`. Split into separate `/rota-work` runs."*
3. **Validate every name** with `rota repo resolve <name>…` (CSV spaces dropped). On failure stop: *"Error: `Repos: <name>` not registered in `.rota/repos.json`. Run `rota init umbrella` from the umbrella root to register sub-repos."*
4. **Walk-up (single-repo only).** If invoked from a cwd that `rota repo which` resolves to a registered sub-repo, default to it when items lack `Repos:`. Multi-repo items always come from the captured field.

**Issue mode** (`backlog.backend: "issues"`, `references/issue-mode.md`, *Umbrella*): items are single-repo with qualified IDs (`<repo>:<ID>`); take the repo from `rota item field get <ID> --name repos` (a bare ID in several sub-repos exits 2 as ambiguous) and pass `--repo <repo>` to `rota ship pr` in Step 10.

Carry the resolved set into Step 5 and Step 10.

## Step 5 — Create Branch or Worktree

Choose a descriptive name (`rota/quick-switch`, `rota/fix-timer-badge`). The pattern depends on `work.isolation` and umbrella mode; `references/isolation-patterns.md` has the table. The common case, single-repo with branch isolation:

```bash
git checkout -b <branch>
rota status add <branch> --items <ID>[,<ID>...]
```

For umbrella branches (single sub-repo, multi-repo via `rota git branch <name> --repos <csv>`, Layout B worktree) see `references/umbrella-mode.md` *Branch creation*.

**Issue mode** (`references/issue-mode.md`). Once the branch exists, per item run `rota item ready <ID>` (exit 1 prints what is missing: warn the user), then claim it with `rota item claim <ID> --as <branch>`. Exit 4 means another worker holds it: drop that item and continue with the rest, or stop when none remain. Exit 3 or 5: stop and report. Load each item's context as the reference's "Resuming an item" describes (start with `rota item show <ID>`) before planning tasks. For a `changes-requested` item, follow *Handling review feedback* in `references/worker-contract.md` when working its `feedback` comments.

The orchestrator stays at the repo root (umbrella root in umbrella mode); workers `cd` into their assigned directory first and use absolute paths.

## Step 6 — Dispatch Worker Agents

For each independent task dispatch a subagent with the **worker** model. Workers write files and never stage or commit, so parallel workers cannot race on `.git/index` under either isolation mode. Launch all independent agents in one message; don't announce.

```
You are implementing Task N of [total].
[UMBRELLA: "Sub-repo: <name>. Run all git operations from <absolute-sub-repo-path>; the umbrella's `.git/` is shared coordinator state, NOT your target."]
[WORKTREE: "Working directory: <absolute-worktree-path>. cd there before any file operations."]

**Goal:** [one sentence]

**Files:**
- Create: [paths]
- Modify: [paths with line references]

**What to do:**
[Precise instructions: what to read, what to change, exact code where possible]

**Known gotchas:**
[Relevant bullets from rota knowledge query output]

**Hard boundaries:**
[Relevant entries from rota decisions query: full rule plus forbids/permits. Workers MUST respect these; Step 7 checks the diff for violations.]

**Canonical terms:**
[Relevant terms from rota glossary read: definition plus aliases. Use these names in code, comments and commit messages.]

**Critical constraints:**
[Behavior preservation, patterns to follow, things NOT to touch]

**Claims to verify before building on them:**
[Every factual claim this brief rests on: a line number, a call-site count, "function X already returns Y". Check each first. If one is false, STOP and report which claim and what is actually there; do not implement around it.]

**Do NOT run `git add` or `git commit`.** Write changes to files only. The orchestrator commits (Step 7.5); leave a clean working-tree diff matching the brief.

**Suggested commit message:** [exact message; the orchestrator uses it in Step 7.5]

**On completion:** report the files you modified, plus any tool-generated siblings the toolchain produced, and confirm you did not stage or commit. Name any brief claim that turned out false, even if you worked around it.
```

**Umbrella.** The `[UMBRELLA]` line replaces the WORKTREE line under branch isolation; both appear under Layout B worktrees. Workers MUST `cd` to the named directory before any `git` command: the orchestrator stays at the umbrella for `.rota/` access, so a worker's default cwd targets the wrong `.git/`. For a multi-repo set, dispatch one worker per sub-repo with that repo's name and path; each brief lists only its own files, and Step 7 verifies each repo's commit independently.

Brief-writing rules, falsifiable-claims discipline, pre-baked citations, doc-writer ordering and the same-file Edit race: [`references/work-wave-planning.md`](references/work-wave-planning.md).

## Step 7 — Verify Each Completion

Verify internally; don't narrate. Trust the diff, not the worker's narrative.

1. `git status --porcelain`, then `git diff` for the files the worker reported.
2. Read the modified files: do they match the brief?
3. Structural checks: grep for expected patterns, no regressions.
4. **Rename validation.** Re-run `git grep -l "<old-name>" -- <scope>`; files outside the worker's set get a fix-up dispatch before staging.
5. Claim-weight check on gap-fills, and treat a worker's dispute of its brief as a FAIL on the plan (see `references/work-wave-planning.md`, *Verifying a completion*).

**PASS** → move on silently. **FAIL** → dispatch a fix agent and re-verify; surface failures only if they persist.

**Record proof.** For each task that PASSes, append one row per item it resolves: `rota proof add <ID> --check "<verify command or grep>" --result PASS --evidence "<output line or path>" [--sha <task-commit>]`. A FAIL that persists is recorded with `--result FAIL`. Proof rows are facts about what ran, not acceptance: `rota item complete` (Step 9) is the acceptance write and exits 4 when an item has no proof. `--no-proof` is never passed on its own; an unproven item stays open and is surfaced. Issue mode records the rows in the item's proof note.

## Step 7.5 — Commit per Task (orchestrator)

One commit per task, sequential, in dispatch order, staging only that task's files:

```bash
git add <task-N-files>
git commit -m "<suggested-message-from-task-N-brief>" -m "Task: <key>/<N>"
```

- Every task commit ends with the `Task: <key>/<N>` trailer (the task ledger, [`references/task-ledger.md`](references/task-ledger.md)); a resumed session reads these to skip finished tasks.
- Stage exactly the files named in the task's brief. Never `git add -A` or `git add .`: sweeping in another worker's changes breaks atomicity.
- Use the brief's suggested message verbatim, adjusted only if a FAIL→re-dispatch loop changed what landed.
- **Same-file carve-out.** Two parallel workers editing different ranges of one file get ONE commit naming both task IDs; granularity lives in the message.
- Worktree isolation: commit against the worktree's index (`git -C <worktree-path>`). Umbrella: commit inside each target sub-repo (`git -C <umbrella>/<repo>`).

## Step 8 — Sequential Waves

For dependent tasks, wait for wave 1 to complete and verify, then dispatch wave 2 with updated context. Step 7.5 already committed wave 1, so wave 2 sees a clean tree.

## Step 8.5 — Sweep Tool-Generated Siblings

After the task commits, sweep untracked toolchain siblings into their own `chore:` commit (staged by name) or the next `/rota-work` guard refuses a dirty tree. Non-sibling dirt means a worker produced unexpected changes: investigate before merging. See [`references/work-toolchain-siblings.md`](references/work-toolchain-siblings.md).

## Step 9 — Close the Items

**Issue backend:** skip this step and Step 9.5. Don't call `rota item complete`: the issue closes when its PR merges (`Closes #<n>`, Step 10).

**File backend**, per resolved item (match by keyword overlap between task and item title; leave items you didn't work on):

```bash
rota item complete <ID> --commit <commit-hash>
```

`rota item complete` is the only write to the backlog; never edit `.rota/` by hand. It leaves `.rota/BACKLOG.md` modified, and Step 9.5 may remove a plan file. Commit those paths by name, with no directory-wide `git add .rota/`:

```bash
git add .rota/BACKLOG.md [.rota/plans/<key>.md …]
git commit -m "chore: close <IDs>"
```

## Step 9.5 — Tombstone Consumed Item Plans

For each item `rota item complete` resolved, remove its item plan if one exists. Skip when the item has no milestone tag or no plan file:

```bash
MILESTONE=$(rota item field get <ID> --name milestone)
if [ -n "$MILESTONE" ] && [ -f ".rota/plans/${MILESTONE}-<ID>.md" ]; then
  rota plan rm "${MILESTONE}-<ID>"
fi
```

The plan's decomposition is stale once the item ships; truth lives in code and commits. **Slice plans (`M01-S01.md`) stay**: a slice covers several items, and cleanup is manual via `rota plan rm <key>`. Run this before the Step 9 commit so the removals ride along.

## Step 10 — Merge or PR

`work.mergeStrategy` picks `rota ship merge` (direct, the default and when unset) or `rota ship pr`. The user set the policy via `rota config set`; don't ask. Invocations (umbrella `--repo`, exit-4 verdict, merge-approval handling) are in `rota-ship` Steps 6a/6b. Opening a PR is a manual gate.

**The issue backend forces the PR path** and never merges:

```bash
printf '%s' "$BODY" | rota ship pr <branch> --title "<short title>" --body-file - --items <ID1>,<ID2>
rota item state <ID> --to needs-review    # once per item
```

Don't call `rota item release`: the claim persists until the PR merges, which is `/rota-review --queue`'s job. Details in `references/issue-mode.md`.

## Step 11 — Update Status and Report

```bash
rota status rm <branch>                  # umbrella: add --repo <repo>
```

Without `--repo`, `rota status rm` preserves umbrella-tagged entries, so umbrella waves MUST pass it or the entry leaks into the next no-argument `/rota-work`.

Then one compact summary; don't recap the plan, list verification results or describe intermediate steps:

```
Done — merged `rota/fix-timer-badge` into main.

- #12 Timer badge shows stale duration — fixed invalidation in MenuBarManager

Commit: a1b2c3d
```

## Step 12 — After-Work QA

Read `rota config show qa.afterWork` (default `false`). `false` → skip silently. `true` → check the touched files against the `Watch globs` of the `.rota/qa/*.md` strategies (umbrella: `.rota/qa/<REPO>.md`); on a match, invoke `Skill(skill="rota-qa", args="run")` for the item just finished (umbrella: `args="run --repo $REPO"`). No strategy or no match → skip silently. The verdict is advisory here; route nothing on it. Skip this step in a round worker: the worker contract (`references/worker-contract.md`) limits workers to targeted verification and a PR, and QA is the orchestrator's or `/rota-ship`'s call (`ship.qa`).

## Step 13 — After the Cycle

One line, only when `references/post-cycle-trigger-gate.md` fires: *"Run `/rota-learn` to save what this cycle taught; `/rota-decide`, `/rota-ship --docs` and `/rota-refactor` are there when you want them."* Never auto-invoke any of them. If the cycle touched files in a `.rota/map/<name>.md` entry's `Key files / dirs`, bump its `touched:` and run `rota map index`.

## Gates: Thought → Reality

| Thought | Reality |
|---|---|
| "The worker said it passed." | Step 7 trusts the diff: `git status`, `git diff` and the read files, never the worker's narrative. |
| "The check is obvious, skip the proof row." | `rota item complete` exits 4 without one. An unproven item stays open and is surfaced. |
| "Pass `--no-proof`, the code is fine." | Never on your own. The row records what ran; the flag records that nothing did. |
| "A worker disputing its brief is noise." | A dispute is a FAIL on the plan. Fix the plan, then re-dispatch. |
| "Open the PR now, the user wants it shipped." | Opening a PR is a manual gate whatever `autonomy.level` says (Step 10, `/rota-ship` Step 6a). |
| "Run the full smoke after each task." | Per-task checks stay structural. The full suite runs in `/rota-ship` and `/rota-review`. |

## Key Principles

- **No noise.** Report results, not process.
- **Orchestrator plans and verifies; worker executes.** Never dispatch without a clear brief; never trust completion without reading the result.
- **Orchestrator owns `.rota/` state.** Only the orchestrator touches `status.json` and the backlog (`rota item` verbs).
- **Isolation protects main.** Branch or worktree, never work directly on main.
- **One commit per task, owned by the orchestrator.** Clean history, easy revert granularity, no `.git/index` races.

## References

| Reference | Purpose |
|-----------|---------|
| [`task-ledger.md`](references/task-ledger.md) | `Task:` commit trailer and the resume rule that skips finished tasks. |
| [`work-preview.md`](references/work-preview.md) | `--preview` procedure and peek template. |
| [`work-wave-planning.md`](references/work-wave-planning.md) | File and shared-symbol collisions, brief rules, verifying a completion. |
| [`work-toolchain-siblings.md`](references/work-toolchain-siblings.md) | Tool-generated sibling patterns and the sweep commit. |
| [`isolation-patterns.md`](references/isolation-patterns.md) | Branch / worktree creation per `work.isolation` and umbrella mode. |
| [`issue-mode.md`](references/issue-mode.md) | Issue-mode helper map, state labels, PR flow, resuming an item, exit codes. |
| [`knowledge-consult.md`](references/knowledge-consult.md) | Canonical K+D query pattern used by every cycle-starting skill. |
| [`post-cycle-trigger-gate.md`](references/post-cycle-trigger-gate.md) | The condition for the Step 13 nudge. |
| [`umbrella-mode.md`](references/umbrella-mode.md) | Umbrella verbs, registry shape, `Repos:` field semantics. |
