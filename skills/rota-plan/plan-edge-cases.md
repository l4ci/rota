# rota-plan — edge cases

Read from `SKILL.md` Steps 3 and 4 when the case applies.

## Task-shaping rules (Step 3)

- A rename and its incoming-link sweep are one task; derive the file list from `git grep -l "<old-name>"`.
- A wide rename-style refactor (too many call sites for one window) is planned as expand → migrate in batches → contract, per `references/dependent-items.md`; each step becomes its own task.
- Doc deliverables under `docs/` (or `docs.path`) must land in an existing doc home; otherwise raise it as an Open question (umbrella: a sibling `<repo>-docs` is the usual home).

## Splitting into items and docs check (Step 4)

When the plan splits into separate items (a sliced milestone, or the expand/migrate/contract steps), file each with `rota item create ... --depends-on <prerequisite IDs>` where it clearly needs another open one first, prerequisites first (`references/dependent-items.md`). Tasks inside one plan need no items.

**Confirm before filing three or more.** When the split yields 3+ items, do not file yet. Show a numbered breakdown, one line per item: `N. <title> — depends on: <numbers or none> — delivers: <what works when it merges>`. Ask one confirm covering granularity and edges (merge, split or drop items, change an edge). File with `rota item create` only after the answer, prerequisites first, applying the edits. One or two items: file directly and report edges as usual.

**Prefactor first.** When a preparatory change makes the main change easy (extract a helper, rename, reshape an interface; behavior unchanged), order it as the first item and make the dependants depend on it. Skip it when the main change is already easy.

Then `rota plan validate-docs <key>` (advisory, exits 0): for each `data.mismatches` entry append an Open question naming the path, target repo and suggestion.
