# Umbrella mode

Used by `/rota-work` Step 4.5, `/rota-capture` Step 4.6, `/rota-spike` Step 2.5, `/rota-refactor` (per-sub-repo runs), and indirectly by every skill that branches on umbrella mode. The reference covers the canonical mechanics; skill-local carriers (per-step routing, `--repo` plumbing into specific verbs, dispatch shape) stay inline at each call site.

An umbrella project hosts shared `.rota/` coordinator state at its root, while git history and code live in registered sub-repos under it. The umbrella root has no `.git/` of its own; each sub-repo has its own.

## When umbrella mode is on

Umbrella mode is in effect when `.rota/repos.json` registers ≥1 sub-repo. The config flag `umbrella.enabled` in `.rota/config.json` is informational — data is the truth.

```bash
rota repo umbrella             # exit 0 = umbrella, exit 1 = not (data.umbrella under --json)
rota repo umbrella -C <dir>    # callers that already know the directory (e.g. captured before any `cd`)
```

## The registry — `.rota/repos.json`

Each entry has a `name` and a `path` (relative to the umbrella root). Read it through `rota repo resolve` (below) rather than re-parsing the JSON.

## The `Repos:` field on TODO items

Captured items carry the affected sub-repo(s) so `/rota-work` can route the wave correctly. Single-repo items use one name; multi-repo items use a comma-separated list:

```
- [ ] [F07] add auth endpoint  Created: 2026-05-11  Repos: api
- [ ] [F08] cross-cut error type  Created: 2026-05-11  Repos: web, api
```

Parse the field with the canonical field reader:

```bash
rota item field get <ID> --name repos
```

Under umbrella mode, items lacking `Repos:` cannot be routed by `/rota-work` — see *Walk-up convenience* below for the single-repo exception, and `/rota-capture` Step 4.6 for how items get tagged at capture time.

## Resolution verbs

Both resolvers exit 3 (`resolution`), not 0, on no-match.

- `rota repo which` — resolve cwd → sub-repo. **Exits 0** with the name on stdout (`data.name` and the absolute `data.path` under `--json`) when cwd is inside a registered sub-repo (including its Layout B worktree); **exit 3** otherwise, including when a stray `.rota/` inside a registered sub-repo masks the umbrella (the message names it).
- `rota repo resolve <name>…` — validate every name, one positional each. **Exits 0** with `data.repos: [{name, path}, …]` (absolute paths) under `--json` when all names resolve; **exit 3** naming every missing one if any fail.

Every `rota` verb finds the umbrella root itself by walking up to the nearest `.rota/`; there is no separate umbrella-root lookup.

(Compare to `rota status show <branch>`, which returns `active: false` with exit 0 on no-match — do not blur the distinction.)

## Walk-up convenience

When `/rota-work` is invoked from a cwd that resolves via `rota repo which`, the resolved sub-repo defaults as the wave's scope for items lacking explicit `Repos:`. This is the single-repo cwd convenience only — it does not generalize.

Multi-repo items always need the captured `Repos:` field. There is no cwd default for them, because cwd resolves to at most one sub-repo.

## Branch creation

Three patterns, all driven from the umbrella root (the orchestrator stays there so it can read/write `.rota/`); workers `cd` into the sub-repo path before any git operation.

**Single sub-repo (branch isolation):**

```bash
(cd <repo> && git checkout -b <branch>)
rota status add <branch> --items <ID>[,<ID>...] --repo <repo>
```

**Multiple sub-repos (branch isolation):**

```bash
rota git branch <branch> --repos <csv>
rota status add <branch> --items <ID>[,<ID>...] --repos <csv>
```

`rota git branch` is atomic: a precheck refuses (exit 4) for ALL repos if the branch exists in ANY one, before any branch is written. Its `--repos` takes no spaces after commas, so drop them from the `Repos:` value first.

**Single sub-repo with worktree (Layout B):**

```bash
(cd <repo> && git branch <branch>)
WT=$(rota git worktree-path <branch> --repo <repo>)
git -C <repo> worktree add "$WT" <branch>
rota status add <branch> --items <ID>[,<ID>...] --worktree "$WT" --repo <repo>
```

`rota git worktree-path` produces the canonical Layout B path `<umbrella>/.claude/worktrees/<repo>/<branch>` — use it for both `worktree add` and `rota status add`. `rota ship merge` and `rota ship pr` remove that worktree themselves before they integrate the branch.

For the broader picture of when to use branch vs worktree isolation, see `references/isolation-patterns.md`.

## Status registration

- `rota status add <branch> --items <ids-csv> [--worktree <path>] [--repo <name>] [--if-absent]` — uniqueness key becomes `(branch, repo)` when `--repo` is set. An unregistered `--repo` exits 3.
- `rota status add <branch> --items <ids-csv> --repos <repos-csv> [--worktrees <paths-csv>] [--if-absent]` — writes one entry per `(branch, repo)` pair. `--worktrees` is optional; if given, its length must equal `--repos` (else exit 2). `--repo` and `--repos` together exit 2.
- `rota status rm <branch> [--repo <name>]` — in umbrella mode, **pass `--repo`** or umbrella-tagged entries leak. Without `--repo`, only legacy entries (repo: null/missing) are removed; umbrella entries are preserved. It also deletes the branch's handoff note.

## Merge / PR with `--repo`

```bash
echo "<merge message>" | rota ship merge <branch> --body-file - --repo <repo>
echo "<body>"          | rota ship pr    <branch> --title "<title>" --body-file - --repo <repo>
```

Each operates within the sub-repo's `.git/`. At the umbrella root without `--repo`, both exit 2 — there's no `.git/` at the umbrella root to merge into.

## Issue mode in an umbrella

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`, *Umbrella*) puts each sub-repo's items on that sub-repo's own tracker, so `.rota/BACKLOG.md` and `Repos:` tagging by `/rota-capture` Step 4.6 change shape:

- **One repo per item.** Capture needs a target: `--repos <name>` or a cwd inside a sub-repo. A multi-repo item is refused; capture one item per repo and link them with `Related:` (qualified refs allowed). `Repos` cannot be changed on an existing item.
- **Qualified IDs.** `<repo>#<n>` and `<repo>:<ID>` always resolve; a bare `F42` / `#42` resolves only when exactly one sub-repo has it, else exit 2 listing the candidates. Created IDs come back qualified; `rota backlog list` shows them as `<repo>:<ID>`.
- **`--repo` plumbing.** `rota ship pr ... --items ... --repo <repo>` (falls back to the cwd's sub-repo), `rota ship pr-merge <pr> --repo <repo>` (required at the umbrella root), and `rota release milestone-check|notes --from issues|close-milestone ... --repo <repo>` (required at the umbrella root, exit 2 without).

## What this reference does NOT cover

- **Isolation patterns** (branch vs worktree, the decision table, the isolation guard) — see `references/isolation-patterns.md`.
- **Multi-repo parallelism safety** — `references/isolation-patterns.md` covers the rule (cross-repo parallel workers are safe by construction because each sub-repo has its own `.git/index`).
- **`rota-capture`'s `Repos:` tagging interaction** — how items acquire their `Repos:` field at capture time (cwd inference, AskUserQuestion shape) is per-skill carrier semantics; see `/rota-capture` Step 4.6 inline.
