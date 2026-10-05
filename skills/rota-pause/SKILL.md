---
name: rota-pause
description: Use when the session is approaching a context limit, the work must be handed off, a long /rota-work cycle should stop cleanly, or an orchestrator must stop mid-round.
---

# rota-pause — Graceful Session Pause

`/rota-work` with no argument reads the handoff note on the next session; the note goes away when that stream is resumed or abandoned (`rota status rm` deletes it).

## When to Use

- Context window is filling up and you want to stop cleanly
- You have to step away mid-`/rota-work` or mid-`/rota-debug` cycle, or mid-round as the orchestrator
- Work will continue in a new session; git commits alone won't carry the intent

## When NOT to Use

- Work is actually complete → `/rota-ship`
- You can finish in this session → just finish
- No active branch, no `/rota-work` running and no round → nothing to hand off
- The round is finished → `rota round wind-down`, not a pause

## Step 1 — Is a Round Running?

Run `rota round status --json`. A round is in flight when a row in `data.slots` holds an issue or `data.review` is non-empty; slots parked on `park/<agent>` with nothing queued are not a round. If so, follow *Pausing an orchestrator* below; the feature-branch steps do not apply, because the orchestrator sits on the base branch. Otherwise continue with Step 2.

## Step 2 — Resolve the Pause Set

The pause set is the `(branch, repo)` entries to pause: one for a single-repo cycle or a scoped umbrella pause, two or more when one `/rota-work` wave fanned out across sub-repos.

1. `rota status show`: take the active streams whose `branch` matches the current branch. One match is the pause set (`repo` may be null).
2. Several matches: run `rota repo which`. Exit 0 means the user `cd`-ed into one sub-repo, so pause only that entry. Exit 3 (umbrella root) means pause all matches as one wave. Do not ask which repo.
3. No match: use `git rev-parse --abbrev-ref HEAD` and pause `[(branch, null)]`, covering `/rota-pause` before `/rota-work` registered status.
4. `rota git guard feature-branch <branch>` exiting 1 means the base branch: tell the user there is no feature work to pause and stop.

## Step 3 — Handle Uncommitted Work

Check each entry's tree with `git -C <path> status --porcelain` (path from `rota repo resolve <repo> --json`, `data.repos[0].path`; cwd when `repo` is null).

All clean: record `clean tree` and continue. Any dirty: ask once via `AskUserQuestion`:

- **Header:** `"Uncommitted"`
- **Question:** *"N uncommitted files on `<branch>`. How should I handle them?"* For a wave, name the dirty repos instead of N.
- **Options** (single-select):
  1. "WIP commit (Recommended)" — *"Stage the dirty paths by name (`git add -- <paths from git status --porcelain>`), then `git commit -m 'wip: pause before context cutoff'`. Keeps changes on the branch."*
  2. "Stash" — *"`git stash push -u -m 'rota-pause <branch>'` — keeps changes out of history."*
  3. "Leave in place" — *"No action; the handoff will note that the tree is dirty."*

Apply the choice only to dirty entries, with `git -C <path>`. Record each entry's artifact (commit hash, stash ref, `dirty tree`, `clean tree`).

## Step 4 — Write the Handoff Note

Find the milestone first: `rota backlog milestones <ID>...` for the captured items. If none is listed, `rota milestone active`; include it only if exactly one is active. With several, use the one matching the paused items.

Write one note per `(branch, repo)` entry. Get the path from `rota status handoff "$BRANCH" --canonical ${REPO:+--repo "$REPO"}`. Fill the template in `references/handoff-template.md` from the session: omit sections that do not apply, do not invent content. Always overwrite.

In a wave, entries share Items, Milestone, Stage, Next planned step and Current hypothesis; only `Repo:` and the Uncommitted artifact differ. Keep separate files so one repo can be abandoned while the others resume.

Fill *Stage* and *Next planned step* from the task ledger ([`references/task-ledger.md`](references/task-ledger.md)): `rota git base`, then the `Task:` trailers in `git log <base>..HEAD`. Name the finished tasks and make the first unfinished one the next step.

Gotchas and dead ends belong in `/rota-learn` (Step 6), not the note.

