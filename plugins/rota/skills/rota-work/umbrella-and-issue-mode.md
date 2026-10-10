# Umbrella and issue-mode branches

Loaded by `SKILL.md` when the repo is an umbrella (`rota repo umbrella` exits 0, items carry `Repos:`) or `backlog.backend` is `"issues"`. Registry shape and `Repos:` semantics: [`references/umbrella-mode.md`](references/umbrella-mode.md). Issue-mode verbs: [`references/issue-mode.md`](references/issue-mode.md). Step 4.5 is cited by `umbrella-mode.md`: keep the heading.

## Step 3 — Name the Branch (umbrella)

Items carry `Repos:`, comma-separated (`rota item field get <ID> --name repos`). All items in a wave must share the same repo set. Validate with `rota repo resolve <name>…`; exit 3 names the missing ones: surface and stop.

## Step 4.5 — Umbrella Pre-Flight

Skip when `rota repo umbrella` exits 1 (single-repo). When it exits 0:

1. **Every item must carry `Repos:`.** Otherwise stop: *"Error: `[<ID>]` lacks a `Repos:` tag. Re-run `/rota-capture` to add it. Cannot route to a sub-repo."*
2. **All items in a wave resolve to the same repo set** (order-independent). Otherwise stop: *"Error: items in this wave target different sub-repo sets: `<set-a>` vs `<set-b>`. Split into separate `/rota-work` runs."*
3. **Validate every name** with `rota repo resolve <name>…` (CSV spaces dropped). On failure stop: *"Error: `Repos: <name>` not registered in `.rota/repos.json`. Run `rota init umbrella` from the umbrella root to register sub-repos."*
4. **Walk-up (single-repo only).** If invoked from a cwd that `rota repo which` resolves to a registered sub-repo, default to it when items lack `Repos:`. Multi-repo items always come from the captured field.

**Issue mode** (*Umbrella* in `issue-mode.md`): items are single-repo with qualified IDs (`<repo>:<ID>`); take the repo from `rota item field get <ID> --name repos` (a bare ID in several sub-repos exits 2 as ambiguous) and pass `--repo <repo>` to `rota ship pr` in Step 10.

Carry the resolved set into Step 5 and Step 10.

## Step 5 — Create Branch or Worktree

**Umbrella registration.** One repo: add `--repo <repo-name>` to `rota status add`. Several repos register one entry per `(branch, repo)`: `rota status add <branch> --items <ids> --repos <repos-csv> [--worktrees <csv>]`. Umbrella branches (`rota git branch <name> --repos <csv>`, Layout B worktree): `references/isolation-patterns.md`.

**Issue mode.** Once the branch exists, per item run `rota item ready <ID>` (exit 1 prints what is missing: warn the user), then claim it with `rota item claim <ID> --as <branch>`. Exit 4 means another worker holds it: drop that item and continue with the rest, or stop when none remain. Exit 3 or 5: stop and report. Load each item's context as `issue-mode.md` "Resuming an item" describes (start with `rota item show <ID>`) before planning tasks. For a `changes-requested` item, follow *Handling review feedback* in `references/worker-contract.md` when working its `feedback` comments.

## Step 6 — Dispatch (umbrella)

The `[UMBRELLA]` line replaces the WORKTREE line under branch isolation; both appear under Layout B worktrees. Subagents MUST `cd` to the named directory before any `git` command (their default cwd targets the wrong `.git/`). For a multi-repo set, dispatch one subagent per sub-repo; each brief lists only its own files, and Step 7 verifies each repo's commit independently.

## Step 7 — Proof (issue mode)

The proof rows go in the item's proof note.

## Step 7.5 — Commit (umbrella)

Commit inside each target sub-repo (`git -C <umbrella>/<repo>`).

## Step 9 — Close the Items (issue backend)

Skip. Don't call `rota item complete`: the issue closes when its PR merges (`Closes #<n>`, Step 10).

## Step 10 — Merge or PR (issue backend)

The issue backend forces the PR path and never merges:

```bash
printf '%s' "$BODY" | rota ship pr <branch> --title "<short title>" --body-file - --items <ID1>,<ID2>
rota item state <ID> --to needs-review    # once per item
```

Don't call `rota item release`: the claim persists until the PR merges, which is `/rota-review --queue`'s job.

## Step 11 — Status (umbrella)

Umbrella waves MUST pass `--repo` to `rota status rm <branch>`, or the entry leaks into the next no-argument `/rota-work`.
