# Removing work

`/rota-capture --remove` permanently removes backlog entries from [`BACKLOG.md`](../reference/rota-folder.md), their associated files, and any cross-references. It's the local inverse of plain [`/rota-capture`](capturing-work.md).

## /rota-capture --remove

```
/rota-capture --remove F99
```

Default mode is a dry run. The skill shows what would change and asks for confirmation before writing anything. Nothing is modified until you say yes.

## Worked example

You captured a feature two days ago:

```markdown
- **[F99] [Major] Redesign the splash screen.** ...
```

Later you find it's a duplicate of `[F42]`. Run:

```
/rota-capture --remove F99
```

The skill prints a structured preview:

```
[F99] removal plan:
  TODO entry: .rota/BACKLOG.md ## Features (line removed)
    - **[F99] [Major] Redesign the splash screen.** ...
  Cross-references to strip: 1
    .rota/BACKLOG.md ## Bugs [B12]: Related: [F99] → (removed)
  Detail file: .rota/features/F99.md (delete)
  Plan files: 0
  ARCHIVE: untouched (use --scrub-archive to mirror)
  Active stream: none

dry-run: no files modified.
```

The skill then asks for confirmation with three options: *Apply (Recommended)*, *Apply + scrub ARCHIVE*, *Cancel*. Pick *Apply* and the helper runs again with `--force`, applies the changes, and reports:

```
[F99] removed: TODO entry, 1 cross-reference, detail file
```

## What gets cleaned vs. preserved

| Target | Default | With `--scrub-archive` |
|--------|---------|------------------------|
| `BACKLOG.md` active entry | removed | removed |
| `Related:` cross-references in active `BACKLOG.md` | removed | removed |
| `.rota/features/<ID>.md` / `.rota/bugs/<ID>.md` / `.rota/tasks/<ID>.md` | deleted if present | deleted if present |
| `.rota/plans/<milestone>-<ID>.md` | deleted if present | deleted if present |
| `status.json` active entry | stripped with `--force`; refused otherwise | same |
| `ARCHIVE.md` entry | preserved | removed |
| ID counters in `counters.json` | not decremented; ID stays claimed | not decremented |
| GitHub issue references | not touched | not touched |

Counters never decrement. An ID removed today won't be reissued to a different item tomorrow; gaps in the sequence are intentional and prevent ID collisions in git history.

Close GitHub issues upstream manually. `/rota-capture --remove` has no knowledge of remote trackers.

## Safety semantics

`/rota-capture --remove` refuses to apply until you confirm. The confirmation gate runs even when [`autonomy.level`](autonomy.md) is set to `auto`; removal is always a manual step.

Active items (items present in any `status.json` `items` array) are refused by default:

```
error: [F99] is active on branch rota/redesign-splash; pass --force to strip from active stream
```

Pass `--force` to override. With `--force`, the ID is stripped from the entry's items list (the entry is dropped if it becomes empty) and a warning is written to stderr. `--force` doesn't touch any branch or worktree; it removes only the backlog record. Branch commits survive unchanged.

## CSV / batch usage

Remove multiple items in one pass by separating IDs with commas:

```
/rota-capture --remove B01,F03,T05
```

Validation is all-or-nothing. If any ID in the list is unknown or invalid, the entire batch aborts before any write. Fix the offending ID and re-run.

The dry-run preview lists every item in the batch so you can review the full scope before confirming.

## When to use

- Duplicate captures: same item filed twice under different IDs.
- Wrong-premise items: you captured something that turned out not to be real.
- Items obsoleted by other work: a refactor made the item moot before it was started.
- Items captured against the wrong project: filed here, should have been filed upstream.

If an item is done rather than unwanted, use [/rota-work](running-work.md) to complete it. Completed items are archived, not removed.

## What /rota-capture --remove is not

`/rota-capture --remove` isn't a soft-delete or an undo mechanism. Once applied, the entry is gone from the active backlog. `ARCHIVE.md` keeps a historical record by default; `--scrub-archive` erases it there too.

`/rota-capture --remove` doesn't close GitHub issues. Close upstream issues manually after removing a backlog entry.

`/rota-capture --remove --force` only removes the backlog record for an active item. It won't delete the branch, revert commits, or discard work in progress. To abandon a branch, use git directly after removing the backlog entry.
