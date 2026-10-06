---
name: rota-work
description: Use when backlog items already exist and need implementation ("implement 42", "build these", or "implement [B07]" on the file backend), or with no argument to reconcile active work and pick the next item. A parallel round with standing workers is /rota-orchestrate, not this skill; an item not yet captured goes through /rota-capture first.
---

# rota-work

Main-session-driven implementation with per-task verification and commits. Subagents are in-process `Agent` calls that write files; the main session commits.

**Rounds are not this skill** (standing workers, merge gate, relays: `/rota-orchestrate` and the `rota round` verbs). If `work.dispatch` is `"tmux"` or `"herdr"`, this skill still dispatches subagents; say so once and continue, or point the user at `/rota-orchestrate` for a multi-issue round.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — main session model: planning and verification (default `opus`)
- `models.worker` — the `standard` tier: implementation subagents (default `sonnet`)
- `work.isolation` — `"branch"` (default) or `"worktree"`
- `work.mergeStrategy` — `"direct"` (default) or `"pr"`

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

Exit 0: continue. Exit 3: not a repo, surface and stop. Exit 1 (dirty tree): if **every** dirty path in `git status --porcelain` is a tool-generated sibling, sweep them into their own `chore:` commit (never inside a task commit) and continue; any user change stops with the guard's message. Siblings are:

- a path beside a tracked file (e.g. `Foo.gd.uid` beside tracked `Foo.gd`), or
- one of `*.gd.uid`, `*.xcworkspace/contents.xcworkspacedata`, `Package.resolved`, `*.xcodeproj/project.pbxproj` regenerated without a meaningful diff, `.DS_Store`.

Stage by name:

```bash
git add -- <sibling paths>
git commit -m "chore: sweep tool-generated siblings"
```

If a tool only regenerates siblings when the editor loads (Godot `class_name` → `.gd.uid`), force generation once headless first (`godot --headless --editor --quit`). Don't narrate the sweep unless it happened.

On a fresh `git init` with no commits the guard points at a `chore: import initial files` baseline; run it and re-invoke.

## Step 2 — Clarify Ambiguous Briefs (only when needed)

Only when the brief is too thin to plan concrete tasks (scope hits 2+ incompatible areas, a requirement could yield opposite implementations, captured items imply different orderings), ask one `AskUserQuestion` of 1-3 questions before touching code. Otherwise proceed. Don't ask to confirm understanding, for preferences inferable from `KNOWLEDGE.md` or the codebase, or for style inside an agreed scope.

Each question gets a short `header`, options that map to concrete plans with the likeliest marked `(Recommended)`, and `multiSelect: true` for conflicting items. On ambiguity, default to Recommended and state it in the dispatch brief.

## Step 2.5 — Detect Knowledge-vs-Correction Contradictions

When Step 2 produced a non-Recommended answer that pushes back on a stated assumption, cache the correction text. After Step 4's K+D query, a bullet is a candidate when ≥4 contiguous words of it appear in the correction (case-insensitive). Log each, in parallel:

```bash
rota knowledge contradiction add --topic <T> --title <S> --text "<first 200 chars of correction>"
```

`/rota-learn` Step 9 surfaces these at session end and asks per bullet whether to demote. Skip silently when Step 2 didn't run.

## Step 3 — Name the Branch

Pick a descriptive branch name (`rota/quick-switch`). Step 5 creates the branch and registers it once.

**Umbrella mode** (items carry `Repos:`, comma-separated; `rota item field get <ID> --name repos`). All items in a wave must share the same repo set. Validate with `rota repo resolve <name>…`; exit 3 names the missing ones: surface and stop.

## Step 4 — Plan Tasks

**Plan-as-artifact check (first).** For an item tagged to a milestone (`Milestone: M01` on `B07` → key `M01-B07`) or a slice (`M01-S01`), run `rota plan show <milestone>-<unit> 2>/dev/null`. If a plan exists, use its decomposition, files, verify steps and assumptions as the dispatch briefs; restate user redlines. If the conversation contradicts the plan, ask whether to update it first (`/rota-plan`) or proceed and ignore it.

1. **Consult knowledge and decisions** with the canonical K+D pattern (`references/knowledge-consult.md`), topics inferred from the planned work. Run `rota glossary read <terms in the item>…` too; call out drift from a canonical term. Carry matches into Step 6 briefs as `**Known gotchas:**` (relevant bullets only) and `**Hard boundaries:**` (full entries: rule, *Why*, **Forbids**, **Permits**). If a planned task would violate a decision, **stop and surface it** before dispatching. Run `rota map stats --cap` (one-line nudge; never blocks).

   > **REQUIRED — Register hits on consumed bullets.** After writing the briefs, apply *Hit-register after consumption* in `references/knowledge-consult.md`: one `rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"` per bullet that landed in a brief's `**Known gotchas:**`, all in one parallel batch. Bullets pruned before the briefs earn no credit.

