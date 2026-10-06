# Spike Finish Mode (Steps 5 to 7)

## Step 5 (Finish mode) — Read the Spike Branch

Read `repo:` from `.rota/spikes/<name>.md`'s frontmatter first (parallel-load with the spike file). When set, run the git calls in the sub-repo (`rota repo resolve <name>`, `data.repos[0].path`) via `git -C <sub-repo path>`; when unset, run them in the cwd:

```bash
# Single-repo (no `repo:` in frontmatter):
git log spike/<name> --oneline
git diff main...spike/<name> --stat

# Umbrella mode (`repo: <name>` in frontmatter):
git -C <sub-repo path> log spike/<name> --oneline
git -C <sub-repo path> diff main...spike/<name> --stat
```

Read the spike file for the original question and any notes already written.

Ask for the verbal summary if not already given: what they learned, viable or not, and why.

**If the user stays silent.** Don't pause indefinitely and don't fabricate findings. Pivot to `AskUserQuestion`:

- Pre-fill **What was tried** from the diff stat and commit log above.
- Ask one structured question: header `"Decision"`, single-select with options `"viable"`, `"not viable"`, `"depends on X"` (free-text X via "Other"), `"inconclusive"`.
- On `viable`, follow up with one short free-text question for the recommended approach (one line); otherwise leave it empty in Step 6.

If even that returns nothing usable (dismissed or empty), write `Decision: inconclusive` and `Findings:` listing only the observed diff/commit signals. Never invent findings the user didn't confirm.

## Step 6 (Finish mode) — Write the Findings

`Edit` `.rota/spikes/<name>.md` to fill in:

- **What was tried** — concrete commands run, libraries pulled in, files touched (cite the diff stat)
- **Findings** — 3–5 bullets. Bad findings are as valuable as good
- **Decision** — `viable` / `not viable` / `depends-on-X` / `inconclusive`
- **Recommended approach** — only if viable. Describe the shape of the *real* implementation. Do not paste spike code

Then mark the spike done:

```bash
rota spike finish <name>
```

It sets `status: done` and `finished: <date>` (a repeat call is a no-op). The branch stays as-is, never merged.

**Promotion nudge.** For a `viable`, `not viable` or `depends-on-X` decision, print one line and move on: *"Run `/rota-decide --from-spike <name>` to promote this to a hard boundary."* Say nothing for `inconclusive` or empty. Don't ask, don't dispatch.

## Step 7 (Finish mode) — Optional Follow-Up

If the decision is `viable` and the user is ready to act, offer one of:

- *"Capture the real implementation as a backlog item? (`/rota-capture`)"*
- *"Write a plan for it now? (`/rota-plan`)"*

If not viable or inconclusive, the spike is its own conclusion; don't push to capture work it argued against.
