# Dependent items

Used by `/rota-capture`, `/rota-plan` and `/rota-refactor` when they create more than one item. A round holds an item until everything in its `## Depends on` section is closed (`rota round` readiness, check `dependencies`), so an ordering written here is one the orchestrator need not rebuild by hand.

## When to declare

Declare an edge only when the later item **cannot start or cannot merge** before the earlier one closes: it consumes an interface, file or behaviour the earlier one produces, or both rewrite the same lines and the second must build on the first. Shared theme, shared subsystem or "nicer in this order" is `--related`, not a dependency. A false edge holds an item back for nothing; an edge to an item that never closes holds it forever.

Point at open items only. A closed dependency is satisfied already: leave it out.

## How

Pass `--depends-on` to `rota item create`; it writes the `## Depends on` section (`#N`, or an ID like `B07` on the file backend) so the skill never hand-writes it:

```bash
A=$(rota item create --json --kind tasks --title "Add the new helper" | jq -r .data.id)
rota item create --json --kind tasks --title "Move callers to the new helper" --depends-on "$A"
```

IDs are minted on creation, so create items in dependency order within a batch: prerequisites first, then capture the dependants with the IDs just printed. Several prerequisites: `--depends-on "#12,#13"`. `--depends-on` and a `## Depends on` section in the `--body-file` are mutually exclusive.

Report each edge in the closing summary (`#14 depends on #13`) so the user can veto one. `/rota-plan` confirms the breakdown before filing three or more items, so for it the summary is a record, not the veto point.

## Wide rename-style refactors

A change that touches many call sites (rename, signature change, moving a type) is not one item. File it as a sequence, each step its own item depending on the one before:

1. **Expand** — add the new name or shape beside the old; both work. Ships alone.
2. **Migrate** — move callers over, in batches that each fit one worker's window and touch disjoint paths. Each batch depends on *expand*; batches do not depend on each other, so a round can run them in parallel.
3. **Contract** — remove the old name or shape. Depends on every migrate batch.

Derive the batches from `git grep -l "<old-name>"`, grouped by directory. Skip the sequence when one worker can do the whole change in one PR.
