# Umbrella mode

Umbrella mode lets one rota setup span several independent git repositories that sit side by side under one parent folder. Knowledge, decisions, vision, and the backlog live once at the umbrella; each sub-repo keeps its own history, branches, and remotes.

If you're in single-repo mode, skip this page. Single-repo behavior is unchanged.

## When to use it

Turn on umbrella mode when you maintain a handful of related repositories (say `~/projects/myorg/` with `web/`, `api/`, and `shared/`) and want one shared `KNOWLEDGE.md`, `DECISIONS.md`, `MILESTONES.md`, and backlog instead of forking them into N copies. The typical signal: "I keep cross-pasting the same gotcha into three repos' notes."

## When NOT to use it

- **Single-repo project.** Don't enable. The single-repo path is simpler and faster.
- **Monorepo.** Don't enable. A monorepo is one git repo; umbrella mode is for *multiple* repos under a parent.
- **Casually-grouped unrelated repos.** If the repos under your folder don't share knowledge, decisions, or planning concerns, umbrella mode just adds bookkeeping.

## How it differs from single-repo

| Aspect | Single-repo | Umbrella |
|--------|-------------|----------|
| `.rota/` location | Repo root | Umbrella root (one level up from sub-repos) |
| Where `rota` runs git ops | The repo | The sub-repo for the current item (resolved via `Repos:` tag or `--repo` flag) |
| Worktree path | `<repo>/.claude/worktrees/<branch>` | `<umbrella>/.claude/worktrees/<repo>/<branch>` (Layout B) |
| `BACKLOG.md`, `ARCHIVE.md`, `DECISIONS.md`, `MILESTONES.md` | Per-repo | Shared at the umbrella. With `backlog.backend: issues` there is no `BACKLOG.md`: each sub-repo's items live on that sub-repo's own tracker ([issue backend](issue-backend.md)) |
| `KNOWLEDGE.md` (+ Glossary, tier sidecar) | Per-repo | Hybrid: umbrella `.rota/KNOWLEDGE.md` for cross-repo learnings/terms **plus** per-sub-repo `.rota/knowledge/<name>/KNOWLEDGE.md` for repo-local ones |
| `status.json` entries | Keyed by `branch` | Keyed by `(branch, repo)` |
| `.rota/handoff/<branch>.md` | One per branch | `.rota/handoff/<branch>@<repo>.md` (one per branch+repo) |
| Sub-repo git histories | n/a | Independent. No submodules, no version pinning |

Single-repo behavior is unchanged. Umbrella mode is in effect when `.rota/repos.json` registers at least one sub-repo; `umbrella.enabled` in `.rota/config.json` is informational. Without registered sub-repos, every skill behaves exactly as before.

## Enabling it

1. `cd` to the umbrella folder, the parent that contains your sub-repos as immediate children.
2. Run `rota init umbrella --list` to see the immediate git children it would register.
3. Run `rota init umbrella --repos web,api,shared` (or `--all` for every child). It seeds `.rota/` like `rota init`, writes `.rota/repos.json` with the repos you chose and sets `umbrella.enabled: true` in `.rota/config.json`.
4. If the umbrella is itself a git repo, `.gitignore` gains a `# ── rota umbrella ──` block listing `.claude/`, `.rota/`, and each registered sub-repo.

The result looks like:

```
myorg/                 # umbrella root
├── .rota/               # shared coordinator state
│   ├── repos.json
│   ├── KNOWLEDGE.md
│   ├── DECISIONS.md
│   └── …
├── .claude/           # worktrees land here (Layout B)
├── web/               # registered sub-repo (independent git)
├── api/               # registered sub-repo (independent git)
└── shared/            # registered sub-repo (independent git)
```

To opt back out, run `rota config set umbrella.enabled false` (see [configuration](configuration.md)). The registry file stays intact: entries in `.rota/repos.json` remain on disk, and `rota` stops consulting them until you toggle umbrella mode back on.

## The registry: `.rota/repos.json`

The registry is one JSON file at the umbrella's `.rota/repos.json`:

```json
{
  "repos": [
    { "name": "api", "path": "./api" },
    { "name": "web", "path": "./web" }
  ]
}
```

