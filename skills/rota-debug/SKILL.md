---
name: rota-debug
description: Use on "debug 42" or "debug [B07]" on the file backend, "why is X broken", "investigate the crash", or when a bug needs a proper cycle rather than a one-shot fix.
---

# rota-debug — Systematic Bug Cycle

Reproduce → hypothesize → verify → fix → prove, for one bug, anchored to a backlog item (`#N`; file backend `[B07]`) so the fix commit and PR close it. Work inline; delegate only reads and searches too big for your context (a `light` subagent), never the diagnosis.

**Not for:** a trivial obvious one-liner (`/rota-capture`, then `/rota-work`), several items in one pass (`/rota-work`), or a bug nobody has captured (`/rota-capture` first).

## Configuration

From `.rota/config.json`: `work.isolation` (`"branch"` default, or `"worktree"`). The Iron Law count is per item in `.rota/verdicts.json`; a new branch or session does not reset it.

Copy this checklist and track your progress:
```
- [ ] Step 1 — Guard and resolve
- [ ] Step 2 — Branch and claim
- [ ] Step 3 — Build the feedback loop
- [ ] Step 4 — Hypothesize and verify
- [ ] Step 5 — Fix and commit
- [ ] Step 6 — Verify the fix
- [ ] Step 7 — Proof, PR, cleanup
- [ ] Step 8 — Report
```

## Step 1 — Guard and resolve

```bash
rota git guard clean --context "/rota-debug"   # non-zero: stop (1 dirty tree, 3 not a git repo)
```

Load the item: `rota item field list --json <ID>` for title, body and fields; `rota item show <ID>` for state, claim and comments (`references/issue-mode.md`, "Resuming an item"; `decision` comments bind). File backend: its `.rota/BACKLOG.md` line plus the `Detail:` file. A symptom with no ID goes through `rota-capture` first.

Consult knowledge and decisions per `references/knowledge-consult.md` for topics touching the symptom. DECISIONS entries are hard boundaries on fix directions.

## Step 2 — Branch and claim

Branch `rota/fix-<n>-<slug>` (`git checkout -b`; for a worktree, `git worktree add .claude/worktrees/<branch> -b <branch>`), then `rota status add <branch> --items <ID>` (worktree: add `--worktree <path>`). Claim: `rota item claim <ID> --as <branch>` (exit 4 means someone else holds it, stop; exit 3, 5, 6: stop and report). Start the Iron Law counter:

```bash
rota debug counter init <ID>
```

Exit 4 (`data.blockedBy` `iron law`) means the bug already has 3 failed fixes: go to *Iron Law stop* without reproducing.

## Step 3 — Build the feedback loop

The first deliverable, non-negotiable: a command that shows the bug and that you can re-run. Don't theorize from code before it exists. Take the cheapest rung that works:

1. **Existing test** — run the bug's test, if one exists.
2. **New failing test** — write one in the suite.
3. **CLI or HTTP call** — a `rota` verb, `curl`, or the app's entry point with the failing input.
4. **Replayed input** — captured payload, log line, file or seed data.
5. **Bisect** — "it worked before": `git bisect run <reproducer>`.
6. **Differential run** — same input on old and new (or two configs).
7. **Loop** — flaky bug: wrap any rung above in a loop (*Flaky bugs*).
8. **Human-in-the-loop script** — last resort: sets up state and prints the exact steps and pass/fail question for the user.

**Reproducer checklist.** All four before Step 4:

- **Red now** — it fails today, with a concrete signal (error message, wrong value, stack trace).
- **Deterministic** — same result every run; for a flaky bug, its failure rate is measured (below).
- **Fast** — seconds; trim a slow loop first.
- **Unaided** — you can run it without the user. Only rung 8 is exempt, and it says so in the report.

**Flaky bugs.** Run the reproducer N times (start at 20), record `fails/N`, and raise the rate (parallel runs, tighter timing, fixed seed, injected load) until one run is informative. Step 6 re-runs at the same N.

**Minimise.** Shrink the reproducer while it stays red, re-running after each cut, until any further removal turns it green. It becomes the regression test in Step 5.

If no rung produces a red run, stop and ask: *"Can't reproduce — need [X] from you (repro steps, environment, seed data)."*

## Step 4 — Hypothesize and verify

Write 3–5 hypotheses, ranked, each falsifiable: **"if X, then changing Y makes the bug vanish"**, with its probe (a line to read, a temporary trace, a targeted test). Print the list before probing; it is information, not a question, so don't wait for a reply.

```
H1 (likely): if the badge reads a stale timer ref, resetting it in pause() makes the bug vanish. Probe: trace in pause().
H2: if tick() early-returns on paused state, skipping the early return makes it vanish. Probe: read tick().
H3 (unlikely): if the formatter drops zero durations, formatting 0 explicitly makes it vanish. Probe: unit test.
```

Probe in rank order, before touching production code. Tag every temporary probe line (trace, log, scratch assertion) `[DEBUG-<id>]` (`<id>` = item number) so cleanup is a grep. Kept test files carry no tag.

- **Confirmed** — Step 5; drop the rest unprobed.
- **Refuted** — strike it with the evidence (`~~H1~~ refuted: pause() ran, ref was valid`), probe the next. Never fix-and-pray. When the list is exhausted, or after two refutations, re-read the symptom and reproducer from scratch and write a new list.

## Step 5 — Fix and commit

**Iron Law: no fix without a verified hypothesis.** Make the minimal diff that removes the root cause: no unrelated cleanup, unaffected callers unchanged. A large edit may go to a `standard` subagent with the verified root cause, files and constraints; tell it not to commit.

