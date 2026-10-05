# #77 rename: port-phase names to domain names

One-shot tooling for issue #77. Not a standing doc: delete this directory in the PR that applies it
(or after).

    bash docs/contributing/rename-77/apply.sh <checkout-dir>

`apply.sh` takes a clean checkout, applies `map.tsv` (files, identifiers, test-name prefixes) and
`text-edits.tsv` (phase labels in comments), runs `gofmt`, then `go build` and `go vet`. It
leaves the result uncommitted. A missing or doubled anchor, a source file that is gone, a new
name already in use in `internal/cli` or `cmd/rota`, or a leftover `a4`/`a6` name stops it.

## What is not a pure rename

Three small edits so `Tree()` registers each group by its own builder (the ticket asks for it).
Command order and verb behaviour are unchanged.

- `a6Commands()` splits into `debugCommands()` and `spikeCommands()`.
- `a4Commands()` becomes `withReadOnly(groups...)`, which keeps the read-only wrapping.
- `Tree()` lists `docsCommands`, `proofCommands`, `milestoneCommands`, `debugCommands`,
  `spikeCommands`, then `withReadOnly(item, backlog, config, issues)`.

## Calls the ticket did not settle (unratified)

- `a6.go` becomes `debug.go` and still holds the spike verbs. A split is a code move, not a rename.
- `a6_docs.go` becomes `design_plan.go` (design and plan share one file today).
- `a6_issue.go` becomes `artifact_issue.go` (issue-mode halves of design, plan and proof).
- `a4c.go` becomes `config.go` and still holds `update` and `repo`.
- `a4In` becomes `hasString`, not `inList`: `limit.go` already has an identical `inList`. Merging
  the two is a logic edit.
- `a4Null` and `nullStr` are identical; both stay.

## Left alone

- `docs/design/**`, `.rota/**` and `internal/backlog/write.go` still say A4/A6: they describe the
  migration, and `.rota/` is skill-owned.
- Frozen records are moved, contents untouched (the `a4` strings inside are hash fragments).
