---
skill: rota-capture
lane: capture
order: 10
situation: Something to do
summary: Record bugs, features and tasks in the backlog without starting work.
not_when:
  - use: rota-work
    when: the item is already captured and needs implementing
  - use: rota-debug
    when: a bug needs a proper root-cause cycle now, not a backlog entry
  - use: rota-brainstorm
    when: a captured Major item needs its design negotiated first
before: []
after: [rota-brainstorm, rota-plan, rota-work]
manual_only: false
flags:
  - flag: "--remove <ID>"
    does: remove a captured item and clean up its dependencies; dry-run first, asks before applying
  - flag: "--triage <N>"
    does: vet a tracker issue someone else filed; creates no item, ends in closed, needs-info or ready
example:
  source: walkthroughs/quickstart-manual-mode.md#step-1-capture
  reviewed: true
  steps:
    - you: /rota-capture the list command shows every row when I ask for zero
    - agent: |
        [B03] list shows every row when the limit is zero   Bug, P2
  changes:
    - a new row under `## Bugs` in `.rota/BACKLOG.md`
    - nothing else; capture records and stops
---
# /rota-capture

Captures bugs, features and tasks into the backlog with `rota item create`. Where the item lands depends on `issues.source`. On the file backend (default) it goes into [`BACKLOG.md`](../reference/rota-folder.md) under the correct section with a zero-padded auto-incrementing ID (`[B01]`, `[F01]`, `[T01]`). On the issue backend it becomes a GitHub or GitLab issue with type, priority and size labels, and the ID is the issue number (`#42`); see [issue backend](../usage/issue-backend.md). Either way it classifies each item automatically and assigns priority (P0/P1/P2) for bugs and size (Major/Minor/Cosmetic) for features. See [capturing work](../usage/capturing-work.md) for the full flow.

## --triage

Vets tracker issues filed by someone else: redundancy search, prior-decision check and, for bugs, a bounded reproduce. Creates no item. Each issue ends in one outcome you confirm: closed with the reason, `needs-info` with a question comment, or ready. See [capturing work](../usage/capturing-work.md#triaging-an-issue-someone-else-filed).

## --remove

Removes a captured backlog item and cleans up its dependencies in one operation, through `rota item rm`. File backend only; on the issue backend close the issue with `rota item complete <ID> --reason dropped` instead. Strips the item's entry from `BACKLOG.md`, removes `Related:` cross-references that point to it from other items, and deletes any matching detail file (`.rota/bugs/`, `.rota/features/`, `.rota/tasks/`) and plan file (`.rota/plans/`). An item active in `status.json` is refused on apply until its stream is dropped with `rota status rm <branch>`.

Dry-run by default: the first pass shows what would change, and an explicit `AskUserQuestion` confirmation must pass before any write. `ARCHIVE.md` is preserved by default as the historical record; pass `--scrub-archive` to also remove an archived entry and its cross-references there. Accepts one or more comma-separated IDs (e.g. `B07,F03`). If any ID is unknown, the verb exits 3 before any write. See [removing work](../usage/removing-work.md) for the full flow.
