# Step 4.6 — Tag Sub-Repo

Gate and registry semantics: `references/umbrella-mode.md`. Ask only when `rota repo umbrella` exits 0 and `.rota/repos.json` registers at least one sub-repo. Otherwise skip silently.

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`): pick exactly one repo (single-select) and pass `--repos <name>`. `rota item create` refuses multi-repo items: for work spanning repos, capture one item per repo and link with `Related:`.

Otherwise ask:

- **Header:** `"Repos"`; **Question:** *"Which sub-repo(s) does this item belong to?"* (with the item's short title)
- **multiSelect:** true
- **Options:** one per `name` in `.rota/repos.json` (mark the likely match `(Recommended)` when the item text names a repo), then *"None / unsure — leave untagged"* last.

Two or more repos make a multi-repo item that `/rota-work` branches in each repo. Concrete names beat *"None / unsure"*. An ambiguous reply leaves the item untagged; `/rota-work` then refuses it and points back here.

Carry the picks as a comma-separated list of registered sub-repos into `--repos`. Omit the flag if untagged.
