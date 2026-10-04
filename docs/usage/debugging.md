# Debugging

`/rota-debug` is for real bugs that need a proper cycle: reproduce, hypothesize, verify, fix. If the root cause is already obvious and the fix is mechanical, reach for [`/rota-capture`](capturing-work.md) (accept the hand-off) or [`/rota-work`](running-work.md) instead.

## /rota-debug

Invoke with a bug ID: `/rota-debug B07`.

The skill reads the `[B07]` entry in [`BACKLOG.md`](../reference/rota-folder.md) and any associated detail file, consults `KNOWLEDGE.md` for topics that match the bug's area, then works through a fixed cycle:

1. **Reproduce**: runs the bug's existing test or writes a minimal failing reproducer.
2. **Hypothesize**: the orchestrator model proposes a root cause based on the reproduction and project context.
3. **Verify**: the hypothesis is tested (log inspection, targeted reads, narrow experiment) before any code changes.
4. **Fix**: the worker model applies the fix. The reproducer must pass before the commit lands.
5. **Commit**: a single atomic commit tagged `fix: … [B07]` so `/rota-ship` can close the loop.

If the root cause was non-obvious (required extra verification rounds, or contradicted the initial hypothesis, or touched a known-tricky subsystem), the skill nudges you to run [`/rota-learn`](learning.md) so the insight lands in `KNOWLEDGE.md`.

`/rota-debug` uses the same isolation mode (`branch` or `worktree`) and model configuration as `/rota-work`. It refuses to start on a dirty working tree; stash or commit first.

## The cycle

Here is what the session looks like:

**Invoke**
```
/rota-debug B07
```

**Reproduce phase.** The skill runs the relevant test or writes a new failing one. You see something like:
```
Reproducer: tests/test_parser.py::test_empty_input  FAILED
```

**Hypothesis phase.** After reproduction, the orchestrator surfaces a root cause candidate:
```
Hypothesis: empty-string input bypasses the null-guard on line 42 because
  the guard tests `if not value` rather than `if value is None`.
```

**Verify phase.** Before touching code, the skill confirms the hypothesis holds (targeted read, probe, or narrow experiment). A short verification note appears:
```
Verified: `not ""` evaluates True, so the guard never fires for empty string.
```

**Fix and commit.** The worker model applies the minimal change, the reproducer is re-run, and the fix lands:
```
fix: guard against empty-string input in parser [B07]
```

If the root cause surprised you, the skill ends with a nudge:
```
Root cause was non-obvious. Consider running /rota-learn to capture this.
```

## When to use /rota-debug vs /rota-work

Use `/rota-debug` when you don't yet know the root cause and need the reproduce, hypothesize, verify loop. The cycle is the point.

Use `/rota-work` (or `/rota-capture` with its hand-off) when:

- The root cause is already clear and the fix is a small, mechanical change.
- The item is a feature or task, not a bug.
- You want lighter-weight dispatch without the hypothesis machinery.

See [running work](running-work.md) for a full comparison of the entry points.

## When the cycle won't converge

`/rota-debug` has two distinct circuit breakers: one for hypotheses that won't verify, one for fixes that won't hold. They count different things and trigger at different points in the cycle.

### Fresh-context escalation (hypothesize, verify won't converge)

If the hypothesize, verify loop iterates 3 times without finding the root cause, `/rota-debug` automatically escalates: it synthesizes a brief listing the refuted hypotheses, files inspected, and suspected blockers, then dispatches a fresh subagent with no transcript of the failed attempts. The fresh context often surfaces angles the orchestrator's accumulated weight has blocked.

If the fresh subagent's hypothesis also fails verification, `/rota-debug` surfaces to you rather than looping a second fresh-context attempt; at that point the bug needs human triage.

The 3-cycle threshold applies only in single-hypothesis mode. With `debug.competingHypotheses: true`, the three parallel framing lenses already cover the diverse-angles pattern.

### Iron Law: hard stop at 3 failed fixes

A second, stricter gate catches the case where hypotheses *do* converge (fixes get committed) but the bug keeps coming back. Every committed fix that fails to resolve the reproducer increments a per-session counter persisted at `.rota/debug/<session>.json` (session keyed by current branch, with `/` → `-`). The counter survives `/clear` and resumption.

At 3 failed fix attempts, the Iron Law fires: hard stop, no further attempts in this session. `/rota-debug` prints a fail-loud summary of every refuted hypothesis and committed fix, then surfaces. It does NOT dispatch a fresh-context worker (that's the hypothesis-cycle gate above, which targets a different failure mode). Three committed fixes that don't hold mean the framing of the bug needs human triage, not more agents.

Suggested next steps from the hard stop:

- Run [`/rota-pause`](pausing-and-resuming.md) to leave a handoff note and step away. A fresh session reads the persisted counter and can decide whether to wipe it or continue.
- Or re-open the bug from a different angle. The symptom may be in a subsystem the past three hypotheses haven't touched.

The branch and `status.json` entry stay intact so you can resume. The Iron Law breaks `autonomy.level: "loop"`: the loop stops at the hard stop and the user re-engages by hand. The counter clears on a successful fix so subsequent bugs start at zero.

## Competing hypotheses

The `debug.competingHypotheses` config option (default `false`) controls whether `/rota-debug` generates one hypothesis or fans out three parallel agents that attack the bug from different angles (recent changes, data shape, concurrency/lifecycle).

| Value | Behavior |
|-------|----------|
| `false` (default) | Single hypothesis agent. Cheaper and faster; fine for most bugs where one angle is obviously primary. |
| `true` | Three parallel hypothesis agents. Better framing diversity on hard bugs where the right angle isn't obvious upfront. Costs roughly 3× the orchestrator budget at the hypothesis step; latency stays similar because the agents run concurrently. |

Turn it on when you have a class of bugs that consistently take multiple cycles to resolve. Keep it off when most bugs are single-cause and the diversity isn't worth the cost.

See [configuration](configuration.md) for how to set this option.
