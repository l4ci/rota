# Your first parallel round

Install rota through your coding agent, confirm the project settings, then start a parallel round.

## Bring a project and two tasks

Use a git repository with a pushed base branch and two independent issues with clear acceptance criteria. For GitHub or GitLab PRs, have `gh` or `glab` installed and logged in.

Open Claude Code or Codex in the project **inside herdr or tmux**. This lets rota launch separate worker sessions and use multiple accounts. [Worker setup](first-round.md) covers the one-time details.

## Install rota

Paste this into your coding agent, in the project:

```text
Install rota and set it up in this project. Follow https://raw.githubusercontent.com/l4ci/rota/main/install.md
```

Alternative: [Homebrew](reference/installation.md#homebrew) · [curl](reference/installation.md#install-script) · [Claude Plugin](reference/installation.md#-plugin-marketplace) · [Codex Plugin](reference/installation.md#codex) · [More](reference/installation.md).

Reopen your agent session if the new skills are not visible.

## Set up the project

If installation already confirmed your project settings, skip to [starting the round](#start-the-round). To review or tune them, use:

```text
/rota-init
```

In Codex, type `$rota-init`.

The skill reads the project and proposes settings for you to confirm: where the issues live, which agents to use and how to check changes. It also initializes a project that has no `.rota/` yet.

Tell it which command checks the project before a merge. If you have no tests yet, you can leave this unset, but the merge gate will wait for a configured check. There is currently no automatic reminder conversation; see [starting without tests](troubleshooting.md#no-tests-yet).

Review and commit the setup files. Additional accounts and a human-approval policy are separate settings today; [worker setup](first-round.md) shows how to add them.

## Start the round

In the same agent:

```text
/rota-orchestrate
```

In Codex, type `$rota-orchestrate`. Then give it the scope, for example:

> Work on issues #12 and #13 in parallel, with two workers. Ask me about unresolved product decisions.

The orchestrator checks the environment before assigning work. It reports missing prerequisites with the next step to fix them. You do not need to open rota's CLI interface.

## Follow the work

Each worker gets its own worktree and works on one issue. The orchestrator coordinates reviews, runs the merge gate and reports progress. Answer product questions in the conversation or the linked issue.

A worker saying "done" is not the end: its PR still has to pass the required review and checks on the combined code. Failed checks keep it from merging and send it back for another attempt. By default, a passing gate merges automatically; [require your approval](usage/parallel-rounds.md#merge-approval) if you prefer.

When the selected work is finished, ask the orchestrator to wind down. It verifies the base branch and reports what merged, what remains open and what needs you.

[How changes are checked](agent-mistakes.md) · [Troubleshooting](troubleshooting.md)