## Step 5 — Pin Status

For each entry run `rota status add <branch> --items <ids> [--worktree <path>] [--repo <repo>] --if-absent` so the resume flow finds it. `--if-absent` keeps the original `startedAt`, so time in flight stays accurate; the note carries the pause time.

## Pausing an orchestrator

A round outlives the session that runs it: workers keep working in their own worktrees and tabs. A pause records the round and leaves it running; it never winds the round down (`rota round wind-down` re-verifies, parks every slot and releases the lease, which is the end of a round, not a pause) and never merges, reclaims or reassigns anything.

1. Read `rota round status --json`: the host, each slot's issue, state and PR, the `review` list (PRs waiting for gate and merge), open `escalations`, `limits` and the `drift` count. Add `rota round candidates` only if the slate is part of what the next session must decide.
2. Write the note to `.rota/handoff/<base>.md` (`<base>` from `rota git base`), first line `<!-- rota-handoff: orchestrator -->`, the marker the SessionStart hook injects without a lease. Sections: **Round** (host from `rota round status`; the round number from `.rota/workers.json`; the lease holder from `rota keepalive status` when a supervisor runs), **Slots** (one line each: agent, issue, state, PR), **Review queue** (PR numbers in merge order, and why that order), **Waiting on** (escalation ids and what each blocks, slots out of quota with reset time), **Next** (the one concrete call: usually `rota round wait`). Record decisions the maintainer settled that a worker brief does not already carry; leave out anything `rota round status` reproduces.
3. Leave the lease alone. A gone holder makes it stale, and the next `rota round start` reclaims it and keeps the round. Don't pin `rota status add`; the round's registry is `.rota/workers.json`.
4. Uncommitted work on the base branch: report it and leave it in place; don't WIP-commit onto the base.
5. Confirm in one block: round number, slots busy and free, PRs in review, what the next session does first. Resume is `rota round start` (it keeps the recorded scope) and then the note, with `/rota-orchestrate` for the judgment calls. Add the `/rota-learn` line below when it applies.

## Step 6 — Confirm

One compact block. Single entry:

```
Paused `rota/fix-B07-timer-badge` (web) — handoff saved.

Stage: mid-hypothesis verification for [B07]
Next: run the verification probe in MenuBarManager.swift:54
Uncommitted: wip commit a1b2c3d

Resume with `/rota-work` in a fresh session.
```

Show the `(web)` suffix only when `repo` is non-null. For a wave:

```
Paused `rota/api-refactor` across web, api — 2 handoffs saved.

Stage: implementing wave 2 of 3
Next: thread the new repo arg through rota status add --repos
Uncommitted:
  - web: wip commit a1b2c3d
  - api: clean tree

Resume with `/rota-work` in a fresh session.
```

Stage, Next and Hypothesis are shared across the wave; Uncommitted is per repo.

**Learn nudge (conditional).** Pausing loses context. If the session hit a durable gotcha (a hypothesis that contradicted assumptions, a non-obvious root cause, a tool quirk), add one line: *"Run `/rota-learn` now to preserve session insights durably — handoff captures intent, not learnings."* Skip if nothing non-obvious surfaced or `/rota-learn` already ran. Advisory only.

## Rules

- **Write what you know, not what you wish you knew.** The note is a snapshot of orchestrator state, not a task spec.
- **One note per `(branch, repo)`.** Overwrite on re-pause.
- **A multi-repo wave is one logical pause.** Scope to one sub-repo by `cd`-ing into it first.
- **Never commit `.rota/handoff/`.** It is per-developer scratch and gitignored by `rota init`.
- **Do not delete the note here.** Resuming or abandoning the stream removes it; the orchestrator note is consumed by the next session's hook.
- **A paused round is not a finished round.** Never wind down, merge or reclaim from a pause.
- **No mutation beyond the note, status pin and the chosen wip commit or stash.** Capture, not integration.

## References

- [`references/task-ledger.md`](references/task-ledger.md) — `Task:` commit trailer; read for the note's Stage and Next planned step.
- [`references/handoff-template.md`](references/handoff-template.md) — Handoff-note template written by `/rota-pause`, read by `/rota-work`.
