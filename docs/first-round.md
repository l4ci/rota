# Your first round

This walks you from a fresh machine to a first parallel round in [herdr](https://herdr.dev) or [tmux](https://github.com/tmux/tmux): an orchestrator in one pane, workers in tabs (tmux: windows), each worker on its own issue. The details behind each step live in [parallel rounds](usage/parallel-rounds.md); this page is the order to do them in.

## 1. Prerequisites

- A git repo with an `origin`, and the base branch (usually `main`) pushed.
- `gh` (GitHub) or `glab` (GitLab), logged in.
- A few open issues with acceptance criteria. A round only offers issues that have them (or a design or plan note).
- `claude` (Claude Code) on your `PATH`.
- A terminal host: herdr 0.9.x, from [herdr.dev](https://herdr.dev), or [tmux](https://github.com/tmux/tmux). herdr reports each worker's state directly; under tmux rota reads the panes instead.

## 2. Install rota

```bash
curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh
# or: brew install l4ci/tap/rota
rota version
```

See [install](install.md) for options and upgrades.

## 3. Install the skills

```bash
rota skills install                  # user scope: every project on this machine
rota skills install --scope project  # this repo only; commit .claude/skills and .agents/skills
```

Pick user scope for yourself. Pick project scope when a team should run the same pinned version. More in [install](install.md#the-skills).

## 4. Initialize the project

At the project root:

```bash
rota init
```

Commit `.rota/`, `AGENTS.md`, `CLAUDE.md` and `.gitignore`. `rota init` sets three worker slots. For a small first round, use two:

```bash
rota config set work.workerSlots 2
```

## 5. Extra accounts (optional)

Skip this if you have one Claude account. For each additional account, create a config dir, log in once, and install the skills into it. User-scope skills live per config dir, so each account needs its own copy (a project-scope install covers every account).

```bash
mkdir ~/.claude-b
CLAUDE_CONFIG_DIR=~/.claude-b claude          # log in, then exit
CLAUDE_CONFIG_DIR=~/.claude-b rota skills install
```

## 6. Tell rota about the accounts

Account paths are machine-specific, so they go in `.rota/config.local.json` (gitignored), not the tracked config. Edit it by hand, list your main account too, and use absolute paths:

```json
{
  "work": {
    "accounts": [
      { "name": "main", "configDir": "/home/you/.claude" },
      { "name": "b", "configDir": "/home/you/.claude-b" }
    ]
  }
}
```

Then check them:

```bash
rota worker account list
```

## 7. Start herdr or tmux

```bash
herdr                       # or: herdr --session <name>
tmux new -s <name>          # with tmux instead
```

Open a pane and `cd` to the project root. Run everything from here on in that pane, so the round can detect its host.

## 8. Install the herdr integration (herdr only)

Skip this under tmux. herdr reports each worker's state (working, blocked, idle) only for agents it has an integration for. Install it once per account:

```bash
herdr integration install claude
CLAUDE_CONFIG_DIR=~/.claude-b herdr integration install claude   # each extra account
herdr integration status
```

## 9. Orchestrator permissions

The orchestrator runs many `rota`, git and forge commands. Allow them in `.claude/settings.local.json` so it doesn't stop at every prompt:

```json
{
  "permissions": {
    "allow": [
      "Bash(rota *)",
      "Bash(git *)",
      "Bash(gh *)",
      "Bash(glab *)"
    ]
  }
}
```

`gh` covers GitHub and `glab` GitLab; a rule for a CLI you don't use never matches. `Bash(rota *)` allows `rota` with any arguments; the older `Bash(rota:*)` form means the same. See Claude Code's [permission rules](https://code.claude.com/docs/en/permissions#wildcard-patterns). Running with `--dangerously-skip-permissions` is your call; rota doesn't do it for you. Workers are different: they launch with permissions skipped by default (`work.workerCommand`), because nobody is in their pane to answer a prompt. See [configuration](usage/configuration.md#workdispatch-subagent-tmux-or-herdr).

## 10. Unattended hooks (skip for now)

`rota hook install` adds handoff hooks so a long round can survive the orchestrator's context filling. It writes `.claude/settings.local.json` by default; `--scope project` and `--scope user` also exist, `--wrap-statusline` keeps a statusline you already have, and `rota` must be on `PATH`.

For a first round, skip the hooks and keepalive both. Hooks without keepalive means the orchestrator exits at the context threshold and nothing restarts it. Install them together later: [unattended rounds](usage/unattended-rounds.md).

## 11. Run doctor

```bash
rota doctor
```

Fix every `fail`; each one prints the command or edit that fixes it. Run it inside the herdr or tmux pane so it sees the host. See [doctor and reap](usage/doctor-and-reap.md).

## 12. Launch the orchestrator

In the pane, at the project root:

```bash
rota orchestrate
```

It runs `rota doctor`, then opens a focused orchestrator tab that runs the agent under `rota keepalive run` and has already started `/rota-orchestrate`. `rota` alone opens a small palette (banner, version, the project and round state, and the common actions) with Orchestrate preselected, so `rota` then Enter does the same. Tell the orchestrator what you want, for example "run a round on issues 12 and 13". `orchestrator.harness` picks the agent (`claude`, `codex`, `hermes` or `opencode`). A `claude` orchestrator starts under your `CLAUDE_CONFIG_DIR` if set, else under the `work.accounts` entry with the most headroom. See [your first round](usage/parallel-rounds.md#your-first-round) for what happens outside herdr or tmux.

The skill runs `rota doctor` again, then `rota round start`. That takes the orchestrator lease, creates the worker slots and lists the ready issues, and it detects herdr or tmux from the pane it runs in. It starts no worker yet. The orchestrator then picks the slate and assigns each issue with `rota round assign`, which cuts a branch and starts a worker in a new tab.

## 13. Watch

Each worker shows up as a herdr tab or a tmux window. To see all slots with their host, PR and drift:

```bash
rota round status
```

You don't need to poll. The orchestrator waits on `rota round wait` and wakes when a slot is done, blocked or dead. A slot is free as soon as its worker opens a PR: the orchestrator gives it the next issue while that PR waits for review.

## 14. Merges and escalations

Workers open PRs and never merge. For each finished PR the orchestrator reads the diff, runs the gate (`rota worker gate`) on the merged tree and merges on a pass. By default (`ship.mergeApproval` is `none`) a passing gate merges. If you set `all` or `paths`, the gate asks you on the PR thread first and merges only after you reply `approve`, `yes` or `lgtm`. See [merge approval](usage/parallel-rounds.md#merge-approval).

When a worker or the orchestrator needs a human decision, it posts the question on the issue or PR thread, or asks in the pane. Answer there. A line starting with `m:` typed into a pane counts as a maintainer answer: [details](usage/parallel-rounds.md#maintainer-answers-typed-into-a-pane).

## 15. Wind down

When the slate is done, tell the orchestrator to finish. It runs `rota round wind-down`, which re-verifies the base branch, parks every slot and releases the lease. Then clean up what is left:

```bash
rota reap            # preview
rota reap --apply    # remove the leftovers that hold no work
```

See [doctor and reap](usage/doctor-and-reap.md).

## Next

- [Parallel rounds](usage/parallel-rounds.md): every verb, scope, tiers, solo mode.
- [Unattended rounds](usage/unattended-rounds.md): hooks, keepalive and usage limits.
- [Codex workers](usage/codex-workers.md): run Codex in some slots.
