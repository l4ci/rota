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

In Finish mode with no spike named, see Step 5.

## Step 2 (Start mode) — Sharpen the Question and Resolve the Repo

A spike answers a *yes/no/conditional* question. Push back on a vague one:

- ❌ *"Try Server-Sent Events"*
- ✅ *"Can we use SSE for live updates over our existing nginx setup without proxy buffering issues?"*

Name it with a short kebab-case identifier (`sse-feasibility`, `auth-rotation`). It becomes the branch suffix and the spike file's stem.

In umbrella mode (`rota repo umbrella` exits 0), read [`start-umbrella.md`](start-umbrella.md) first: it resolves `<repo>` for `--repo <repo>` in Step 4.

## Step 3 (Start mode) — Branch Without Asking

No confirmation: the branch is throwaway. On a clean tree, create it and switch to it. On a dirty tree, create it but stay on the current branch (so the changes don't follow) and say so in the handoff.

## Step 4 (Start mode) — Create the Spike

```bash
BRANCH=$(rota spike add --json <name> --question "<question>" | jq -r .data.branch)
```

The verb creates branch `spike/<name>` off the current HEAD (in the sub-repo with `--repo`) and writes `.rota/spikes/<name>.md` (frontmatter, question, section stubs).

On a clean tree, run `git checkout "$BRANCH"`. In umbrella mode, `start-umbrella.md` covers `--repo`, the checkout and the handoff.

Compact handoff:

```
Spike opened: spike/<name>
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

- [`start-umbrella.md`](start-umbrella.md) — umbrella-mode start: resolve `<repo>`, `--repo` variant of the verb, checkout and handoff.
- [`finish-mode.md`](finish-mode.md) — Steps 5 to 7: pick the spike, read the branch, write findings, follow-up.
- [`references/umbrella-mode.md`](references/umbrella-mode.md) — umbrella-mode verbs, registry shape, `Repos:` field semantics.
