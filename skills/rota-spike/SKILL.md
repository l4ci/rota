---
name: rota-spike
description: Use when X must be tried before committing to it ("can we use SSE?", "does this library handle our scale?") and the answer is a finding, not shipped code.
---

# rota-spike — Throwaway Feasibility Experiment

**Code on the spike branch is reference, not product.**

Two modes:

- **Start mode** — open a new spike with a question
- **Finish mode** — extract findings from a spike branch into the spike file

Copy this checklist and track your progress (start mode; Step 1 routes to either mode):
```
- [ ] Step 1 — Mode
- [ ] Step 2 (Start mode) — Sharpen the Question and Resolve the Repo
- [ ] Step 3 (Start mode) — Branch Without Asking
- [ ] Step 4 (Start mode) — Create the Spike
```

## Step 1 — Mode

Determine the mode silently:

- *"spike SSE for live updates"*, *"try X"*, *"feasibility check on Y"* → **Start mode**
- *"spike done"*, *"finish the SSE spike"*, *"extract findings"* → **Finish mode**
- Neither matches, or both → ask once

In Finish mode, if no spike is named, list open spikes via `rota spike list` (`data.spikes`, `status` not `done`) and ask which.

## Step 2 (Start mode) — Sharpen the Question and Resolve the Repo

A spike answers a *yes/no/conditional* question. Push back on a vague one:

- ❌ *"Try Server-Sent Events"*
- ✅ *"Can we use SSE for live updates over our existing nginx setup without proxy buffering issues?"*

Name it with a short kebab-case identifier (`sse-feasibility`, `auth-rotation`). It becomes the branch suffix and the spike file's stem.

**Sub-repo (umbrella mode only; skip when `rota repo umbrella` exits 1, see `references/umbrella-mode.md`).** The spike branch must land in a specific sub-repo (the umbrella root often is not a git repo). Resolve `<repo>`:

1. The user named a sub-repo (*"spike SSE feasibility in web"*) → use it.
2. Else `rota repo which --json` from the cwd; on success use `data.name`.
3. Else ask via `AskUserQuestion`:
   - **Header:** `"Repo"`
   - **Question:** *"Which sub-repo should `spike/<name>` live in?"*
   - **Options:** one per registered sub-repo (names from `.rota/repos.json`, or `rota repo resolve --json`), single-select.

Carry `<repo>` into Step 4 as `--repo <repo>`.

## Step 3 (Start mode) — Branch Without Asking

No confirmation: the branch is throwaway. On a clean tree, create it and switch to it. On a dirty tree, create it but stay on the current branch (so the changes don't follow) and say so in the handoff. Umbrella mode: the branch is created in `<repo>`.

## Step 4 (Start mode) — Create the Spike

```bash
# Single-repo:
BRANCH=$(rota spike add --json <name> --question "<question>" | jq -r .data.branch)
# Umbrella mode — spike lives in <repo>:
BRANCH=$(rota spike add --json --repo <repo> <name> --question "<question>" | jq -r .data.branch)
```

The verb creates branch `spike/<name>` off the current HEAD (in the sub-repo with `--repo`) and writes `.rota/spikes/<name>.md` (frontmatter, question, section stubs). The spike file lives at the umbrella root regardless of `--repo`; only the branch lands in the sub-repo. Frontmatter records `repo: <name>` so `/rota-spike done` and listings know which sub-repo to use.

On a clean tree, run `git checkout "$BRANCH"` (umbrella: `cd` into `<repo>` first, or `git -C <repo-path> checkout "$BRANCH"`).

Compact handoff:

```
Spike opened: spike/<name>           # umbrella: Spike opened: spike/<name> (in <repo>)
Question: <one line>
File: .rota/spikes/<name>.md

Hack freely on the branch. When done, return to main and run:
  /rota-spike done <name>
```

Nothing further here; the user drives the experiment.

Copy this checklist and track your progress (finish mode):
```
- [ ] Step 5 (Finish mode) — Read the Spike Branch
- [ ] Step 6 (Finish mode) — Write the Findings
- [ ] Step 7 (Finish mode) — Optional Follow-Up
```

## Step 5 (Finish mode) — Read the Spike Branch

Read [`finish-mode.md`](finish-mode.md) when Step 1 chose Finish mode and follow it: Steps 5 to 7 (read the spike branch, write the findings and mark the spike done, optional follow-up).

## Step 6 (Finish mode) — Write the Findings

See Step 5: `finish-mode.md` holds Steps 5 to 7.

## Step 7 (Finish mode) — Optional Follow-Up

See Step 5: `finish-mode.md` holds Steps 5 to 7.

## Key Principles

- **One question per spike.** Multiple questions → multiple spikes.
- **Scoped, not open-ended.** A spike open >2 weeks without a decision is stale: close it `inconclusive` and recapture if needed.

## References

- [`references/umbrella-mode.md`](references/umbrella-mode.md) — umbrella-mode verbs, registry shape, `Repos:` field semantics.
