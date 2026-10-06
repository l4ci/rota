---
name: rota-pause
description: Use when the session is approaching a context limit, the work must be handed off, a long /rota-work cycle should stop cleanly, or an orchestrator must stop mid-round.
---

# rota-pause — Graceful Session Pause

`/rota-work` with no argument reads the handoff note next session; it goes away when the stream is resumed or abandoned (`rota status rm` deletes it).

## When NOT to Use

- Work complete → `/rota-ship`
- You can finish in this session
- No active branch, no `/rota-work` running and no round
- Round finished → `rota round wind-down`

Copy this checklist and track your progress:
```
- [ ] Step 1 — Is a Round Running?
- [ ] Step 2 — Resolve the Pause Set
- [ ] Step 3 — Handle Uncommitted Work
- [ ] Step 4 — Write the Handoff Note
- [ ] Step 5 — Pin Status
- [ ] Step 6 — Confirm
```

## Step 1 — Is a Round Running?

Run `rota round status --json`. A round is in flight when a row in `data.slots` holds an issue or `data.review` is non-empty; slots parked on `park/<agent>` with nothing queued are not a round. If in flight, read [`orchestrator-pause.md`](orchestrator-pause.md) and follow it; Steps 2-5 do not apply (the orchestrator sits on the base branch). Otherwise continue with Step 2.

## Step 2 — Resolve the Pause Set

The pause set is the `(branch, repo)` entries to pause: one for a single-repo cycle or scoped umbrella pause, several when a `/rota-work` wave fanned out across sub-repos.

1. `rota status show`: take the active streams whose `branch` matches the current branch. One match is the pause set (`repo` may be null).
2. Several matches (a `/rota-work` wave across sub-repos): read [`umbrella-wave.md`](umbrella-wave.md), *Step 2*.
3. No match: use `git rev-parse --abbrev-ref HEAD` and pause `[(branch, null)]`, for `/rota-pause` before `/rota-work` registered status.
4. `rota git guard feature-branch <branch>` exiting 1 means the base branch: tell the user there is no feature work to pause and stop.

## Step 3 — Handle Uncommitted Work

Check each entry's tree with `git -C <path> status --porcelain` (path from `rota repo resolve <repo> --json`, `data.repos[0].path`; cwd when `repo` is null).

All clean: record `clean tree`. Any dirty: ask once via `AskUserQuestion`:

- **Header:** `"Uncommitted"`
- **Question:** *"N uncommitted files on `<branch>`. How should I handle them?"* For a wave, name the dirty repos instead of N.
- **Options** (single-select):
  1. "WIP commit (Recommended)" — *"Stage the dirty paths by name (`git add -- <paths from git status --porcelain>`), then `git commit -m 'wip: pause before context cutoff'`. Keeps changes on the branch."*
  2. "Stash" — *"`git stash push -u -m 'rota-pause <branch>'` — keeps changes out of history."*
  3. "Leave in place" — *"No action; the handoff note will record that the tree is dirty."*

Apply the choice only to dirty entries, with `git -C <path>`. Record each entry's artifact (commit hash, stash ref, `dirty tree`, `clean tree`).

## Step 4 — Write the Handoff Note

Find the milestone first: `rota backlog milestones <ID>...` for the captured items. If none is listed, `rota milestone active`; include it only if exactly one is active. With several, use the one matching the paused items.

Write one handoff note per `(branch, repo)` entry. Get the path from `rota status handoff "$BRANCH" --canonical ${REPO:+--repo "$REPO"}`. Fill the template in `references/handoff-template.md` from the session: omit sections that do not apply, do not invent content. Always overwrite.

In a wave, read *Step 4* in [`umbrella-wave.md`](umbrella-wave.md).

Fill *Stage* and *Next planned step* from the task ledger ([`references/task-ledger.md`](references/task-ledger.md)): `rota git base`, then the `Task:` trailers in `git log <base>..HEAD`. Name the finished tasks and make the first unfinished one the next step.

Gotchas and dead ends go to `/rota-learn` (Step 6), not the note.

## Step 5 — Pin Status

For each entry run `rota status add <branch> --items <ids> [--worktree <path>] [--repo <repo>] --if-absent` so resume finds it. `--if-absent` keeps the original `startedAt`; the note carries the pause time.

## Step 6 — Confirm

One compact block. Wave: name every repo, count handoffs, list Uncommitted per repo.

```
Paused `rota/fix-B07-timer-badge` (web) — handoff note saved.

Stage: mid-hypothesis verification for [B07]
Next: run the verification probe in MenuBarManager.swift:54
Uncommitted: wip commit a1b2c3d

Resume with `/rota-work` in a fresh session.
```

Show the `(web)` suffix only when `repo` is non-null.

**Learn nudge (conditional).** If the session hit a durable gotcha (a hypothesis that contradicted assumptions, a non-obvious root cause, a tool quirk), add one line: *"Run `/rota-learn` now to preserve session insights durably — handoff captures intent, not learnings."* Skip if nothing non-obvious surfaced or `/rota-learn` already ran.

## Rules

- **Write what you know.** The note is a state snapshot, not a task spec.
- **One handoff note per `(branch, repo)`.** Overwrite on re-pause.
- **A multi-repo wave is one logical pause.** `cd` into a sub-repo to scope to it.
- **Never commit `.rota/handoff/`** (gitignored per-developer scratch).
- **Do not delete the handoff note here.** Resume or abandon removes it; the next session's hook consumes the orchestrator note.
- **A paused round is not a finished round.** Never wind down, merge or reclaim from a pause.
- **No mutation beyond the handoff note, status pin and the chosen wip commit or stash.**

## References

- [`orchestrator-pause.md`](orchestrator-pause.md) — pausing an orchestrator mid-round; read from Step 1 when a round is in flight.
- [`umbrella-wave.md`](umbrella-wave.md) — multi-repo wave pauses; read from Steps 2 and 4.
- [`references/task-ledger.md`](references/task-ledger.md) — `Task:` commit trailer; read for the handoff note's Stage and Next planned step.
- [`references/handoff-template.md`](references/handoff-template.md) — Handoff note template written by `/rota-pause`, read by `/rota-work`.