- `name` is the sub-repo's basename and the value `/rota-capture` accepts in the `Repos:` field on items.
- `path` is relative to the umbrella root. `rota` canonicalizes each entry via `realpath` at lookup time, so symlinked sub-repo paths resolve correctly.
- Entries are sorted alphabetically for stable diffs.
- No SHAs, no version pins. Sub-repos are independent git repositories. See `.rota/DECISIONS.md` (Architecture, "Umbrella mode does not use git submodules") for the rationale.

To edit the registry today, re-run `rota init umbrella` from the umbrella. `rota init umbrella` is idempotent: a second run with the same selection is a no-op; a run with new names adds them; names you omit but were previously registered are kept (with a warning).

### KNOWLEDGE.md and Glossary in umbrella mode

KNOWLEDGE.md is **hybrid** in umbrella projects:

- `.rota/KNOWLEDGE.md`: umbrella file. Cross-repo learnings and umbrella Glossary terms.
- `.rota/knowledge/<name>/KNOWLEDGE.md`: per-sub-repo file. Repo-local learnings and per-sub-repo Glossary terms. Created on first write (and pre-seeded by `rota init` umbrella setup).

The Glossary topic follows the same hybrid scoping. Scope resolves in this order: an explicit `--repo umbrella|<name>` flag wins; otherwise the cwd auto-resolves (inside a registered sub-repo → that repo; at the umbrella root → umbrella). Single-repo projects always resolve to `umbrella` and behave byte-identically to before. The knowledge verbs (`rota knowledge add`, `query`, `tier`, `amend`) and the glossary verbs (`rota glossary write`, `read`, `import`) all take `--repo`; readers (`rota knowledge query`, `rota glossary read`) merge umbrella + sub-repo content with a `> from: <path>` provenance line per source when scope is a sub-repo. Tier sidecars split per file (`.rota/knowledge-tier.json` umbrella, `.rota/knowledge/<name>/knowledge-tier.json` per sub-repo).

