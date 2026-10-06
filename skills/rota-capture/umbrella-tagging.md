# Step 4.6 — Tag Sub-Repo

Gate and registry semantics: `references/umbrella-mode.md`. Ask only when `rota repo umbrella` exits 0 and `.rota/repos.json` registers at least one sub-repo (the registry is the truth, not the config flag). Otherwise skip silently.

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`): an item lives on one sub-repo's tracker, so pick exactly one repo (single-select, no multi-repo option) and pass `--repos <name>`. `rota item create` refuses multi-repo items: for work spanning repos, capture one item per repo and link them with `Related:`.

Otherwise ask:

- **Header:** `"Repos"`; **Question:** *"Which sub-repo(s) does this item belong to?"* (with the item's short title)
- **multiSelect:** true
- **Options:** one per `name` in `.rota/repos.json` (mark the likely match `(Recommended)` when the item text names a repo), then *"None / unsure — leave untagged"* last.

Two or more repos make a multi-repo item that `/rota-work` branches in each repo. If *"None / unsure"* comes with concrete names, the names win. An ambiguous reply leaves the item untagged, and `/rota-work` will then refuse it and point back here.

Carry the picks as a comma-separated list of registered sub-repos into `--repos`. Omit the flag if untagged.
