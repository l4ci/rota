# Debugging

`/rota-debug` is for real bugs that need a proper cycle: reproduce, hypothesize, verify, fix. If the root cause is already obvious and the fix is mechanical, capture it with [`/rota-capture`](capturing-work.md) and use [`/rota-work`](running-work.md) instead.

## /rota-debug

Invoke with a bug ID: `/rota-debug 42` on the [issue backend](issue-backend.md), `/rota-debug B07` on the file backend.

The skill loads the item (its issue, or the `[B07]` entry in [`BACKLOG.md`](../reference/rota-folder.md) and any associated detail file), consults `KNOWLEDGE.md` for topics that match the bug's area, then works through a fixed cycle:

1. **Build a feedback loop**: before any theory, it builds a command that shows the bug, taking the cheapest rung of a ladder: existing test, new failing test, CLI or HTTP call, replayed input, `git bisect`, old-vs-new differential run, a loop for flaky bugs, or a script for you to run. The reproducer must be red now, deterministic (or have a measured failure rate), fast and runnable without you. A flaky bug is run N times and its rate raised first. Then the skill minimises the reproducer, and the minimal version becomes the regression test.
2. **Hypothesize**: it writes 3–5 hypotheses ranked by likelihood, each as a falsifiable claim ("if X, then changing Y makes the bug vanish"), and prints the list before probing.
3. **Verify**: it probes in rank order (log inspection, targeted reads, narrow experiment) before any code changes, striking each refuted hypothesis with its evidence. Temporary probe lines carry a `[DEBUG-<id>]` tag, and the skill will not commit or finish while `git grep` still finds one.
4. **Fix**: the minimal change that removes the root cause, as one atomic commit tagged `fix: … [B07]` (`#42` on the issue backend).
5. **Prove and hand off**: the reproducer must pass, and the fix carries a regression test at the bug's seam. If the only possible test would mock the code under test or assert internals, the skill skips it, files a `refactor` item naming the missing seam, and records the reproducer as the proof with a row that links that item. It opens a PR. It never merges. The issue closes when the PR merges; on the file backend it completes the item.

If the root cause was non-obvious (required extra verification rounds, or contradicted the initial hypothesis, or touched a known-tricky subsystem), the skill nudges you to run [`/rota-learn`](learning.md) so the insight lands in `KNOWLEDGE.md`.

`/rota-debug` uses the same `work.isolation` setting (`branch` or `worktree`) as `/rota-work`. It refuses to start on a dirty working tree; stash or commit first.

## The cycle

Here is what the session looks like:

**Invoke**
```
/rota-debug B07
```

**Feedback-loop phase.** The skill builds and minimises a reproducer. You see something like:
```
Reproducer: tests/test_parser.py::test_empty_input  FAILED
```

**Hypothesis phase.** After reproduction, the skill prints a ranked list and starts probing without waiting for a reply:
```
H1 (likely): if the null-guard on line 42 tests `if not value`, testing `is None` makes the bug vanish.
H2: if the parser strips input before the guard, skipping the strip makes it vanish.
H3 (unlikely): if the caller passes "" by mistake, fixing the caller makes it vanish.
```

**Verify phase.** Before touching code, the skill confirms the hypothesis holds (targeted read, probe, or narrow experiment). A short verification note appears:
```
Verified H1: `not ""` evaluates True, so the guard never fires for empty string.
```

**Fix and commit.** The minimal change is made, the reproducer is re-run, and the fix lands:
```
fix: guard against empty-string input in parser [B07]
```

If the root cause surprised you, the skill ends with a nudge:
```
Root cause was non-obvious. Consider running /rota-learn to capture this.
```

## When to use /rota-debug vs /rota-work

Use `/rota-debug` when you don't yet know the root cause and need the reproduce, hypothesize, verify loop. The cycle is the point.

Use `/rota-capture` then `/rota-work` when:

- The root cause is already clear and the fix is a small, mechanical change.
- The item is a feature or task, not a bug.
- You want lighter-weight dispatch without the hypothesis machinery.

See [running work](running-work.md) for a full comparison of the entry points.

## When the cycle won't converge

### Hypotheses that won't verify

A refuted hypothesis is struck from the list with its evidence and the next one is probed, never a fix-and-pray. When the list runs out, or after two refuted hypotheses, `/rota-debug` re-reads the symptom and the reproducer from scratch and writes a new ranked list.

### Iron Law: hard stop at 3 failed fixes

Each committed fix is recorded as PASS or FAIL for the bug (`rota debug verdict`). Failed fixes are counted per item in `.rota/verdicts.json`, so a new branch or session does not reset the count; a per-branch attempt log sits in `.rota/debug/<session>.json` (session keyed by current branch, with `/` → `-`).

At 3 failed fixes, the Iron Law fires: hard stop, no further attempts. `/rota-debug` prints a summary of every refuted hypothesis and committed fix (`rota debug counter summary`), then surfaces. Three committed fixes that don't hold mean the framing of the bug needs human triage, not more agents.

Suggested next steps from the hard stop:

- Run [`/rota-pause`](pausing-and-resuming.md) to leave a handoff note and step away.
- Or re-open the bug from a different angle. The symptom may be in a subsystem the past three hypotheses haven't touched.

Further attempts are refused until a human runs `rota debug reset <ID> --reason "<why>"`, a manual gate that asks first. The branch and `status.json` entry stay intact so you can resume. The Iron Law is a hard stop at every autonomy level; the user re-engages by hand. A successful fix clears the attempt log.

