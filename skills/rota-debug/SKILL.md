---
name: rota-debug
description: Systematic root-cause investigation for a bug — reproduce first, state a testable hypothesis, verify it, fix with one atomic commit, record proof, open a PR. Hard stop at 3 failed fixes (Iron Law). Use on "debug 42" or "debug [B07]" on the file backend, "why is X broken", "investigate the crash", when a bug needs a proper cycle rather than a one-shot fix.
---

# rota-debug — Systematic Bug Cycle

Reproduce → hypothesize → verify → fix → prove, for one bug. Anchors to a backlog item (`#N`; file backend `[B07]`) so the fix commit and PR close it. Work inline; delegate only reads and searches too big for your context (a `light` subagent), never the diagnosis itself.

**Not for:** a trivial obvious one-liner (`/rota-capture`, then `/rota-work`), several items in one pass (`/rota-work`), or a bug nobody has captured (`/rota-capture` first).

## Configuration

From `.rota/config.json`: `work.isolation` (`"branch"` default, or `"worktree"`). The Iron Law count is per item in `.rota/verdicts.json`; a new branch or session does not reset it.

## Step 1 — Guard and resolve

```bash
rota git guard clean --context "/rota-debug"   # non-zero: stop (1 dirty tree, 3 not a git repo)
```

Load the item: `rota item field list --json <ID>` for title, body and fields; `rota item show <ID>` for state, claim and comments (`references/issue-mode.md`, "Resuming an item"; `decision` comments bind). File backend: its `.rota/BACKLOG.md` line plus the `Detail:` file. A symptom with no ID goes through `rota-capture` first.

Consult knowledge and decisions per `references/knowledge-consult.md`, topics that plausibly touch the symptom. DECISIONS entries are hard boundaries: they rule out fix directions that violate them.

## Step 2 — Branch and claim

Branch `rota/fix-<n>-<slug>` (`git checkout -b`; for a worktree, `git worktree add .claude/worktrees/<branch> -b <branch>`), then `rota status add <branch> --items <ID>` (worktree: add `--worktree <path>`). Claim: `rota item claim <ID> --as <branch>` (exit 4 means someone else holds it, stop; exit 3, 5, 6: stop and report). Start the Iron Law counter:

```bash
rota debug counter init <ID>
```

Exit 4 (`data.blockedBy` `iron law`) means the bug already has 3 failed fixes: go to *Iron Law stop* without reproducing.

## Step 3 — Reproduce

Non-negotiable, and before any hypothesis. In order of preference: run the bug's existing test, write a failing test in the suite, or reproduce by hand. You need a concrete failure signal (error message, wrong value, stack trace). If you cannot reproduce, stop and ask: *"Can't reproduce — need [X] from you (repro steps, environment, seed data)."*

## Step 4 — Hypothesize and verify

State the hypothesis as a testable claim: **"X causes Y because Z"**, plus the probe that would confirm or refute it (a line to read, a temporary trace, a targeted test). Run the probe before touching production code.

- **Confirmed** — go to Step 5.
- **Refuted** — new evidence, new hypothesis. Never fix-and-pray. After two refuted hypotheses, re-read the symptom and the reproducer from scratch before the third.

## Step 5 — Fix and commit

**Iron Law: no fix without a verified hypothesis.** Make the minimal diff that removes the root cause: no unrelated cleanup, existing behavior for unaffected callers preserved, the file read before editing. Delegating the edit to a `standard` subagent is fine when it is large; give it the verified root cause, files and constraints, and tell it not to commit.

Commit one atomic fix with explicit paths (`git add <files>`, never `-A`):

```bash
git commit -m "fix: <short imperative> <ID>"
rota debug counter record-attempt --hypothesis "<one-line hypothesis>" --commit "$(git rev-parse --short HEAD)"
```

Legitimate toolchain siblings (e.g. Godot `.gd.uid`) go in a separate `chore:` commit.

## Step 6 — Verify the fix

Re-run the Step 3 reproducer. It must pass; a new regression test must be in the suite and run under the default test command. Then record the outcome:

```bash
rota debug verdict <ID> --verdict <PASS|FAIL> --json
```

Route on `data.next`: `complete` continue to Step 7; `hypothesize` the fix failed, back to Step 4, and don't keep a partial fix; `halt` the Iron Law limit is reached, go to *Iron Law stop*.

## Iron Law stop

Three failed committed fixes: no further attempts. Print `rota debug counter summary` to the user and suggest `/rota-pause` or reopening the bug from a different angle (the symptom may live in a subsystem the three hypotheses never touched). Further attempts are refused until a human runs `rota debug reset <ID> --reason "<why>"`, a manual gate (`references/manual-gates.md`): only after an `AskUserQuestion` yes, passing `--confirm --confirm-note "<their answer>"`, never on your own. Leave the branch and status entry; do not `rota item complete`; do not dispatch any continuation skill.

## Step 7 — Proof, PR, cleanup

**The proof row:** record the reproducer re-run.

```bash
rota debug counter clear
rota proof add <ID> --check "<reproducer command>" --result PASS --evidence "<output line showing the symptom is gone>" --sha <commit-hash>
```

Open a PR and hand it to review; never merge directly. The issue closes when the PR merges:

```bash
printf '%s' "$BODY" | rota ship pr <branch> --title "<short title>" --body-file - --items <ID>
rota item state <ID> --to needs-review
```

Keep the claim (no `rota item release`). **File backend:** `rota item complete <ID> --commit <hash>` instead; it exits 4 when the proof row is missing, so never pass `--no-proof` (a missing row means Step 6 was skipped). Finally `rota status rm <branch>`; in umbrella mode (`umbrella.enabled` and the entry has a non-null `repo`) pass `--repo <repo>`, or the entry leaks into the next `/rota-work` run.

## Step 8 — Report

```
Fixed #42 Timer badge shows stale duration — commit a1b2c3d on `rota/fix-42-timer-badge`.
Root cause: MenuBarManager held an invalidated timer ref after pause; the next tick no-op'd.
Fix: reset badge to `--:--` in `pause()` before invalidating.
```

One line of nudge, only when it applies: if the cause was not obvious from reading the code, *"Run `/rota-learn` to save this gotcha"*; if the fix locked in a boundary, *"`/rota-decide`"* (never auto-invoked). Then offer `/rota-ship`.

## Key principles

- **Reproduce before hypothesizing, verify before fixing.**
- **Hypothesis is a claim, not a description.** "X causes Y because Z": testable.
- **Iron Law: no fix without a hypothesis; hard stop at 3 failed fixes.**
- **One fix, one commit.** Scope creep in debug commits masks the root cause later.
- **The ID closes the loop.** Commit carries the item ID, the PR carries `Closes #N`.

## References

- [`references/knowledge-consult.md`](references/knowledge-consult.md) — K+D query pattern.