**DECISIONS.md stays umbrella-only.** Hard boundaries are inherently cross-repo. A repo-local "decision" is really a learning; capture it with `/rota-learn`. Full model and rationale: [`references/persistence-skills.md`](../../skills/references/persistence-skills.md#umbrella-scoping), governed by the `.rota/DECISIONS.md` *"Persistence-trio scoping under umbrella mode"* boundary.

## Resolvers: `rota repo umbrella` and `rota repo which`

Two verbs answer the questions every umbrella-aware skill asks: *"is this an umbrella?"* and *"which sub-repo am I in?"*

`rota repo umbrella` reports whether the project is an umbrella: it is when `.rota/repos.json` holds at least one registered sub-repo. `--json` returns `{"umbrella": true|false}`.

| Exit | Meaning |
|------|---------|
| `0` | Umbrella. |
| `1` | Not an umbrella: no `.rota/`, no `.rota/repos.json`, or an empty registry. |

`rota repo which` answers the second question. From any cwd inside a registered sub-repo (including a Layout B worktree), it prints the registered name, and `--json` adds the sub-repo's absolute path. It uses `git rev-parse --git-common-dir` to find the sub-repo root from inside a worktree, then matches against canonicalized entries in `.rota/repos.json`. To go from a name to a path, use `rota repo resolve <name>`.

| Exit | Meaning |
|------|---------|
| `0` | Inside a registered sub-repo. |
| `3` | Not inside a registered sub-repo, not in a git repo, no umbrella, or a stray `.rota/` inside a registered sub-repo masks the umbrella (the message says which). |

Both are read-only.

## Worktree layout

Umbrella worktrees use **Layout B**:

```
<umbrella>/.claude/worktrees/<repo>/<branch>
```

One discovery point at the umbrella, `<umbrella>/.claude/worktrees/`, holds every active worktree across every sub-repo, grouped by repo. No `.gitignore` edits in the sub-repos. Single-repo mode keeps `<repo>/.claude/worktrees/<branch>` and is unaffected. To resolve the canonical Layout B path for a `(repo, branch)` pair without hand-encoding it, call `rota git worktree-path --repo <name> <branch>`.

## Per-skill behavior

Most skills delegate umbrella resolution to the underlying verbs and stay umbrella-flat at the prose level. The user-visible surface:

- **`/rota-capture`** asks for `Repos:` when umbrella mode is on, accepting one or more registered names. Items can also be untagged (umbrella-flat, appropriate for cross-cutting tasks).
- **`/rota-work`** reads `Repos:` from the item and runs the orchestrator plus workers against the resolved sub-repo's `.git/`. The atomic commits land in that sub-repo's history; `status.json` records the entry as `(branch, repo)`.
- **`/rota-pause`** writes its handoff to `.rota/handoff/<branch>@<repo>.md` (instead of `<branch>.md`) so two sub-repos sharing a branch name don't clobber each other's notes. The body gains a `Repo: <name>` line. `/rota-work` (no argument) reads the umbrella-keyed path first and falls back to the legacy `<branch>.md` form for older streams.
- **`/rota-plan`** records the target sub-repo in plan frontmatter (`repo: <name>`) when invoked with `--repo` or when the item carries `Repos:`. Slice and milestone plans stay umbrella-flat.
- **`/rota-spike`** runs the spike branch in the resolved sub-repo (`spike/<name>` lives in that repo's `.git/`); the spike file stays at `<umbrella>/.rota/spikes/<name>.md` with a `repo: <name>` frontmatter line.
- **`/rota-work --preview`** displays the resolved sub-repo for items with `Repos:` in its peek output.
- **`/rota-debug`** routes its single fix-commit to the sub-repo resolved from the bug's `Repos:` tag.
- **`/rota-review`** scopes its branch inspection to the sub-repo via `rota review scope --repo <name>`. `BACKLOG.md` and `ARCHIVE.md` lookups stay at the umbrella.
- **`/rota-ship`** threads `--repo` through `rota ship merge` / `rota ship pr` so the merge or PR runs in the correct sub-repo.
- **`/rota-refactor`** runs once per sub-repo with `--repo <name>` (`rota refactor targets --json` lists them), so each finding is filed on the tracker that owns the code.
- **`/rota-learn`** routes the learning (and `--term` Glossary entries) to the scope resolved from cwd or `--repo`: repo-local learnings land in `.rota/knowledge/<name>/KNOWLEDGE.md`, cross-repo ones in the umbrella file. At the umbrella root it asks once whether a learning is umbrella-shared or sub-repo-scoped. The per-sub-repo CLAUDE.md knowledge block lists umbrella ∪ that sub-repo's topics. DECISIONS via `/rota-decide` stays umbrella-only.

The `--repo <name>` flag is also exposed on the underlying verbs when you call them directly: `rota status add`, `rota status rm`, `rota review scope`, `rota ship merge`, `rota ship pr`, `rota plan add`, `rota spike add`, `rota git worktree-path`, plus the knowledge/glossary surface (`rota knowledge add`, `rota knowledge query`, `rota knowledge tier`, `rota knowledge amend`, `rota glossary write`, `rota glossary read`, `rota glossary import`) where scope auto-resolves from cwd when the flag is omitted. Without the flag, verbs operate on the cwd's git tree / umbrella scope as in single-repo mode.

## What's not yet in umbrella mode

- **Multi-repo items on the issue backend.** An item lives on one sub-repo's tracker, so `rota item create` refuses several repos: capture one item per repo and link them with `Related:`. On the file backend `Repos:` takes a comma-separated list and `/rota-work` branches in each repo.
- **Registry editor.** Add/remove repos without re-running `rota init umbrella`. Planned.

## Footguns

- **Don't create `.rota/` inside a registered sub-repo.** It masks the umbrella. `rota repo which` detects this and exits 3 with a message naming the stray `.rota/`.
- **Never add a sub-repo as a git submodule of the umbrella.** Sub-repos must remain independent. See `.rota/DECISIONS.md` (Architecture).
- **Symlinked sub-repo paths work,** because `rota` resolves each registry entry with `realpath` at lookup time.

## See also

- `.rota/DECISIONS.md` (Architecture, "Umbrella mode does not use git submodules")
- [The `.rota/` folder](../reference/rota-folder.md): what `rota init` writes
- [Vision and plans](vision-and-plans.md): milestones and plans
- [Parallel rounds](parallel-rounds.md): the `rota round` verbs are not repo-scoped (no `--repo`)
