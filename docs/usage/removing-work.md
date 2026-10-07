# Removing work

`/rota-capture --remove` permanently removes backlog entries from [`BACKLOG.md`](../reference/rota-folder.md), their associated files, and any cross-references. It's the local inverse of plain [`/rota-capture`](capturing-work.md).

It works on the file backend only (`rota item rm` refuses under `backlog.backend: "issues"`). On the [issue backend](issue-backend.md) the issue is the item: close it with `rota item complete <ID> --reason dropped`.

## /rota-capture --remove

```
/rota-capture --remove F99
```

Default mode is a dry run. The skill shows what would change and asks for confirmation before writing anything. Nothing is modified until you say yes.

## 🧪 Worked example

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
F99: would remove TODO entry, 1 cross-reference(s), detail file
preview only; pass --apply
```

The skill then asks for confirmation with three options: *Apply (Recommended)*, *Apply + scrub ARCHIVE*, *Cancel*. Pick *Apply* and the skill runs `rota item rm --apply F99`, which applies the changes and reports:

```
F99: removed TODO entry, 1 cross-reference(s), detail file
```

## What gets cleaned vs. preserved

| Target | Default | With `--scrub-archive` |
|--------|---------|------------------------|
| `BACKLOG.md` active entry | removed | removed |
| `Related:` cross-references in active `BACKLOG.md` | removed | removed |
| `.rota/features/<ID>.md` / `.rota/bugs/<ID>.md` / `.rota/tasks/<ID>.md` | deleted if present | deleted if present |
| `.rota/plans/<milestone>-<ID>.md` | deleted if present | deleted if present |
| Item active in `status.json` | apply refused until the stream ends | same |
| `ARCHIVE.md` entry | preserved | removed |
| ID counters in `counters.json` | not decremented; ID stays claimed | not decremented |
| Upstream issues (legacy `GH: #N` tags) | not touched; the skill offers to remove the `in-progress` label | same |

Counters never decrement. An ID removed today won't be reissued to a different item tomorrow; gaps in the sequence are intentional and prevent ID collisions in git history.

Close upstream issues manually. `/rota-capture --remove` never closes them.

## 🔒 Safety semantics

`/rota-capture --remove` refuses to apply until you confirm. The confirmation gate runs even when [`autonomy.level`](autonomy.md) is set to `auto`; removal is always a manual step.

Active items (items present in any `status.json` `items` array) are refused when you apply:

```
error: [F99] is active on branch rota/redesign-splash
```

The preview still works and marks the branch. End the stream first with `rota status rm <branch>`, then re-run. There is no `--force`. Ending the stream touches only the status entry (and drops that branch's handoff note); it doesn't touch any branch or worktree, and branch commits survive unchanged.

## CSV / batch usage

Remove multiple items in one pass by separating IDs with commas:

```
/rota-capture --remove B01,F03,T05
```

Validation is all-or-nothing. If any ID in the list is unknown or invalid, the entire batch aborts before any write. Fix the offending ID and re-run.

The dry-run preview lists every item in the batch so you can review the full scope before confirming.

## 🎯 When to use

- Duplicate captures: same item filed twice under different IDs.
- Wrong-premise items: you captured something that turned out not to be real.
- Items obsoleted by other work: a refactor made the item moot before it was started.
- Items captured against the wrong project: filed here, should have been filed upstream.

If an item is done rather than unwanted, use [/rota-work](running-work.md) to complete it. Completed items are archived, not removed.

## 🚫 What /rota-capture --remove is not

`/rota-capture --remove` isn't a soft-delete or an undo mechanism. Once applied, the entry is gone from the active backlog. `ARCHIVE.md` keeps a historical record by default; `--scrub-archive` erases it there too.

`/rota-capture --remove` doesn't close upstream issues. Close them manually after removing a backlog entry.

It never deletes a branch, reverts commits, or discards work in progress. To abandon a branch, end its stream with `rota status rm <branch>` and delete the branch with git.
