# Spike start in umbrella mode

Used by Steps 2 to 4 when `rota repo umbrella` exits 0 (see `references/umbrella-mode.md`).

**Sub-repo (umbrella mode only; skip when `rota repo umbrella` exits 1, see `references/umbrella-mode.md`).** The spike branch must land in a specific sub-repo (the umbrella root often is not a git repo). Resolve `<repo>`:

1. The user named a sub-repo (*"spike SSE feasibility in web"*) → use it.
2. Else `rota repo which --json` from the cwd; on success use `data.name`.
3. Else ask via `AskUserQuestion`:
   - **Header:** `"Repo"`
   - **Question:** *"Which sub-repo should `spike/<name>` live in?"*
   - **Options:** one per registered sub-repo (names from `.rota/repos.json`, or `rota repo resolve --json`), single-select.

Carry `<repo>` into Step 4 as `--repo <repo>`.

## Step 3 and 4 differences

The branch is created in `<repo>`.

```bash
BRANCH=$(rota spike add --json --repo <repo> <name> --question "<question>" | jq -r .data.branch)
```

The spike file lives at the umbrella root regardless of `--repo`; only the branch lands in the sub-repo. Frontmatter records `repo: <name>` so `/rota-spike done` and listings know which sub-repo to use.

On a clean tree, `cd` into `<repo>` first, or run `git -C <repo-path> checkout "$BRANCH"`.

Handoff first line: `Spike opened: spike/<name> (in <repo>)`.