2. **Resuming** (a branch with commits past the base): apply the resume rule in [`references/task-ledger.md`](references/task-ledger.md) after decomposing, and plan only the unfinished tasks.
3. Identify tasks: files, what changes, acceptance criteria.
4. **Absorb file collisions** before grouping: any two tasks whose modified-file sets intersect, and shared-symbol changes that disjoint file sets hide. Rules and the rename check in [`references/work-wave-planning.md`](references/work-wave-planning.md).
5. Group into dependency waves: wave 1 is independent files (parallel); wave 2+ depends on earlier output.

## Step 4.5 — Umbrella Pre-Flight

Skip when `rota repo umbrella` exits 1 (single-repo). Registry shape and `Repos:` semantics: `references/umbrella-mode.md`. When it exits 0:

1. **Every item must carry `Repos:`.** Otherwise stop: *"Error: `[<ID>]` lacks a `Repos:` tag. Re-run `/rota-capture` to add it. Cannot route to a sub-repo."*
2. **All items in a wave resolve to the same repo set** (order-independent). Otherwise stop: *"Error: items in this wave target different sub-repo sets: `<set-a>` vs `<set-b>`. Split into separate `/rota-work` runs."*
3. **Validate every name** with `rota repo resolve <name>…` (CSV spaces dropped). On failure stop: *"Error: `Repos: <name>` not registered in `.rota/repos.json`. Run `rota init umbrella` from the umbrella root to register sub-repos."*
4. **Walk-up (single-repo only).** If invoked from a cwd that `rota repo which` resolves to a registered sub-repo, default to it when items lack `Repos:`. Multi-repo items always come from the captured field.

**Issue mode** (`backlog.backend: "issues"`, `references/issue-mode.md`, *Umbrella*): items are single-repo with qualified IDs (`<repo>:<ID>`); take the repo from `rota item field get <ID> --name repos` (a bare ID in several sub-repos exits 2 as ambiguous) and pass `--repo <repo>` to `rota ship pr` in Step 10.

Carry the resolved set into Step 5 and Step 10.

## Step 5 — Create Branch or Worktree

Use the Step 3 name; `references/isolation-patterns.md` has the pattern per `work.isolation` and umbrella mode. Single-repo, branch isolation:

```bash
git checkout -b <branch>
rota status add <branch> --items <ID>[,<ID>...]
```

The only `rota status add` of the cycle: create the branch or worktree first, then register once (`--worktree <path>` for a worktree). Idempotent on `(branch, repo)`.

**Umbrella registration.** One repo: add `--repo <repo-name>`. Several repos register one entry per `(branch, repo)`: `rota status add <branch> --items <ids> --repos <repos-csv> [--worktrees <csv>]`.

Umbrella branches (`rota git branch <name> --repos <csv>`, Layout B worktree): `references/isolation-patterns.md`.

**Issue mode** (`references/issue-mode.md`). Once the branch exists, per item run `rota item ready <ID>` (exit 1 prints what is missing: warn the user), then claim it with `rota item claim <ID> --as <branch>`. Exit 4 means another worker holds it: drop that item and continue with the rest, or stop when none remain. Exit 3 or 5: stop and report. Load each item's context as the reference's "Resuming an item" describes (start with `rota item show <ID>`) before planning tasks. For a `changes-requested` item, follow *Handling review feedback* in `references/worker-contract.md` when working its `feedback` comments.

The main session stays at the repo root (umbrella root in umbrella mode); subagents `cd` into their assigned directory first and use absolute paths.

## Step 6 — Dispatch Subagents

Dispatch one `standard`-tier subagent per independent task, all in one message; don't announce. Subagents write files and never stage or commit (no `.git/index` races).

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
[Relevant entries from rota decisions query: full rule plus forbids/permits. Subagents MUST respect these; Step 7 checks the diff for violations.]

**Canonical terms:**
[Relevant terms from rota glossary read: definition plus aliases. Use these names in code, comments and commit messages.]

**Critical constraints:**
[Behavior preservation, patterns to follow, things NOT to touch]

**RED before GREEN:** [behavior change: write the new test first, run it against the unchanged code and report the command plus the failing output line before touching production code; a test that already passes proves nothing, so fix the test. Docs, skill-text or other no-test-seam change: write `no test seam: docs/skill change` and skip.]

**Claims to verify before building on them:**
[Every factual claim this brief rests on: a line number, a call-site count, "function X already returns Y". Check each first. If one is false, STOP and report which claim and what is actually there; do not implement around it.]

**Do NOT run `git add` or `git commit`.** Write changes to files only. The main session commits (Step 7.5); leave a clean working-tree diff matching the brief.

**Suggested commit message:** [exact message; the main session uses it in Step 7.5]

