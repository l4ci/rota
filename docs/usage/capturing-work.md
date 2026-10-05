# Capturing work

`/rota-capture` records bugs, features, and tasks into the backlog with auto-classification and auto-assigned IDs. Where they land depends on `backlog.backend`: on the default `"file"` backend, in `.rota/BACKLOG.md`; on the `"issues"` backend, as GitHub or GitLab issues. See [Issue backend](issue-backend.md). Paste a raw description, structured notes, or a mixed list; it sorts the rest out.

The examples below show the file backend. On the issue backend the same classification becomes labels (`type:bug`, `p1`, `size:Major`), and the ID is the issue number (`#42`).

## /rota-capture

```
/rota-capture "the sidebar flickers on hover"
```

That single line logs a bug entry:

```markdown
- **[B03] [P2] Sidebar flickers on hover.** ...
```

On the file backend each item gets a zero-padded, auto-incrementing ID (`[B##]` for bugs, `[F##]` for features, `[T##]` for tasks). The skill asks a few quick questions for context, then assigns:

- **Bugs**: priority `[P0]`, `[P1]`, or `[P2]`
- **Features**: size `[Major]`, `[Minor]`, or `[Cosmetic]`
- **Tasks**: no tag

## Mixed input: bugs, features, and tasks in one message

You don't need to file items one at a time. Describe everything at once and `/rota-capture` splits it into distinct entries routed to the correct section.

Telling `/rota-capture` *"the sidebar flickers on hover, also we should add keyboard shortcuts, and update the linter config"* produces:

```markdown
## Bugs
- **[B03] [P2] Sidebar flickers on hover.** ...

## Features
- **[F04] [Minor] Keyboard shortcuts for top actions.** ...

## Tasks
- **[T06] Update linter config for new rules.** ...
```

Each item gets its own ID type, section, and classification, regardless of how they arrived in one message. Items captured in the same batch can reference each other via `Related:` links.

## Detail files for large input

When an item's input is too large for a TODO entry (crash dumps, specs, logs, long checklists), `/rota-capture` creates a detail file and links to it from the main entry (on the issue backend the text goes into the issue body instead):

```markdown
- **[B07] [P0] App crashes on launch after iOS 18.2 update.** EXC_BAD_ACCESS in CoreData stack during migration. Detail: `.rota/bugs/B07.md` Related: [F12]
```

Detail files land in type-specific subdirectories:

| Type | Directory | Example |
|------|-----------|---------|
| Bug | `.rota/bugs/` | `.rota/bugs/B07.md` |
| Feature | `.rota/features/` | `.rota/features/F08.md` |
| Task | `.rota/tasks/` | `.rota/tasks/T09.md` |

Most entries won't need a detail file; they're only created when the input would bloat the TODO entry beyond a few sentences.

## Related items

Any item can carry a `Related:` suffix linking it to other items:

```markdown
- **[B05] [P1] Timer badge stale after pause.** Description... Related: [F03]
```

Links are optional. [`/rota-work` (no argument)](picking-work.md) infers the reverse link automatically, so you don't need to add it to both sides. When linked items form clusters, `/rota-work` (no argument) suggests tackling them together (see [picking work](picking-work.md)).

`/rota-capture` scans the open items for connections (on the file backend also [`ARCHIVE.md`](../reference/rota-folder.md)), so a new bug can link back to a completed feature.

## Milestone-spec audit

When you capture from a milestone spec (the text references an `M<NN>` tag or a `milestones/M<NN>.md` path), `/rota-capture` runs a ship-evidence audit before writing anything. Old specs drift behind code. Acceptance criteria written months ago often ship under different IDs, and capturing them again creates duplicate work.

The audit runs [`rota item shipped`](../reference/cli-helpers.md) against every parsed title and groups matches into three confidence tiers: `[STRONG]` (commit subject + title overlap), `[MEDIUM]` (subject hint only), `[PATH]` (file path hint). For each flagged title you get three options:

- **Skip** (recommended). Drop the title from this capture run.
- **Capture anyway.** You've reviewed the matches and confirmed the item is genuinely distinct (prior commit was an incomplete first pass, etc.).
- **Stop the whole capture.** Abort and reconcile the milestone spec via [/rota-vision](vision-and-plans.md) or by hand before retrying.

Ordinary brain-dump captures (no `M<NN>` reference) skip the audit entirely.

## What /rota-capture is not

`/rota-capture` is a pure recording tool. It classifies and files. It does not act, validate the item, or deduplicate against existing entries. It prints the new IDs and stops; it never starts work. To implement an item, run [/rota-work](running-work.md) on its ID. To remove a captured item that turned out to be a duplicate or wrong-premise, use [`/rota-capture --remove`](removing-work.md).
