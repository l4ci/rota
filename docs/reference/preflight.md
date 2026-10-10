# Project check reference

Skills no longer run a preflight step. Every `rota` verb that needs `.rota/` exits `3` when it cannot find one, so a skill that touches a project learns about a missing init from the verb itself. `rota init check` is the explicit check, for the few places that want one up front and for scripts. Before a [parallel round](../usage/parallel-rounds.md), `rota doctor` checks the machine instead of the project.

## `rota init check`

```bash
rota init check
rota init check --json
```

| Exit | Meaning | What to do |
|------|---------|------------|
| `0` | `.rota/` and its core files are present. | Proceed. |
| `1` | Not initialized: `.rota/` or one of the core files is missing. `--json` lists every missing path in `data.missing`. | Tell the user to run `rota init`, then **stop**. Never auto-init: initialization needs the user's consent. |

`rota init check` acts on the working directory (after `-C`) with no walk-up, so it also runs where there is no `.rota/` yet.

Core files it checks under `.rota/`:

- `DECISIONS.md`
- `BACKLOG.md`
- `KNOWLEDGE.md`
- `MILESTONES.md`
- `counters.json`
- `config.json`
- `status.json`

Advisory findings come back as `warnings`, never as a failure: an umbrella flag that disagrees with the registry, and version drift between the project's stamped `rota.version` (the old `hvSkills.version` is read as a fallback until `rota init` / `rota config fill` moves it) and the installed binary (`rota version --drift` reports the same thing on its own).

## `rota doctor`

`rota init check` asks whether the project is set up. `rota doctor` asks whether the machine can run a round: git, jq, the terminal host, the tracker login, accounts, the orchestrator hooks, the installed skills and Codex.

```bash
rota doctor
rota doctor --json
```

It is read-only, spends no usage quota, and runs without `.rota/` (it falls back to default config). Each check reports `pass`, `fail` or `skip`; a `disk` line (`warn`) appears only when free disk space is low and never fails the run, and a `verify` line (`warn`) only while `test.full` and `test.e2e` are both empty. A `fail` carries a `hint` with the one command or edit that fixes it, and `detail` says what was found (`herdr 0.8.2, need 0.9.x`).

| Exit | Meaning | What to do |
|------|---------|------------|
| `0` | Every check passed or was skipped. | Proceed. |
| `1` | At least one check failed. `--json` gives the same `data`, with `ok: false`. | Run each failed check's `hint`, then run `rota doctor` again. |

A missing tool is a failed check, never exit 5.

The checks, in the order they run:

| Check | Passes when | Skipped when |
|-------|-------------|--------------|
| `git` | git is on `PATH` and `.worktrees/` is gitignored | never |
| `jq` | `jq` is on `PATH` (the skills read `rota --json` output with it) | never |
| `host` | the host a round would run on is usable: `work.dispatch` as named, or with it unset or `subagent`, herdr inside a herdr pane, else tmux inside tmux. herdr on `PATH` and 0.9.x, or tmux on `PATH` | no host is detected (solo) |
| `tracker` | `gh` or `glab` is on `PATH` and logged in for the project's provider | the project has no remote |
| `accounts` | every account in `work.accounts` has a `configDir` with a credentials file | no accounts configured |
| `hook` | herdr's agent integration is installed for each configured account (`herdr integration install claude`) | the host is not herdr, or no account is configured |
| `statusline` | the effective statusline runs `rota statusline dump` | the orchestrator hooks are not installed (opt-in) |
| `stop-hook` | a Stop and a SessionStart hook installed by `rota hook install` exist and their command resolves | the orchestrator hooks are not installed (opt-in) |
| `guard-hook` | a `PreToolUse` hook installed by `rota hook install` sits in project or user scope (project-local never reaches a worker worktree) and its command resolves | the orchestrator hooks are not installed (opt-in) |
| `switch` | with `orchestrator.switchOnUsage` on: two or more accounts have a `configDir` and the Stop hook is installed | `orchestrator.switchOnUsage` is off |
| `skills` | every installed skills root (user and project, Claude and Codex, including each `work.accounts` config dir) matches the binary's skill set, and has no missing or edited files | no root has a `.rota-manifest.json` (run `rota skills install`) |
| `codex` | `codex` runs, and the default Codex home (or each `work.codexAccounts` account) is logged in and has the herdr integration | `codex` is not on `PATH` and no `work.codexAccounts` are configured |
| `agents` | the subagent files `rota agents write` generates are present and current | nothing to report: the line appears only as a `warn`, when a file is missing or stale (fix: `rota agents write`) |
| `verify` | `test.full` or `test.e2e` is set, so the merge gate has something to run | nothing to report: the line appears only as a `warn`, when both are empty under a local verify (fix: `rota config set test.full '[...]'`) |

The three hook checks are opt-in. Until something `rota hook install` writes is present, they skip and do not fail a project that never installed the hooks. Once it is, a partial or broken install fails. `skills` follows the same rule: it skips until `rota skills install` has written a manifest.

See [unattended rounds](../usage/unattended-rounds.md) for the hooks and [parallel rounds](../usage/parallel-rounds.md) for what a round does after a clean `rota doctor`.

## Missing `.rota/` from any other verb

Exit `3` (`resolution`), with the message `no .rota/ directory here or in any parent` and the hint `run: rota init`. The skills carry no special message for it: they surface the verb's error and hint, and the user runs `rota init`.

| Skill or verb | When `.rota/` is missing |
|-------|------------------------|
| Any verb that reads project state | Exit `3` with the hint `run: rota init`. |
| `rota update` | Not affected: it runs without a project. |
| `rota init` | Is the bootstrapper itself. |

All exit codes: [`rota` verb reference](cli-helpers.md#conventions).
