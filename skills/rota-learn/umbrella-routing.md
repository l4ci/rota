# Umbrella-mode routing for `/rota-learn`

Loaded by `skills/rota-learn/SKILL.md` Step 5 when `.rota/repos.json` registers at least one sub-repo. `rota knowledge add` honors `--repo umbrella|<name>` to pick the `KNOWLEDGE.md` written:

- **`--repo <name>`** — writes to `.rota/knowledge/<name>/KNOWLEDGE.md` (the sub-repo's scoped file).
- **`--repo umbrella`** — writes to `.rota/KNOWLEDGE.md` (the shared umbrella file).
- **No `--repo`** — scope auto-resolves from cwd: inside a registered sub-repo's directory the verb writes that sub-repo's scoped file; at the umbrella root it falls back to `.rota/KNOWLEDGE.md`.

**At the umbrella root**, when a learning is clearly repo-local rather than cross-repo, ask once via `AskUserQuestion` before calling `rota knowledge add`:

- Header: `"Learning scope"`
- Question: *"Capture this learning as umbrella-shared, or scoped to a specific sub-repo?"*
- Options (single-select, one per registered sub-repo plus a shared option):
  1. `"Umbrella-shared (Recommended)"` — *"Write to `.rota/KNOWLEDGE.md`; visible across all sub-repos."*
  2. `"<name>"` (one option per registered sub-repo) — *"Write to `.rota/knowledge/<name>/KNOWLEDGE.md`; scoped to that repo."*

Pass the chosen scope as `--repo <scope>` to `rota knowledge add`. `/rota-learn --term` (Glossary entries) uses the same routing — a `--repo`-scoped term lands in that sub-repo's `## Glossary`.

**New topics in a scoped file:** the "append the heading first" rule applies to the *resolved* file; a fresh sub-repo `KNOWLEDGE.md` is empty, so seed the heading there first.

**DECISIONS stay umbrella-only** (*"Persistence-trio scoping under umbrella mode"* in `.rota/DECISIONS.md`): never offer or pass `--repo` when writing decisions.
