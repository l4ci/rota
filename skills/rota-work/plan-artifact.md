# Plan-as-artifact check (Step 4)

Loaded by `SKILL.md` Step 4 for every item or slice, including resume, before decomposition.

Resolve the lookup key using the configured backend:

| Target | Lookup key |
|---|---|
| Issue item, with or without a milestone | The issue ref, e.g. `rota plan show '#42'` (quote `#42`), or its lettered ID such as `B42`. Normalize a bare issue number to `#42`. An explicit `M01-B42` also reaches the item's plan note. |
| File item | The explicit item-plan key, or its milestone plus ID (`Milestone: M01` on `B07` → `M01-B07`). An untagged file item without an explicit key has no supported item-plan key; do not invent a milestone or call `plan show B07`. |
| Slice, either backend | The full slice key, e.g. `M01-S01`. |

Run `rota plan show <key>`. Exit 3 means no plan; other failures must be surfaced, not treated as permission to re-decompose. Issue item plans live on their issues, so `rota plan list` cannot substitute for this lookup.

If a plan exists, use its decomposition, files, interfaces, constraints, verify steps and assumptions as the dispatch briefs; restate user redlines. Keep its stored `key` and task numbers for the task ledger even when the lookup used an alias. If the conversation contradicts the plan, ask whether to update it first (`/rota-plan`) or proceed and ignore it.

On resume, reload that same plan before reading the task ledger. Preserve each `<key>/<N>` identity, drop committed tasks, and dispatch only the remainder; a stale handoff cannot revive a committed task. If the saved plan or its task identities no longer match the ledger, surface the mismatch and ask once rather than silently rebuilding the decomposition.
