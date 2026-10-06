---
name: rota-spike
description: Use when X must be tried before committing to it ("can we use SSE?", "does this library handle our scale?") and the answer is a finding, not shipped code.
---

# rota-spike — Throwaway Feasibility Experiment

**Code on the spike branch is reference, not product.**

Two modes:

- **Start mode** — open a new spike with a question
- **Finish mode** — extract findings from work done on a spike branch into the spike file

## Step 1 — Mode

Determine the mode silently:

- *"spike SSE for live updates"*, *"try X"*, *"feasibility check on Y"* → **Start mode**
- *"spike done"*, *"finish the SSE spike"*, *"extract findings"* → **Finish mode**
- Neither set of triggers matches, or both match → ask once

In Finish mode, list existing open spikes via `rota spike list` (`data.spikes`, `status` not `done`) and ask which one if not specified.

## Step 2 (Start mode) — Sharpen the Question and Resolve the Repo

A spike answers a *yes/no/conditional* question. Push back if the question is vague:

- ❌ *"Try Server-Sent Events"* — too open
- ✅ *"Can we use SSE for live updates over our existing nginx setup without proxy buffering issues?"*

Name the spike with a short kebab-case identifier (`sse-feasibility`, `auth-rotation`, `migration-cost`). The name becomes the branch suffix and the spike file's stem.

**Resolve the sub-repo (umbrella mode only).** Skip this part entirely when umbrella mode is off (`rota repo umbrella` exits 1). See `references/umbrella-mode.md` for what umbrella mode means and how the registry works.

In umbrella mode, the spike branch must land in a specific sub-repo (the umbrella root often is not a git repo at all). Resolve `<repo>` via the 3-step fallback:

1. If the user named a sub-repo in their input (e.g. *"spike SSE feasibility in web"*) — use it.
2. Else, run `rota repo which --json` from the current cwd; if it succeeds (`data.name`), default to the resolved name.
3. Else, ask via `AskUserQuestion`:
   - **Header:** `"Repo"`
   - **Question:** *"Which sub-repo should `spike/<name>` live in?"*
   - **Options:** one per registered sub-repo (read names from `.rota/repos.json`, or `rota repo resolve --json`), single-select.

Carry `<repo>` into Step 4's verb invocation as `--repo <repo>`.

## Step 3 (Start mode) — Branch Without Asking

No confirmation: the spike branch is throwaway. On a clean tree, create it and switch to it. On a dirty tree, create it but stay on the current branch (so the changes don't follow) and say so in the handoff. Umbrella mode: the branch is created in `<repo>`.

## Step 4 (Start mode) — Create the Spike

```bash
# Single-repo:
BRANCH=$(rota spike add --json <name> --question "<question>" | jq -r .data.branch)
# Umbrella mode — spike lives in <repo>:
BRANCH=$(rota spike add --json --repo <repo> <name> --question "<question>" | jq -r .data.branch)
```

The verb:

- Creates branch `spike/<name>` off the current HEAD (in the sub-repo's git history when `--repo` is set)
- Writes `.rota/spikes/<name>.md` with frontmatter + question + section stubs
- Spike file `.rota/spikes/<name>.md` lives at the umbrella root regardless of `--repo`; only the git branch lands in the sub-repo. The frontmatter records `repo: <name>` so `/rota-spike done` and listings know which sub-repo to operate against.

On a clean tree, run `git checkout "$BRANCH"` — in umbrella mode, `cd` into `<repo>` first (or `git -C <repo-path> checkout "$BRANCH"`).

Compact handoff:

```
Spike opened: spike/<name>           # umbrella: Spike opened: spike/<name> (in <repo>)
Question: <one line>
File: .rota/spikes/<name>.md

Hack freely on the branch. When done, return to main and run:
  /rota-spike done <name>
```

No further work in this skill — the user drives the experiment.

## Step 5 (Finish mode) — Read the Spike Branch

Read [`finish-mode.md`](finish-mode.md) when Step 1 chose Finish mode and follow it: Steps 5 to 7 (read the spike branch, write the findings and mark the spike done, optional follow-up).

## Step 6 (Finish mode) — Write the Findings

See Step 5: `finish-mode.md` holds Steps 5 to 7.

## Step 7 (Finish mode) — Optional Follow-Up

See Step 5: `finish-mode.md` holds Steps 5 to 7.

## Key Principles

- **One question per spike.** Multiple questions → multiple spikes.
- **Spikes are scoped, not open-ended.** A spike open >2 weeks without a decision is stale — close it `inconclusive` and recapture if needed.

## References

- [`references/umbrella-mode.md`](references/umbrella-mode.md) — Umbrella-mode verbs, registry shape, and `Repos:` field semantics.
