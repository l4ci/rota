# Spike Finish Mode (Steps 5 to 7)

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
