---
name: rota-debug
description: Systematic root-cause investigation for a bug — reads the TODO entry + detail file, consults KNOWLEDGE.md, reproduces, hypothesizes, verifies, fixes with one atomic commit, and nudges /rota-learn. Use on "debug [B07]", "why is X broken", "investigate the crash", when a bug needs a proper cycle rather than a one-shot fix.
---

**Print the banner below verbatim before any other action — skip if dispatched as a subagent.** See `references/banner-preamble.md`.

```
════════════════════════════════════════════════════════════════════════
  🐛  rota-debug  ·  systematic root-cause investigation
  triggers: "debug [B07]", "why is X broken"  ·  pairs: rota-learn
════════════════════════════════════════════════════════════════════════
```

# rota-debug — Systematic Bug Cycle

Full reproduce → hypothesize → verify → fix cycle for a single bug. Anchors to a `[B##]` ID so the fix commit closes the backlog entry and the learning gets routed back to `KNOWLEDGE.md`.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model for hypothesis + verification (default `opus`)
- `models.worker` — model for the fix agent (default `sonnet`)
- `work.isolation` — `"branch"` (default) or `"worktree"`
- `autonomy.level` — `"off"` (default), `"auto"`, or `"loop"`. Controls whether Step 11 (Next move) and Step 12 (Learn) ask vs. invoke directly.
- `debug.competingHypotheses` — `false` (default) or `true`. When `true`, Step 6 fans out 3 parallel hypothesis agents from different angles instead of dispatching one.
- The Iron Law count is per item, in `.rota/verdicts.json` (`rota debug verdict` records each outcome); a new branch or session does not reset it. The attempt log for `rota debug counter summary` persists at `.rota/debug/<session>.json` (session = current branch with `/` → `-`); both survive `/clear` and session resumption.

## When to Use

- You have a bug ID (`[B07]`) or a reproducer and want a proper cycle
- Previous attempts failed or the symptom isn't obvious
- The bug looks novel enough to be worth capturing in `KNOWLEDGE.md`

## When NOT to Use

- Trivial fix with an obvious one-liner → `/rota-capture`, then accept the hand-off to `/rota-work`
- Multiple items in one pass → `/rota-work`
- You don't have a reproducer and the bug isn't captured → `/rota-capture` first

## Flow

```
Resolve bug → Consult knowledge → Reproduce → Hypothesize → Verify → Fix → Commit → (Iron Law gate) → Learn nudge
```

## Step 1 — Guard

```bash
rota git guard clean --context "/rota-debug"
```

Non-zero = stop (exit 1 dirty tree, exit 3 not a git repo).

**Initialize task list.** Follow the canonical pattern in `references/task-list-init.md` — load `TaskCreate(…)` via `ToolSearch select:TaskCreate,TaskUpdate` if needed, then create one task per phase below.

Phases:

1. *Guard* — clean tree, bug ID resolved (Step 1)
2. *Read item & knowledge* — TODO entry + KNOWLEDGE.md cross-ref loaded (Steps 2–3)
3. *Reproduce* — failure triggers reliably from a known input (Step 4)
4. *Hypothesize & verify* — claim is testable; evidence supports or refutes (Steps 5–6)
5. *Fix & commit* — minimal diff, atomic commit with `[B##]` footer (Steps 7–9)
6. *Iron Law gate* — at 3 failed fixes, hard stop and surface (Step 9.5)
7. *Smoke / regression* — no untouched-area breakage; existing tests pass (Step 10)
8. *Learn nudge* — autonomy-aware learn/decide nudges (Steps 11–12.5)

## Step 2 — Resolve the Bug

If the user named a `[B##]`:

- Read that line from `.rota/BACKLOG.md`
- If `Detail:` points at `.rota/bugs/B##.md`, read the detail file too

If the user described a symptom without an ID, invoke `rota-capture` via the `Skill` tool first so the bug gets logged — then resume here with the new ID.

## Step 3 — Consult KNOWLEDGE & DECISIONS

