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

Read `repo:` from `.rota/spikes/<name>.md`'s frontmatter first (parallel-load with the spike-file content). When set, run the `git log` / `git diff` calls in the sub-repo (resolve via `rota repo resolve <name>`, `data.repos[0].path`); when unset, run them in the cwd. Inspect git either by `cd`-ing into the sub-repo before the call or by passing `git -C <sub-repo path>`:

```bash
# Single-repo (no `repo:` in frontmatter) — run in cwd:
git log spike/<name> --oneline
git diff main...spike/<name> --stat

# Umbrella mode (`repo: <name>` in frontmatter) — run against the sub-repo:
git -C <sub-repo path> log spike/<name> --oneline
git -C <sub-repo path> diff main...spike/<name> --stat
```

Read `.rota/spikes/<name>.md` for the original question and any notes the user already wrote.

Ask the user for the verbal summary if they haven't already given one — what they learned, viable or not, and why.

**Fallback when the user stays silent.** Don't pause indefinitely and don't fabricate findings from intuition. Pivot to question-only mode via `AskUserQuestion`:

- Pre-fill **What was tried** from the diff stat + commit log already gathered above — objective signals, no judgment required.
- Ask one structured question for the decision: header `"Decision"`, single-select with options `"viable"`, `"not viable"`, `"depends on X"` (free-text X via "Other"), `"inconclusive"`.
- If the user picks `viable`, follow up with one short free-text question for the recommended approach (one line); otherwise skip it and leave the field empty in Step 6.

If even the structured question returns nothing usable (dismissed or empty), write `Decision: inconclusive` and `Findings:` listing only the observed diff/commit signals — never invent findings the user didn't confirm.

## Step 6 (Finish mode) — Write the Findings

Use the `Edit` tool on `.rota/spikes/<name>.md` to fill in:

- **What was tried** — concrete commands run, libraries pulled in, files touched (cite from the diff stat)
- **Findings** — 3–5 bullets, what you learned. Honest reporting — bad findings are as valuable as good
- **Decision** — `viable` / `not viable` / `depends-on-X` / `inconclusive`
- **Recommended approach** — only if viable. Describe the shape of the *real* implementation. Do not paste spike code

Then mark the spike done:

```bash
rota spike finish <name>
```

The verb sets `status: done` and `finished: <date>` in the spike file (a repeat call is a no-op). The branch is left as-is — historical reference, never merged.

**Promotion nudge.** For a `viable`, `not viable` or `depends-on-X` decision, print one line and move on: *"Run `/rota-decide --from-spike <name>` to promote this to a hard boundary."* Say nothing for `inconclusive` or empty. Don't ask, don't dispatch.

## Step 7 (Finish mode) — Optional Follow-Up

If the decision is `viable` and the user is ready to act, offer one of:

- *"Capture the real implementation as a backlog item? (`/rota-capture`)"*
- *"Write a plan for it now? (`/rota-plan`)"*

If not viable or inconclusive, the spike is its own conclusion. Don't push to capture work that the spike just argued against.

## Key Principles

- **One question per spike.** Multiple questions → multiple spikes.
- **Spikes are scoped, not open-ended.** A spike open >2 weeks without a decision is stale — close it `inconclusive` and recapture if needed.

## References

- [`references/umbrella-mode.md`](references/umbrella-mode.md) — Umbrella-mode verbs, registry shape, and `Repos:` field semantics.
