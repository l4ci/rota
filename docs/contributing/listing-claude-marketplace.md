# Listing rota in Anthropic's plugin directory

How to submit the rota plugin to Anthropic's directory, which Claude Code users browse as a marketplace, and how each release reaches it. Checked against Anthropic's plugin docs on 2026-10-09 (`claude.com/docs/plugins/submit` and `pre-submission-checklist`). Anthropic changes the portal and its rules without notice, so re-read both pages before a submission.

The maintainer submits by hand. Nothing in this repo calls the portal.

## Before you open the portal

1. From the repo root, run `claude plugin validate .`. On 2026-10-09 it passed with two findings:
   - Warning: `CLAUDE.md` at the plugin root is not loaded as project context. The plugin root is the repo root, so the repo's own `CLAUDE.md` triggers this. Not blocking.
   - Advice: the README has no one-line marketplace install command. Not blocking. Adding one is a plugin change, so it belongs in a follow-up issue.
2. Confirm the repo is public (it must be before the listing goes live) and that the GitHub account on claude.ai has push access to `l4ci/rota`.
3. Confirm `.claude-plugin/plugin.json` `version` matches the release you are submitting. The directory reads the version from that file.

## Submit

1. Open `https://claude.ai/directory/manage`, select **Submit new**, then **Plugin bundle**.
2. Fill the fields in the table below.
3. Select **Validate** and fix every blocking finding. The portal runs more checks than the CLI.
4. Answer the data-handling questions, enter the contact email, and tick all four acknowledgements.
5. Choose how new versions arrive and whether they publish automatically, then select **Submit for review**.

A submission is one plugin folder. The repository and folder cannot change after submission. To move them, submit again.

## Fields

The listing name, description, author, version and license come from `plugin.json`, not from portal fields. Edit the file, push, and validate again to change them.

| Field | Value |
| :- | :- |
| Submission type | Plugin bundle |
| Repository | `https://github.com/l4ci/rota` |
| Plugin path | empty (the plugin is the repo root) |
| Branch or tag | empty (follows the default branch) |
| Listing name | `rota` (from `plugin.json` `name`) |
| Listing description | from `plugin.json` `description` (see the PR draft) |
| Version | from `plugin.json` `version` |
| Update delivery | GitHub push webhook |
| Auto-publish new versions | off for the first submission |
| Contact email | the maintainer's address, entered in the portal |

Data-handling answers (verify each one before submitting):

- Reads or stores personal data: no. The plugin reads its own manifest and runs `rota --version`. The rota binary keeps its state in the user's repo under `.rota/`.
- Sends data to services other than declared connectors: yes, to GitHub (or GitLab). `rota` uses the user's own `gh` or `glab` login when they run issue, round or ship commands. `/rota:rota-install` downloads the rota binary from GitHub Releases.
- Retention: the plugin keeps nothing. rota keeps its `.rota/` files in the user's repo, and the issues and PRs it creates stay on GitHub or GitLab.
- Intended for people under 18: no.

## Review and updates

The directory checks the newest commit on the tracked branch. Each version ends in one of three states:

- **Passes:** publishable.
- **Held for a reviewer:** goes live only after Anthropic clears it.
- **Doesn't pass:** the portal lists the rules it breaks. A rejected first submission must be resubmitted after a fix.

For each release:

1. Bump `plugin.json` `version`, merge to the default branch, and tag the release.
2. In the portal, select **Check for new commits** on the plugin's **Settings** tab if the webhook has not already triggered a check.
3. Publish the version unless auto-publish is on. The listing keeps serving the last published version until the new one is published.

This is the **bump the marketplace listing** step in `.rota/RELEASE.md`.

## Known findings to clear

From the checklist and the validator, not a live submission. None blocks, but a reviewer may hold the version.

- `plugin.json` lists `./plugin/hooks/hooks.json` in its `hooks` field. The checklist warns on this. Dropping the field clears it. That edits `plugin.json`, a plugin change, so it goes to a follow-up issue.
- The `session-start.sh` hook runs `sh "${CLAUDE_PLUGIN_ROOT}/plugin/hooks/session-start.sh"`, a full path from `${CLAUDE_PLUGIN_ROOT}` as the checklist requires.
- The `rota-install` skill downloads a binary. The checklist requires the README to describe everything the plugin fetches, and the security scan flags undisclosed behavior. Check the README covers the download before submitting.

## Record of submissions

Add one line per submission and per review outcome. The maintainer writes these.

- None yet. The first submission waits on the maintainer's review of this draft.