Apply the canonical K+D query pattern (`references/knowledge-consult.md`) with topics that plausibly touch the symptom (e.g., `Networking`, `Persistence`, `Concurrency`, `Architecture`, `Testing`).

Carry KNOWLEDGE bullets into Step 5's hypothesis brief. Carry DECISIONS entries into Step 6's hypothesis brief under a `**Hard boundaries:**` block — boundaries rule out fix directions that violate them, so applying them up front avoids wasted cycles.

## Step 3.5 — Vocabulary & soft-cap checks

```bash
rota glossary read "<terms from the bug report or the failing component>"
```

Carry any matched terms into Step 6's hypothesis brief — canonical definitions help align bug-report phrasing to existing components.

- **Soft-cap check.** Run `rota map stats --cap` — warns (`overCap`) if the subsystem count is at or above the configured soft cap. Never blocks.

## Step 4 — Branch or Worktree

Pick a descriptive name (e.g., `rota/fix-B07-timer-badge`).

**Branch:**

```bash
git checkout -b <branch-name>
rota status add <branch> --items <ID>
```

**Worktree:**

```bash
git branch <branch-name>
git worktree add .claude/worktrees/<branch-name> <branch-name>
rota status add <branch> --items <ID> --worktree .claude/worktrees/<branch-name>
```

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`): claim the bug now with `rota item claim <ID> --as <branch>` (exit 4: someone else holds it, stop; exit 3, 5 or 6: stop and report) and read its comments per the reference's "Resuming an item" before reproducing.

Initialize the per-session counter for the Iron Law (Step 9.5):

```bash
rota debug counter init <ID>
```

Session ID is derived from the current branch (`/` → `-`); state lives in `.rota/debug/<session>.json`. Idempotent on re-entry. Exit 4 (`data.blockedBy` `iron law`) means the bug already has 3 failed fixes: go to Step 9.5's surfacing without reproducing. `rota debug counter record-attempt` refuses the same way.

## Step 5 — Reproduce

Reproducing before hypothesizing is non-negotiable. Options:

1. **Run the bug's test** — if one exists, capture the failure output
2. **Write a failing test** — preferred when a test doesn't exist; lives in the test suite
3. **Manual repro** — build/run the app and observe, only if no test path is possible

**Dispatch a reproduce worker when the repro is heavy** — multi-MB output, multiple manual setup steps, or generating a failing test from scratch. Per `references/subagent-dispatch.md`, cheap repros (running an existing test that prints a 10-line stack trace, observing a single error in the dev server) stay on the orchestrator because the brief would cost more than the work.

When the dispatch criterion fires, brief one sonnet worker:

- **Goal:** Reproduce `[B##]` and return a concrete failure signal.
- **Inputs:** The bug ID, the symptom description from the TODO entry, suspected file paths, the repro path (which option from 1–3 above).
- **Constraints:** Return a structured verdict; do not propose a fix.
- **Return shape:** `{reproduced: bool, observed-vs-expected, relevant-log-excerpts (≤30 lines, the load-bearing ones)}`.
- **Word budget:** ≤200 words plus the log excerpts.

The orchestrator uses the worker's verdict as the Step 6 brief's **Symptom:** field. The full log stays in the worker's context, not the orchestrator's.

Don't proceed to Step 6 without a concrete failure signal — an error message, a wrong value, a stack trace.

If you can't reproduce, surface that to the user: *"Can't reproduce — need [X] from you (repro steps, environment, seed data)."* Stop and wait.

## Step 6 — Hypothesize (orchestrator)

Read `debug.competingHypotheses` from `.rota/config.json` (default `false`).

**Cycle-counter check.** Maintain a hypothesis-cycle counter for this bug — increment on each entry to Step 6 (initial entry counts as 1). When the counter is `>= 3` AND `debug.competingHypotheses` is `false`, do **not** dispatch a new hypothesis agent — jump to **Step 7.5 (Escalate)** instead. One orchestrator context accumulates enough failed-hypothesis weight by the 3rd cycle that fresh angles get harder to surface; competing mode's 3 parallel lenses already cover the diverse-angles pattern, so the threshold does not apply there.

Brief template (shared by both modes), single-mode dispatch (1 agent, no FRAMING), competing-mode dispatch (3 parallel agents with recent-changes / data-shape / concurrency-lifecycle lenses), and per-axis divergence table in `references/debug-hypothesize.md`.

## Step 7 — Verify

Run the verification probe from Step 6 — read the specific code, add a temporary trace, or run the targeted test. Confirm the hypothesis before touching production code.

If verification fails → the hypothesis is wrong. Go back to Step 6 with the new evidence (which re-runs the cycle-counter check and routes to Step 7.5 on the 3rd cycle). Don't fix-and-pray.

**Dispatch a verification worker when verification itself requires file reads, searches across the codebase, or running a non-trivial test.** Per `references/subagent-dispatch.md`, single-line verifications (read one specific line and confirm a value) stay on the orchestrator because the brief would cost more than the read.

When the dispatch criterion fires, brief one worker. Model tier follows the verification shape:

- **opus** when the verdict requires judgment — *"does this code actually implement the claimed invariant?"*, *"is this contract upheld under the race window?"*.
- **sonnet** when verification is pattern-matching across reads — *"does this symbol appear in any of these N files with the expected shape?"*.

Brief:

- **Goal:** Verify hypothesis `<hypothesis statement from Step 6>`.
- **Inputs:** The hypothesis, the specific verification probe (code paths to read, test to run, trace to inspect), the file:line evidence from Step 6.
- **Constraints:** Return a verdict, not a fix. If verification fails, surface the new evidence that disproves the hypothesis.
- **Return shape:** `{verdict: confirmed|disproved|inconclusive, evidence-citations[]: {file, line, snippet}, new-evidence (if disproved): what the worker found that contradicts the hypothesis}`.
- **Word budget:** ≤200 words.

If the verdict is `disproved`, the orchestrator returns to Step 6 with the new evidence (which re-runs the cycle-counter check and routes to Step 7.5 on the 3rd cycle) — the existing behavior is unchanged; the worker is the new input source.

## Step 7.5 — Escalate on Repeated Hypothesis Failures

Fires only when Step 6's cycle-counter check trips (`counter >= 3`, single-hypothesis mode). The orchestrator's context carries 2+ refuted hypotheses; dispatch a fresh subagent (`Agent` with `subagent_type: general-purpose`, model `models.worker`) carrying a *for-next-agent* brief — refuted hypotheses, files inspected, orchestrator read on why the loop did not converge — and nothing else. On return, reset the cycle counter and carry the fresh hypothesis into Step 7. If the fresh-context attempt also fails verification, surface to the user — do not loop a second fresh-context attempt. See [`references/debug-escalate.md`](references/debug-escalate.md) for the brief template and the user-surfacing fallback.

## Step 8 — Fix (worker)

Dispatch a fix agent with the **worker** model. Brief contains:

```
Fix [B##]: <title>.

**Root cause (verified):**
<one-sentence causal claim>

**Files:**
- Modify: <paths with line numbers>

**Change:**
<precise description of the minimal edit — exact code where possible>

**Constraints:**
- Minimal diff. No unrelated cleanup.
- Preserve behavior for callers not affected by the bug.
- Read the file before editing.

**Do NOT run `git add` or `git commit`.** Write the change to files only — orchestrator commits in Step 8.5.

**Suggested commit message:** fix: <short imperative> [B##]

<optional body with the root cause in 1-2 sentences>
```

The worker reads and edits in one pass. Orchestrator stages and commits in Step 8.5 — same write-only pattern as `/rota-work` Step 6 (default since F11). Hv-debug is single-worker by design, so the file-disjointness and parallel-commit concerns from `/rota-work` don't apply, but using one shared pattern keeps brief templates consistent across skills.

## Step 8.5 — Commit (orchestrator)

Stage exactly the files named in the worker's brief and commit with the suggested message:

```bash
git add <files-from-brief>
git commit -m "fix: <short imperative> [B##]"
```

One commit for the bug. Don't `git add -A` — sweep risk if any sibling artifacts crept in. If the toolchain produced legitimate sibling files (e.g. Godot `.gd.uid`), follow the same sweep pattern as `/rota-work` Step 8.5: a separate `chore:` commit, not the same atomic unit as the fix.

Record the attempt in the Iron Law counter so Step 9 can detect repeat failures:

```bash
rota debug counter record-attempt --hypothesis "<one-line hypothesis from Step 6>" --commit "$(git rev-parse --short HEAD)"
```

## Step 9 — Verify the Fix

Re-run the reproducer from Step 5. It must now pass (or the symptom must be gone). If the regression test is new, confirm it's in the suite and runs under the default test command.

Record whether the fix held. The verb counts the bug's failed fixes and closes the Step 8.5 attempt in the counter:

```bash
rota debug verdict <ID> --verdict <PASS|FAIL> --json
```

Route on `data.next`:

- `complete` — the fix held. Continue to Step 10.
- `hypothesize` — the fix failed. Back to Step 6 (which re-runs the in-context hypothesis cycle counter). Don't commit a partial fix.
- `halt` — the fix failed and the bug has reached the Iron Law limit (`data.failedFixes`). Jump to **Step 9.5 (Iron Law hard stop)**. Do NOT loop back to Step 6.

## Step 9.5 — Iron Law Hard Stop

Fires when `rota debug verdict` routes to `halt`. Three committed fix attempts have failed to resolve the bug — continuing to dispatch more workers in the same session burns context without converging. Iron Law: hard stop, no further attempts.

Print the fail-loud summary verbatim to the user:

```bash
rota debug counter summary
```

Then surface — do NOT dispatch a fresh-context worker (Step 7.5's escalation belongs to the hypothesis-cycle counter; this is a stricter, terminal gate). Suggest the user:

- Run `/rota-pause` to leave a handoff note and step away. Further attempts on this bug are refused until a human resets the count with `rota debug reset <ID> --reason "<why>"`, a manual gate (`references/manual-gates.md`): run it only after an `AskUserQuestion` yes, passing `--confirm --confirm-note "<their answer>"`, never on your own.
- Or re-open the bug from a different angle — the symptom may be in a subsystem the past three hypotheses haven't touched.

Do NOT call `rota status rm` here — the branch and status entry stay so the user can resume. Do NOT call `rota item complete` — the bug is not fixed.

This is a terminal path. Surface any `[Auto:Loop]` decisions before halting:

Surface any `[Auto:Loop]` decisions per `references/terminal-loop-surface.md` (silent when empty).

Loop mode (`autonomy.level == "loop"`): the Iron Law breaks the loop. Do not auto-dispatch `/rota-work` (no argument) or any continuation skill. The loop stops here; the user re-engages by hand.

## Step 10 — Mark Complete

Clear the Iron Law counter for this session:

```bash
rota debug counter clear
```

Record the Step 9 reproducer re-run as proof, then mark the item complete:

```bash
rota proof add <ID> --check "<reproducer command>" --result PASS --evidence "<output line showing the symptom is gone>" --sha <commit-hash>
rota item complete <ID> --commit <commit-hash>
```

`rota item complete` exits 4 if the proof row is missing. Never pass `--no-proof` here: Step 9 always runs the reproducer, so a missing row means the step was skipped. Go back and run it.

**Issue mode:** `rota proof add` above works unchanged (the row lands in the proof note), but do not call `rota item complete` and do not merge directly. Open a PR instead and hand it to review; the issue closes when the PR merges:

```bash
printf '%s' "$BODY" | rota ship pr <branch> --title "<short title>" --body-file - --items <ID>
rota item state <ID> --to needs-review
```

The claim stays (no `rota item release`). Then continue with the status cleanup below.

**Single-repo:**

```bash
rota status rm <branch>
```

**Umbrella mode** (when `umbrella.enabled` is true and the active entry has a non-null `repo`, derive it from `.rota/status.json` as `/rota-ship` does in its Step 2):

```bash
rota status rm --repo <repo> <branch>
```

Without `--repo`, the verb preserves umbrella-tagged entries (only legacy `repo: null` rows are removed) — so umbrella sessions MUST pass `--repo` here or the active entry leaks into the next `/rota-work` (no argument) run.

## Step 11 — Report

One compact block:

```
Fixed [B07] Timer badge shows stale duration — commit a1b2c3d on `rota/fix-B07-timer-badge`.

Root cause: MenuBarManager held an invalidated timer ref after pause; the next tick no-op'd without resetting the badge.

Fix: reset badge to `--:--` in `pause()` before invalidating.
```

Branch on `autonomy.level`:

- `"off"` (default) — `AskUserQuestion`:
  - **Header:** `"Next"`
  - **Question:** *"Fix for [B##] is committed. What's next?"*
  - **Options** (single-select):
    1. "Ship via `/rota-ship` (Recommended)" — *"Run the review gate and integrate."*
    2. "Keep working on the branch" — *"Stay on the branch to add more fixes."*
    3. "Stop here" — *"Leave the branch; come back later."*
  - Plain-text fallback: *"Merge now with `/rota-ship`, or keep it on the branch for more work?"*
- `"auto"` or `"loop"` — **dispatch `rota-ship` via `Skill` with the current branch immediately — no prompt, no confirmation.** (`ship.review` still governs the review gate.)

## Step 12 — Learn (Nudge or Auto-Invoke)

Trigger: the root cause was **not obvious from reading the code alone** — required verification, contradicted an initial hypothesis, or touched a known-tricky subsystem. Skip for trivial fixes (typo, obvious off-by-one).

Branch on `autonomy.level`:

- `"off"` — nudge *"Capture this gotcha? Run `/rota-learn` to save the root cause before context fades."*
- `"auto"` or `"loop"` — **dispatch `rota-learn` via `Skill` immediately — no prompt, no confirmation.** Pass a brief naming the bug ID, root cause, and subsystem so the captured entry lands in the right topic.

If the bug was rooted in rota behavior (touched the `rota` binary, `rota-*/SKILL.md`, or `.rota/`), `/rota-learn`'s Step 8.5 will offer to file an upstream issue against `l4ci/rota`.

- **Update project map.** If the fix touched files belonging to a known subsystem (`.rota/map/<name>.md` whose `Key files / dirs` or `Entry points` overlap the changes), bump `touched:` to today in that file's frontmatter and run `rota map index`. Stage with the cycle's final commit. Skip silently when no map entry matches.

## Step 12.5 — Decide (Nudge Only)

If the fix codified a constraint (e.g., "never use timer-X here", "this surface only goes through controller-Y"), surface a one-liner. **Always nudge — never auto-invoke**, regardless of `autonomy.level`. Skip trivial fixes (single-line edit, obvious typo). Don't repeat in the session.

> *"Did this fix lock in a boundary worth preserving? Run `/rota-decide` to capture it as a hard constraint."*

## Key Principles

- **Reproduce before hypothesizing, verify before fixing.** No fix-and-pray.
- **Iron Law: no fix without a hypothesis; hard stop at 3 failed fixes.** The hypothesis is logged with each attempt; three committed fixes that don't hold trigger Step 9.5 — no more attempts, surface to the user.
- **Hypothesis is a claim, not a description.** "X causes Y because Z" — testable.
- **One fix, one commit.** Scope creep in debug commits masks the root cause later.
- **The ID closes the loop.** The commit message carries `[B##]`; `rota item complete` moves the entry.
- **Learn the non-obvious.** If this bug surprised you, it'll surprise the next person.

## References

- [`references/banner-preamble.md`](references/banner-preamble.md) — Banner-print rule shared by every skill.
- [`references/debug-hypothesize.md`](references/debug-hypothesize.md) — Both-modes hypothesize choreography (brief template, single vs competing dispatch, per-axis divergence table) for `/rota-debug` Step 6.
- [`references/knowledge-consult.md`](references/knowledge-consult.md) — Canonical K+D query pattern (`rota knowledge query` + `rota decisions query`) used by every cycle-starting skill.