**Probe cleanup gate.** Remove every probe, then run `git grep -n "\[DEBUG-<id>\]"`. Any hit: remove and re-run. Don't commit or finish the cycle while it prints anything.

Commit one atomic fix with explicit paths (`git add <files>`, never `-A`):

```bash
git commit -m "fix: <short imperative> <ID>"
rota debug counter record-attempt --hypothesis "<one-line hypothesis>" --commit "$(git rev-parse --short HEAD)"
```

Legitimate toolchain siblings (e.g. Godot `.gd.uid`) go in a separate `chore:` commit.

**Regression test, or the missing seam.** Turn the Step 3 reproducer into a test at the bug's seam, in the same commit. It must fail without the fix. If the only test you can write mocks the thing under test or asserts implementation details (call order, private state, internal strings), it pins nothing: don't commit it. That means no seam exists: keep the reproducer as the proof and file the missing seam (Step 7).

## Step 6 — Verify the fix

Loop: re-run the Step 3 reproducer (flaky: N runs, zero failures), fix what still fails, re-run; continue only on a pass. The regression test must also run under the default test command (unless Step 5 found no seam). Record the outcome:

```bash
rota debug verdict <ID> --verdict <PASS|FAIL> --json
```

Route on `data.next`: `complete` Step 7; `hypothesize` the fix failed, back to Step 4 without keeping a partial fix; `halt` go to *Iron Law stop*.

## Iron Law stop

Three failed committed fixes: no further attempts. Print `rota debug counter summary` and suggest `/rota-pause` or reopening from a different angle. Further attempts are refused until a human runs `rota debug reset <ID> --reason "<why>"`, a manual gate (`references/manual-gates.md`): only after an `AskUserQuestion` yes, passing `--confirm --confirm-note "<their answer>"`, never on your own. Leave the branch and status entry; do not `rota item complete`; do not dispatch any continuation skill.

## Step 7 — Proof, PR, cleanup

Record the reproducer re-run as the proof row:

```bash
rota debug counter clear
rota proof add <ID> --check "<reproducer command>" --result PASS --evidence "<output line showing the symptom is gone>" --sha <commit-hash>
```

**No seam.** File the refactor item first (dedup and body shape as in `/rota-refactor`'s filing step), naming the missing seam and the bug it would have caught:

```bash
rota item create --json --kind tasks --title "<verb-first title naming the seam>" --desc "<one line>" --body-file <scratch> --related <ID>
rota issues label <number> --add refactor
```

The body needs `## Acceptance` boxes, one being a regression test for <ID> across the new seam. Check `rota tracker call -- issue list --label refactor --state all` for an open item on the same seam; if one exists, comment there instead of filing. File backend: `rota item create` alone. Then word the proof row to say no seam existed and link the item:

```bash
rota proof add <ID> --check "<reproducer command>" --result PASS --evidence "no regression test: no seam (<what would be mocked>); refactor #<n>" --sha <commit-hash>
```

Put the same line in the PR body.

Re-run `git grep -n "\[DEBUG-<id>\]"` once more; it must print nothing. Open a PR and hand it to review; never merge directly. The issue closes when the PR merges:

```bash
printf '%s' "$BODY" | rota ship pr <branch> --title "<short title>" --body-file - --items <ID>
rota item state <ID> --to needs-review
```

Keep the claim (no `rota item release`). **File backend:** `rota item complete <ID> --commit <hash>` instead; never pass `--no-proof` (exit 4 means Step 6 was skipped). Finally `rota status rm <branch>`; in umbrella mode (`umbrella.enabled` and the entry has a non-null `repo`) pass `--repo <repo>`, or the entry leaks into the next `/rota-work` run.

## Step 8 — Report

```
Fixed #42 Timer badge shows stale duration — commit a1b2c3d on `rota/fix-42-timer-badge`.
Root cause: MenuBarManager held an invalidated timer ref after pause; the next tick no-op'd.
Fix: reset badge to `--:--` in `pause()` before invalidating.
```

Nudge one line, only when it applies: cause not obvious from the code, *"Run `/rota-learn` to save this gotcha"*; fix locked in a boundary, *"`/rota-decide`"* (never auto-invoked). Then offer `/rota-ship`.

## Gates: Thought → Reality

| Thought | Reality |
|---|---|
| "The cause is obvious, skip the reproducer." | Step 4 starts only on a red, deterministic reproducer. An unreproduced bug has no proof row to write. |
| "One more fix attempt, it's close." | Three failed committed fixes is the stop. `rota debug counter` exits 4 and the count survives a new branch or session. |
| "Reset the counter so I can keep going." | `rota debug reset` is a manual gate: an `AskUserQuestion` yes and `--confirm-note`, never your own call. |
| "The fix works, keep the partial one while I try another." | `hypothesize` means the fix failed. Drop it before the next hypothesis. |
| "No seam for a test, so no proof." | The reproducer re-run is the proof row. Say no seam existed and link the `refactor` item. |
| "`--no-proof` to close it." | A missing row means Step 6 was skipped. Go back and run it. |

## Key principles

- **Feedback loop before hypothesizing, verify before fixing.** The reproducer is red, deterministic, fast, unaided and minimal before Step 4.
- **Hypotheses are plural, ranked, falsifiable.**
- **One fix, one commit.**
- **The ID closes the loop.** Commit carries the item ID, the PR carries `Closes #N`.

## References

- [`references/knowledge-consult.md`](references/knowledge-consult.md) — K+D query pattern.