**On completion:** report the RED command and failing line (or the no-test-seam note), the files you modified, plus any tool-generated siblings the toolchain produced, and confirm you did not stage or commit. Name any brief claim that turned out false, even if you worked around it.
```

**Umbrella.** The `[UMBRELLA]` line replaces the WORKTREE line under branch isolation; both appear under Layout B worktrees. Subagents MUST `cd` to the named directory before any `git` command (their default cwd targets the wrong `.git/`). For a multi-repo set, dispatch one subagent per sub-repo; each brief lists only its own files, and Step 7 verifies each repo's commit independently.

Brief-writing rules, falsifiable-claims discipline, pre-baked citations, doc-writer ordering and the same-file Edit race: [`references/work-wave-planning.md`](references/work-wave-planning.md).

## Step 7 — Verify Each Completion

Verify silently. Trust the diff, not the subagent's narrative.

1. `git status --porcelain`, then `git diff` for the files the subagent reported.
2. Read the modified files: do they match the brief?
3. Structural checks: grep for expected patterns, no regressions.
4. **Rename validation.** Re-run `git grep -l "<old-name>" -- <scope>`; files outside the subagent's set get a fix-up dispatch before staging.
5. Claim-weight check on gap-fills, and treat a subagent's dispute of its brief as a FAIL on the plan (see `references/work-wave-planning.md`, *Verifying a completion*).

**PASS** → move on. **FAIL** → dispatch a fix agent and re-verify; repeat until PASS, and surface only persistent failures. Commit (Step 7.5) only on PASS.

**Record proof** (facts about what ran, not acceptance). Per task:

- **PASS:** one row per item the task resolves: `rota proof add <ID> --check "<verify command or grep>" --result PASS --evidence "<output line or path>" [--sha <task-commit>]`.
- **Persistent FAIL:** record it with `--result FAIL`.
- **Behavior task:** first record the subagent's reported RED run: `rota proof add <ID> --check "<test command>" --result FAIL --evidence "<failing line>" --sha <sha before the change>`, then the PASS row after. A subagent that reports no RED gets a fix dispatch (a FAIL above).
- **Docs or skill-only task:** no RED. Put `no test seam: docs/skill change` in the PASS row's `--check`.
- **Acceptance:** `rota item complete` (Step 9) writes it and exits 4 when an item has no proof. Never pass `--no-proof` on your own; an unproven item stays open and is surfaced.
- **Issue mode:** the rows go in the item's proof note.

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
- Worktree isolation: commit against the worktree's index (`git -C <worktree-path>`). Umbrella: commit inside each target sub-repo (`git -C <umbrella>/<repo>`).

## Step 8 — Sequential Waves

Wait for wave 1 to complete, verify and commit (Step 7.5), then dispatch wave 2 with updated context.

## Step 8.5 — Sweep Tool-Generated Siblings

After the task commits, sweep untracked toolchain siblings into their own `chore:` commit (staged by name), per Step 1, or the next `/rota-work` guard refuses a dirty tree. Non-sibling dirt is an unexpected subagent change: investigate before merging.

## Step 9 — Close the Items

**Issue backend:** skip this step. Don't call `rota item complete`: the issue closes when its PR merges (`Closes #<n>`, Step 10).

**File backend:** follow [`file-backend-close.md`](file-backend-close.md) per resolved item: it tombstones the item's plan, runs `rota item complete <ID> --commit <commit-hash>` (the only write to the backlog), then commits `.rota/BACKLOG.md` and the removed plan by name.

## Step 10 — Merge or PR

`work.mergeStrategy` picks `rota ship merge` (direct, the default and when unset) or `rota ship pr`. Don't ask. Invocations (umbrella `--repo`, exit-4 verdict, merge-approval handling) are in `rota-ship` Steps 6a/6b. Opening a PR is a manual gate.

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

Umbrella waves MUST pass `--repo`, or the entry leaks into the next no-argument `/rota-work`.

Then one compact summary, no plan recap or verification list:

```
Done — merged `rota/fix-timer-badge` into main.

- #12 Timer badge shows stale duration — fixed invalidation in MenuBarManager

Commit: a1b2c3d
```

## Step 12 — After-Work QA

Read `rota config show qa.afterWork` (default `false`). `false` → skip silently. `true` → check the touched files against the `Watch globs` of the `.rota/qa/*.md` strategies (umbrella: `.rota/qa/<REPO>.md`); on a match, invoke `Skill(skill="rota-qa", args="run")` for the item just finished (umbrella: `args="run --repo $REPO"`). No strategy or no match → skip silently. The verdict is advisory here; route nothing on it. Skip in a round worker (`references/worker-contract.md`): QA is the orchestrator's or `/rota-ship`'s call (`ship.qa`).

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
- **The main session plans and verifies; subagents execute.** Never dispatch without a clear brief or trust completion unread.
- **The main session owns `.rota/` state** (`status.json`, backlog via `rota item` verbs).
- **Never work directly on main.**
- **One commit per task, owned by the main session.**

## References

`task-ledger.md`, `context-load-protocol.md`, `work-preview.md`, `work-wave-planning.md`, `isolation-patterns.md`, `issue-mode.md`, `knowledge-consult.md`, `post-cycle-trigger-gate.md`, `umbrella-mode.md`.
