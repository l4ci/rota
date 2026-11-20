# Decision item

Read from `SKILL.md` Step 1 when the item is labelled `decision:grilling`.

An item labelled `decision:grilling` (file backend: a `Decision: grilling` field; see `rota item show <ID>`) belongs to a decision milestone and takes the *decision path* instead of Steps 3-5: run one grilling pass (`references/grilling.md`) on the question the item asks, record the settled answer with `rota item comment add <ID> --kind decision --body-file -`, then close it with `rota item complete <ID> --no-proof --gist "<one line>"` (a decision has no tests, so no `rota proof add` row). The gist is what the milestone's `## Decisions` list gets. Stop there: draft no design, offer no next item, and resolve no second grilling item in this session, so each invocation settles exactly one decision. A `decision:research` item is a worker's finding, not a grilling: say *"`rota round assign` takes it"* and exit.
