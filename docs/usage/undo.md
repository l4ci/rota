# Rolling back a cycle

`/rota-ship --undo` inverts [`/rota-work`](running-work.md)'s commit and completion steps. It resets the base branch past a cycle's merge commit and reopens the items that cycle completed, which moves their entries from `## Completed` back to their type sections. The default mode is a dry run with a structured preview; nothing is written until you confirm. It covers direct-merge cycles only. Cycles shipped through `/rota-ship`'s PR path are refused, and in [issue mode](issue-backend.md) `/rota-ship` always opens a PR, so there is no direct-merge cycle to undo.

## /rota-ship --undo

```
/rota-ship --undo
```

With no arguments, `/rota-ship --undo` targets the most recent `merge: ...` commit on the base branch. The skill prints a preview, asks for confirmation, and only writes after you pick *Apply*. To target a specific cycle, invoke the engine directly: `rota ship undo --cycle <hash>` (rare; useful when you've made unrelated commits since and want to roll back further with `--allow-post-merge`).

## Worked example

You finished `[F42]` an hour ago via [`/rota-work`](running-work.md), `/rota-ship` direct-merged it, and the entry is now in `## Completed`:

```markdown
## Completed
- ~~**[F42] [Major] Inline preview for share links.**~~ landed 2026-05-13
```

You realize the preview implementation conflicts with a milestone constraint that landed yesterday. Run:

```
/rota-ship --undo
```

The skill prints the rollback plan:

```
Undo plan for last cycle: 4d2f8b1

Subject:  merge: F42 — inline preview for share links
Base:     main will reset --hard 4d2f8b1^1 (currently 4d2f8b1)
Items:    F42 will be restored to BACKLOG.md (Features)

Branch:   deleted by rota ship merge; rerun `git branch <name> 4d2f8b1^2` to keep the work
Status:   no active entry to clear (cycle already removed it)
Handoff:  gitignored — not restorable
Plans:    removed on ship for milestone items — not restored

Re-run with --apply to apply.
```

The skill then asks for confirmation through the standard *Apply* / *Cancel* picker. Pick *Apply* and the skill runs `rota ship undo --apply`, which applies the changes and reports:

```
Undone cycle 4d2f8b1. Reset main to 7c91a2e. Restored: F42.
```

Re-run [`/rota-work` (no argument)](picking-work.md) and `[F42]` shows up under Features again, ready to be re-planned or replaced. The detail file at `.rota/features/F42.md` is untouched; only the active backlog state and the merge commit moved. If you want to amend the item's description before re-running, edit the detail file directly and `/rota-work` (no argument) will pick up the new wording on its next pass.

## What gets rolled back vs. preserved

`/rota-ship --undo` rolls back the **merge commit on the base branch** (the base is reset to the commit immediately before it) and the cycle's **TODO entries** (reopened and moved from `## Completed` back to their original type sections under `## Features`, `## Bugs`, or `## Tasks`). An item that is already active again is left alone and reported as a no-op.

Entries that `/rota-work` already moved to `.rota/ARCHIVE.md` are restored too: the cycle's done lines are found in `## Completed` and in `ARCHIVE.md`, and an archived one is moved out of `ARCHIVE.md` back to its type section. Other `ARCHIVE.md` entries are left alone.

Preserved untouched: the **git reflog** (the merge commit is still recoverable for 90 days via `git reflog`) and **git objects** generally. The merged branch's commits stay reachable through the reflog, so nothing is irretrievably lost in the short term.

Not restored, by design: **handoff files** (`.rota/handoff/<branch>.md` are gitignored per-developer scratch and were lost when the branch was deleted at merge time), **plan files** (`.rota/plans/<key>.md` are tracked. `/rota-work` removes a milestone-tagged item's plan when the item ships, and `undo` does not bring it back; recover it from the merge's second parent or the reflog), and **the merged branch itself** (direct-merge deletes it at ship time). The dry-run preview prints the literal `git branch …` command needed to recreate the branch from `<merge>^2` if you want to keep iterating on the same line of work.

## Safety semantics

`/rota-ship --undo` enforces four guards before it will apply anything.

**Clean tree required.** A dirty working tree is refused (exit 4). Commit, stash, or discard your in-flight changes before rolling back; `git reset --hard` cannot run safely otherwise.

**Base branch required.** `/rota-ship --undo` must run on the base branch (whatever [`rota git base`](../reference/cli-helpers.md) returns, usually `main` or `master`). Running from a feature branch refuses with a pointer to switch first.

**Post-merge guard.** If the base branch has commits past the cycle merge, `/rota-ship --undo` refuses by default:

```
error: base has 2 commits past merge 4d2f8b1; pass --allow-post-merge to discard them, or git reset to before them first
```

Two ways to resolve. Either `git reset --hard <merge>` to drop the post-merge commits explicitly first, then re-run `/rota-ship --undo`. Or pass `--allow-post-merge` to discard them as part of the rollback:

```
/rota-ship --undo --allow-post-merge
```

The latter works when the post-merge commits are local and disposable; prefer the explicit reset when you want a clear two-step audit trail.

Either path is destructive on the post-merge commits. They leave the active branch tip but remain recoverable through the reflog for 90 days. If any of those commits matter, cherry-pick them onto a feature branch before rolling back.

**PR-mode refused.** Cycles shipped via [`/rota-ship`](review-and-ship.md)'s PR strategy can't be rolled back through `/rota-ship --undo`. The PR's state on the remote is the source of truth for the merge, and rewriting local history doesn't undo a merged PR. Use `gh pr close <num>` for an open PR or `git revert <merge-sha>` for one that already landed.

## Manual gate

The confirmation step is asked every time, including when [`autonomy.level`](autonomy.md) is set to `auto`. This mirrors the destructive-gate convention [`/rota-capture --remove`](removing-work.md) uses: `git reset --hard` is recoverable through the reflog only inside the 90-day window, and the gate guarantees a human signed off before the reset runs. No flag suppresses the prompt.

## What `/rota-ship --undo` is NOT for

- Editing what landed. If the work is fine but needs a tweak, capture a new fix via [`/rota-capture`](capturing-work.md) + [`/rota-work`](running-work.md). Don't roll back just to redo.
- Partial rollback. `/rota-ship --undo` rolls the entire cycle back as a unit. To revert one task from a multi-task cycle, `git revert <task-commit>` is the right tool.
- Rolling back more than one cycle at once. Invoke `/rota-ship --undo` twice, confirming each step independently.
- PR-mode cycles. See *Safety semantics* above; use `gh pr close` or `git revert` instead.

## When to use

- A landed cycle conflicts with a milestone constraint or decision that surfaced after the merge.
- The work shipped against the wrong premise: the implementation is correct, but the item itself was wrong.
- You want the cycle's `[Major]` feature back on the backlog under a new design. Roll it back, capture the replacement, replan.
- A direct-merge cycle landed on top of unrelated local commits you didn't mean to ship; combine `--allow-post-merge` with care.

If only one task in a multi-task cycle is wrong, prefer `git revert <task-commit>`. It preserves the rest of the cycle's history and leaves the TODO entries archived.

## Hosts without a picker

On hosts where `AskUserQuestion` is unavailable, `/rota-ship --undo` asks in prose: `Apply rollback? (yes/no)`. The semantics are identical: nothing writes until you answer `yes`. The preview block above the prompt is the same structured plan rendered for the picker, so the decision surface stays the same regardless of host.
