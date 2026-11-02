# Worker setup

One-time setup for separate worker sessions. Follow [Your first parallel round](getting-started.md) for the full flow.

## Open your agent in a terminal host

Install [herdr](https://herdr.dev) or [tmux](https://github.com/tmux/tmux), then open it:

```sh
herdr
```

Or, with tmux:

```sh
tmux new -s my-project
```

In a pane, change to your project and start Claude Code or Codex. Invoke `/rota-init` there (`$rota-init` in Codex). rota detects the host when the round starts.

With herdr, install the integration for each harness and account you use. For your default account:

```sh
herdr integration install claude
# If using Codex:
herdr integration install codex
herdr integration status
```

Without a host, the skill falls back to Claude subagents sharing the orchestrator's account. Use herdr or tmux for the separate sessions described in this guide.

## Connect your tracker

For GitHub, log in with `gh auth login`; for GitLab, use `glab auth login`. The repository needs a pushed base branch and an `origin` remote.

In `/rota-init`, choose the issue source you use. Start with two independent issues that say what a successful change must do. [Other issue sources](usage/issue-backend.md) are supported too.

## Add your accounts

One account is enough to begin. To use more, ask your agent to configure the named logins for this machine. Account paths belong in local or global configuration, not the shared project config.

For example, two already-authenticated Claude config directories:

```sh
rota config set --local round.accounts '[{"kind":"claude","name":"main","dir":"/home/you/.claude"},{"kind":"claude","name":"second","dir":"/home/you/.claude-second"}]'
rota skills install
```

Replace the paths with your own. To create a second login, launch `claude` with `CLAUDE_CONFIG_DIR` set to a separate directory and sign in. With herdr, install its integration under that account too.

For Codex, use entries with `kind: "codex"` pointing to authenticated Codex homes. [Codex account setup](usage/codex-workers.md#several-codex-accounts) includes the commands. Claude accounts can be selected by measured headroom; Codex allocation and limit handling work differently.

## Decide how changes land

Confirm the project's check command during `/rota-init`. It can also be set later, for example:

```sh
rota config set test.full 'npm test'
```

Use your project's real command. The gate checks the PR combined with the current base before merging. [No tests yet?](troubleshooting.md#no-tests-yet)

A passing gate merges automatically by default. To require your approval for every merge:

```sh
rota config set ship.mergeApproval all
```

The approval happens on the PR thread. [Approval details](usage/parallel-rounds.md#merge-approval).

Workers launch with unattended permissions by default; review [worker permissions](usage/parallel-rounds.md#worker-guard) and your harness launch configuration before starting. The orchestrator may request permission to run rota, git and tracker commands in your session.

## Check and start

Review and commit the generated project files. In your agent, invoke `/rota-orchestrate` (`$rota-orchestrate` in Codex). It runs `rota doctor` first and helps resolve failures before assigning work.

Stay available for the first round. Starting the skill in an existing session does not install a restart supervisor. For restarts across context limits and account switching, set up [unattended rounds](usage/unattended-rounds.md) later.
