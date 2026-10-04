---
name: rota-spike
description: Throwaway feasibility experiment on a dedicated git branch — answers a specific question without polluting main or the backlog. Creates spike/<name> branch and .rota/spikes/<name>.md for question + findings + decision. Branch is never merged; only findings come back. Use when you need to try X before committing to it ("can we use SSE?", "does this library handle our scale?").
---

**Print the banner below verbatim before any other action — skip if dispatched as a subagent.** See `references/banner-preamble.md`.

```
════════════════════════════════════════════════════════════════════════
  🧪  rota-spike  ·  throwaway feasibility experiment on a branch
  triggers: "spike X", "feasibility"  ·  pairs: rota-vision, rota-plan
════════════════════════════════════════════════════════════════════════
```

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

**Initialize task list.** Follow the canonical pattern in `references/task-list-init.md` — load `TaskCreate(…)` via `ToolSearch select:TaskCreate,TaskUpdate` if needed, then create one task per phase below.

Phases:

1. *Question* — Start: yes/no/conditional question sharpened (Step 2)
2. *Branch* — Start: `spike/<name>` created, scratch file seeded (Steps 2.5–4)
3. *Investigate* — Start: experiment runs to a clear answer (between Start and Finish)
4. *Findings* — Finish: spike file's findings section written from the branch state (Steps 5–6)
5. *Decision* — Finish: verdict (yes / no / conditional / inconclusive) recorded (Step 6)
6. *Promote / cleanup* — Finish: optional `/rota-decide --from-spike`, branch deleted (Steps 6.5–7)

## Step 2 (Start mode) — Sharpen the Question

A spike answers a *yes/no/conditional* question. Push back if the question is vague:

- ❌ *"Try Server-Sent Events"* — too open
- ✅ *"Can we use SSE for live updates over our existing nginx setup without proxy buffering issues?"*

Name the spike with a short kebab-case identifier (`sse-feasibility`, `auth-rotation`, `migration-cost`). The name becomes the branch suffix and the spike file's stem.

## Step 2.5 (Start mode) — Resolve Sub-Repo (umbrella mode only)

Skip this step entirely when umbrella mode is off (`rota repo umbrella` exits 1). See `references/umbrella-mode.md` for what umbrella mode means and how the registry works.

In umbrella mode, the spike branch must land in a specific sub-repo (the umbrella root often is not a git repo at all). Resolve `<repo>` via the 3-step fallback codified in KNOWLEDGE 2026-05-02:

1. If the user named a sub-repo in their input (e.g. *"spike SSE feasibility in web"*) — use it.
2. Else, run `rota repo which --json` from the current cwd; if it succeeds (`data.name`), default to the resolved name.
3. Else, ask via `AskUserQuestion`:
   - **Header:** `"Repo"`
   - **Question:** *"Which sub-repo should `spike/<name>` live in?"*
   - **Options:** one per registered sub-repo (read names from `.rota/repos.json`, or `rota repo resolve --json`), single-select.

Carry `<repo>` into Step 4's verb invocation as `--repo <repo>`. The spike file still lands at the umbrella's `.rota/spikes/<name>.md` — only the git branch lives in the sub-repo, per the umbrella-vs-sub-repo `.git/` distinction in `references/umbrella-mode.md`.

## Step 3 (Start mode) — Confirm Before Branching

Before mutating git state, confirm with the user via `AskUserQuestion`:

- **Header:** `"Spike"`
- **Question:** *"Create branch `spike/<name>` and switch to it now?"* (umbrella mode: *"Create branch `spike/<name>` in `<repo>` and switch to it now?"*)
- **Options:**
  1. *"Yes, create and switch (Recommended)"* — *"Branches off current HEAD; you'll be on the spike branch immediately."*
  2. *"Create only, stay on this branch"* — *"Useful when you want to switch on your own time."*
  3. *"Cancel"* — *"Don't do anything."*

Plain-text fallback: if the working tree is clean, default to "create and switch"; otherwise default to "create only" so dirty changes don't follow.

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

If the user picked "create and switch" in Step 3, run `git checkout "$BRANCH"` — in umbrella mode, `cd` into `<repo>` first (or `git -C <repo-path> checkout "$BRANCH"`).

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

Read `.rota/spikes/<name>.md` for the original question and any notes the user already wrote (the file always lives at the umbrella root, regardless of `repo:`).

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

## Step 6.5 (Finish mode) — Promote Finding to Decision (nudge)

Read back the `Decision` field Step 6 just wrote. Skip this step entirely — no nudge, no question, no log — when the decision is `inconclusive` or empty. An inconclusive spike doesn't have enough evidence to be a hard boundary.

When the decision is `viable`, `not viable`, or `depends-on-X`, ask via `AskUserQuestion`:

- **Header:** `"Decide"`
- **Question:** *"This spike concluded `<verdict>`. Promote the finding to a hard-boundary decision in `DECISIONS.md`?"* (substitute the verbatim verdict)
- **Options:**
  1. *"Yes, promote (Recommended)"* — *"Invoke `/rota-decide --from-spike <name>`. The spike's question + decision + recommended approach pre-fill the rule and why; you'll articulate the forbids/permits."*
  2. *"Skip — keep finding in spike file only"* — *"No decision is captured. The finding stays in `.rota/spikes/<name>.md` for reference."*

Plain-text fallback: *"Promote to a decision?"* — yes / no.

On **Yes**, dispatch `/rota-decide` via the `Skill` tool with `--from-spike <name>` as the argument, then continue to Step 7 once it returns.

On **Skip**, print one line — *"Spike finding stays in `.rota/spikes/<name>.md`. Run `/rota-decide --from-spike <name>` later if you change your mind."* — then continue to Step 7.

## Step 7 (Finish mode) — Optional Follow-Up

If the decision is `viable` and the user is ready to act, offer one of:

- *"Capture the real implementation as a backlog item? (`/rota-capture`)"*
- *"Write a plan for it now? (`/rota-plan`)"*

If not viable or inconclusive, the spike is its own conclusion. Don't push to capture work that the spike just argued against.

## Key Principles

- **One question per spike.** Multiple questions → multiple spikes.
- **The branch never merges.** Findings come back as a markdown file; code stays on the branch as reference.
- **Honest reporting beats salvage.** A "not viable" conclusion is just as valuable as "viable".
- **No stubs or partial work back to main.** Anything on main is real implementation.
- **Spikes are scoped, not open-ended.** A spike open >2 weeks without a decision is stale — close it `inconclusive` and recapture if needed.
- **Spikes feed decisions, not the other way around.** A `viable` / `not viable` / `depends-on-X` finish is a natural moment to ask whether the conclusion is a commitment future work must respect; an `inconclusive` finish is not.
- **Umbrella spikes are per-repo.** A spike's branch lives in the sub-repo named in its frontmatter `repo:` field; the spike file itself stays at the umbrella root.

## References

- [`references/banner-preamble.md`](references/banner-preamble.md) — Banner-print rule shared by every skill.
- [`references/umbrella-mode.md`](references/umbrella-mode.md) — Umbrella-mode verbs, registry shape, and `Repos:` field semantics.
