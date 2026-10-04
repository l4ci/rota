# /rota-capture --from-github / --from-gitlab: pull upstream issues into the backlog

## What it does

Inventory-driven capture: lists open issues from the upstream GitHub or GitLab
repo(s), subtracts ones already imported into BACKLOG.md or ARCHIVE.md, and
presents the remainder in a multiSelect picker. For each selected issue it mints
an ID, writes a detail file with the upstream URL, and appends a BACKLOG.md
entry carrying a `GH: #N` or `GL: #N` cross-reference. An optional manual-gated
step applies an `in-progress` label upstream so collaborators see the issues are
claimed. Round-trip closing is handled by `/rota-ship`: it emits `Closes #N`
lines in PR bodies (auto-close on merge) and offers a manual-gated close prompt
on the direct-push path.

The provider is fixed by the dispatching flag: `--from-github` scans GitHub
remotes only, `--from-gitlab` scans GitLab remotes only. In umbrella mode the
flag filters which sub-repos are scanned (a `--from-github` call ignores
GitLab-hosted sub-repos).

## When to use

- Onboarding to a repo that already has triaged GitHub/GitLab issues.
- Bulk-importing a milestone's worth of upstream issues without re-typing them.
- Periodically syncing upstream backlog signals into rota.
- Mixed-host umbrellas where different sub-repos live on GitHub and GitLab.
  Call `/rota-capture --from-github` and `/rota-capture --from-gitlab` separately;
  each handles only matching sub-repos.

Don't use it to describe work you're inventing from scratch; that's plain
`/rota-capture`.

## Prerequisites

- **GitHub remotes:** `gh` installed and authenticated (`gh auth status`).
- **GitLab remotes:** `glab` installed and authenticated (`glab auth status`).

Missing CLI for a detected provider → that repo is skipped with a one-line note;
the rest proceed. Both CLIs are needed only in mixed-host umbrellas. `rota init`
soft-warns when a remote is detected but its CLI is missing.

## Config keys

| Key | Default | Purpose |
|-----|---------|---------|
| `issues.label` | `"in-progress"` | Label applied upstream when an issue is captured |
| `issues.autoCreateLabel` | `true` | Auto-create the label upstream if it doesn't exist |
| `issues.filterMineOnly` | `false` | Restrict the picker to issues assigned to me (`rota issues list --mine` → `--assignee @me`) |
| `issues.providers.github` | `true` | Enable `--from-github` |
| `issues.providers.gitlab` | `true` | Enable `--from-gitlab` |

Edit via `rota config set` or directly:

```bash
rota config set issues.label accepted
rota config set issues.filterMineOnly true
```

## Flow

1. **Project check**: a verb exits 3 when `.rota/` is missing (see `docs/reference/preflight.md`).
2. **Mode dispatch**: `/rota-capture` Step 1.5 routes `--from-github` /
   `--from-gitlab` to Import Mode (Steps I1+).
3. **Resolve target repo set (I1)**: single-repo or umbrella multiSelect.
   Repos whose provider doesn't match the dispatching flag are silently
   dropped. Loop mode auto-picks all matching repos.
4. **Discover candidates (I2)**: parallel `rota issues provider` +
   `rota issues list` + `rota issues imported` per repo.
5. **Subtract already-imported (I3)**: dedupes by
   `(provider, repo, issue_number)`. Prints `"N candidates, K already imported
   — showing M"` per repo.
6. **Pick issues (I4)**: multiSelect chunked to ≤4 at a time; options show
   title, labels, author, and URL. Loop mode auto-picks all remaining
   candidates.
7. **Classify and capture (I5)**: remote labels drive section (`bug` → Bugs,
   `enhancement` → Features, else Tasks) and default priority/size. Mint ID,
   write `.rota/<kind>/<ID>.md` with upstream URL footer, append BACKLOG.md entry
   with `GH: #N` / `GL: #N` cross-reference.
8. **Manual gate: apply the `in-progress` label upstream (I6)** (see below).
9. **Compact report (I7)**: lists captured IDs, issue numbers, and label
   status.

## Manual gate

Step I6 is a manual gate (see `references/manual-gates.md`). The user sees
which issues will be labeled and confirms before any label is written
upstream. Loop mode never auto-picks this step: the label change is
externally visible, and collaborators see the issues marked as claimed.
Silence is not consent: the default when a plain-text fallback is used is
**skip** (opt-in-off).

## Round-trip closing

Captured items carry `GH: #N` / `GL: #N` in their BACKLOG.md body. On ship:

- **PR path:** `rota ship body` emits `Closes #N` lines into the PR/MR
  description; the host auto-closes the issues on merge. No extra action needed.
- **Direct-push path:** `/rota-ship` Step 6c presents a manual-gated prompt
  listing candidate issues and routes through `rota issues close`, which posts a
  tracking comment naming the merge commit and then closes the issue.

## Cleanup on remove

`/rota-capture --remove` Step R3 presents a manual-gated prompt to remove the
upstream label when a captured item is removed without ever shipping, so the
upstream issue isn't left permanently marked as claimed.

## Example

```
# 1. Pull issues from the upstream repo
/rota-capture --from-github

# Skill discovers open issues, shows picker:
# Which issues to capture?
# ▶ #42: Timer resets on tab refocus  [bug · by @alice · github.com/…]
# ▶ #47: Add keyboard shortcut toggle [enhancement · by @bob · github.com/…]
# ▶ #51: Update README examples       [documentation · by @carol · github.com/…]

# User picks #42 and #47. Skill mints IDs and appends BACKLOG.md:
# - **[B03] [P1] Timer resets on tab refocus.** … GH: #42
# - **[F05] [Minor] Add keyboard shortcut toggle.** … GH: #47

# Step I6 — Apply `in-progress` to these 2 issues upstream?
# ▶ Yes — apply `in-progress` to all (Recommended)
#   No — skip labeling
# User picks Yes → gh labels #42 and #47 in-progress.

# Compact report:
# Captured 2 issues:
# - [B03] Timer resets on tab refocus (GH #42 → in-progress)
# - [F05] Add keyboard shortcut toggle (GH #47 → in-progress)

# 2. Work and ship — PR body auto-emits Closes #42 / Closes #47
/rota-work B03
/rota-ship
# → PR merged → GitHub auto-closes #42 and #47
```

## Related

- `/rota-capture` (no flag): brain-dump counterpart; use when the item
  originates locally.
- `/rota-ship`: emits `Closes #N` in PR bodies and handles direct-push closing.
- `/rota-capture --remove`: optionally removes the upstream label when an
  imported item is deleted without shipping (Step R3 manual gate).
- `rota issues *`: the verbs underlying this flow (`rota issues provider`,
  `rota issues list`, `rota issues imported`, `rota issues label`, `rota issues close`).
