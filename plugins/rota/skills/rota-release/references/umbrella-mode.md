# Umbrella mode

Used by `/rota-work` Step 4.5, `/rota-capture` Step 4.6, `/rota-spike` Step 2, `/rota-refactor` (per-sub-repo runs), and indirectly by every skill that branches on umbrella mode. This reference covers the canonical mechanics; skill-local carriers (per-step routing, `--repo` plumbing into specific verbs, dispatch shape) stay inline at each call site.

An umbrella project hosts shared `.rota/` coordinator state at its root, while git history and code live in registered sub-repos under it. The umbrella root has no `.git/` of its own; each sub-repo has its own.

## Contents

- When umbrella mode is on
- The registry — `.rota/repos.json`
- The `Repos:` field on backlog items
- Resolution verbs
- Walk-up convenience
- Status registration
- Merge / PR with `--repo`

## When umbrella mode is on

Umbrella mode is in effect when `.rota/repos.json` registers ≥1 sub-repo. The config flag `umbrella.enabled` in `.rota/config.json` is informational; the data is the truth.

```bash
rota repo umbrella             # exit 0 = umbrella, exit 1 = not (data.umbrella under --json)
rota repo umbrella -C <dir>    # callers that already know the directory (e.g. captured before any `cd`)
```

## The registry — `.rota/repos.json`

Each entry has a `name` and a `path` (relative to the umbrella root). Read it through `rota repo resolve` (below) rather than re-parsing the JSON.

## The `Repos:` field on backlog items

Captured items carry their affected sub-repo(s) so `/rota-work` can route the wave. Single-repo items use one name; multi-repo items a comma-separated list:

```
- [ ] [F07] add auth endpoint  Created: 2026-05-11  Repos: api
- [ ] [F08] cross-cut error type  Created: 2026-05-11  Repos: web, api
```

Parse the field with the canonical field reader:

```bash
rota item field get <ID> --name repos
```

Under umbrella mode, `/rota-work` cannot route items lacking `Repos:`. See *Walk-up convenience* below for the single-repo exception, and `/rota-capture` Step 4.6 for tagging at capture time.

## Resolution verbs

Both resolvers exit 3 (`resolution`), not 0, on no-match.

- `rota repo which`: resolve cwd → sub-repo. **Exits 0** with the name on stdout (`data.name` and the absolute `data.path` under `--json`) when cwd is inside a registered sub-repo (including its Layout B worktree); **exit 3** otherwise, including when a stray `.rota/` inside a registered sub-repo masks the umbrella (the message names it).
- `rota repo resolve <name>…`: validate every name, one positional each. **Exits 0** with `data.repos: [{name, path}, …]` (absolute paths) under `--json` when all names resolve; **exit 3** naming every missing one if any fail.

Every `rota` verb finds the umbrella root itself by walking up to the nearest `.rota/`.

(Unlike these, `rota status show <branch>` returns `active: false` with exit 0 on no-match. Don't blur the two.)

## Walk-up convenience

When `/rota-work` is invoked from a cwd that resolves via `rota repo which`, the resolved sub-repo defaults as the wave's scope for items lacking explicit `Repos:`. This is a single-repo cwd convenience only; it does not generalize.

Multi-repo items always need the captured `Repos:` field. They get no cwd default, because cwd resolves to at most one sub-repo.

## Status registration

- `rota status add <branch> --items <ids-csv> [--worktree <path>] [--repo <name>] [--if-absent]` ; uniqueness key becomes `(branch, repo)` when `--repo` is set. An unregistered `--repo` exits 3.
- `rota status add <branch> --items <ids-csv> --repos <repos-csv> [--worktrees <paths-csv>] [--if-absent]` ; writes one entry per `(branch, repo)` pair. `--worktrees` is optional; if given, its length must equal `--repos` (else exit 2). `--repo` with `--repos` exits 2.
- `rota status rm <branch> [--repo <name>]`: in umbrella mode, **pass `--repo`** or umbrella-tagged entries leak. Without `--repo`, only legacy entries (repo: null/missing) are removed; umbrella entries are preserved. It also deletes the branch's handoff note.

## Merge / PR with `--repo`

```bash
echo "<merge message>" | rota ship merge <branch> --body-file - --repo <repo>
echo "<body>"          | rota ship pr    <branch> --title "<title>" --body-file - --repo <repo>
```

Each operates within the sub-repo's `.git/`. At the umbrella root without `--repo`, both exit 2: the umbrella root has no `.git/` to merge into.
