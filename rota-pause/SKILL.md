---
name: rota-pause
description: Gracefully pause mid-session — writes a handoff note (current hypothesis, next planned step, mid-edit files, uncommitted work strategy) to .rota/handoff/<branch>.md so `/rota-work` with no argument in a fresh session can pick up with full context, not just git state. Use when the session is approaching a context limit, you need to hand off, or you want to stop a long /rota-work cycle cleanly.
---

# rota-pause — Graceful Session Pause

`/rota-work` with no argument reads the handoff note on the next session; the note goes away when that stream is resumed or abandoned (`rota status rm` deletes it).

## When to Use

- Context window is filling up and you want to stop cleanly
- You have to step away mid-`/rota-work` or mid-`/rota-debug` cycle
- Work will continue in a new session; git commits alone won't carry the intent

## When NOT to Use

- Work is actually complete → `/rota-ship`
- You can finish in this session → just finish
- No active branch / no `/rota-work` running → nothing to hand off

## Step 1 — Task List

Track these phases with the host's task tool if it has one.

1. *Resolve pause set* — which `(branch, repo)` entries (Step 2)
2. *Handle uncommitted work* — user picks a strategy (Step 3)
3. *Write handoff* — one note per entry, status pinned (Steps 4-5)
4. *Report* — confirm (Step 6)

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
  1. "WIP commit (Recommended)" — *"`git add -A && git commit -m 'wip: pause before context cutoff'` — keeps changes on the branch."*
  2. "Stash" — *"`git stash push -u -m 'rota-pause <branch>'` — keeps changes out of history."*
  3. "Leave in place" — *"No action; the handoff will note that the tree is dirty."*

Apply the choice only to dirty entries, with `git -C <path>`. Record each entry's artifact (commit hash, stash ref, `dirty tree`, `clean tree`).

## Step 4 — Write the Handoff Note

Find the milestone first: `rota backlog milestones <ID>...` for the captured items. If none is listed, `rota milestone active`; include it only if exactly one is active. With several, use the one matching the paused items.

Write one note per `(branch, repo)` entry. Get the path from `rota status handoff "$BRANCH" --canonical ${REPO:+--repo "$REPO"}`. Fill the template in `references/handoff-template.md` from the session: omit sections that do not apply, do not invent content. Always overwrite.

In a wave, entries share Items, Milestone, Stage, Next planned step and Current hypothesis; only `Repo:` and the Uncommitted artifact differ. Keep separate files so one repo can be abandoned while the others resume.

Gotchas and dead ends belong in `/rota-learn` (Step 6), not the note.

## Step 5 — Pin Status

For each entry run `rota status add <branch> --items <ids> [--worktree <path>] [--repo <repo>] --if-absent` so the resume flow finds it. `--if-absent` keeps the original `startedAt`, so time in flight stays accurate; the note carries the pause time.

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
- **Do not delete the note here.** Resuming or abandoning the stream removes it.
- **No mutation beyond the note, status pin and the chosen wip commit or stash.** Capture, not integration.

## References

- [`references/handoff-template.md`](references/handoff-template.md) — Handoff-note template written by `/rota-pause`, read by `/rota-work`.
