# Doctor and reap

Two verbs bracket a [round](parallel-rounds.md). `rota doctor` checks the machine before it starts.
`rota reap` clears what the round left behind after it ends.

## rota doctor

```sh
rota doctor            # one line per check: status, name, detail
rota doctor --json     # {"ok": bool, "checks": [...]}
```

It is read-only. It never writes, never calls a usage endpoint (so it spends no quota) and runs
without `.rota/`, falling back to default config. Each line is `pass`, `fail` or `skip`. Every `fail`
carries a hint: the one command or edit that fixes it.

**Exit codes.** `0` when every check passes or skips. `1` when any check fails; `--json` still prints
the full result, so a caller reads `ok`. A missing tool is a failed check, not an error, so doctor
never exits 5.

| Check | Looks at | Skips when |
|---|---|---|
| `git` | git on `PATH`, and `.worktrees/` gitignored | never |
| `host` | the host a round would run on: `work.dispatch` as named, or with it unset or `subagent`, herdr inside a herdr pane, else tmux inside tmux. herdr on `PATH` and 0.9.x, or tmux on `PATH` | no host is detected (solo needs none) |
| `tracker` | `gh` or `glab` on `PATH` and authenticated, for the project's provider | the project has no remote |
| `accounts` | every account in `work.accounts` has an existing `configDir` with a credentials file | no accounts configured |
| `hook` | herdr's agent integration for each account (`herdr integration status`) | the host is not herdr, or no account is configured |
| `statusline` | the effective statusline runs `rota statusline dump` | hooks not installed (opt-in) |
| `stop-hook` | a `Stop` and a `SessionStart` entry marked `# rota-hook`, and the command resolves | hooks not installed (opt-in) |
| `switch` | with `orchestrator.switchOnUsage` on: the Stop hook and two accounts with a `configDir` | the key is off |
| `skills` | every installed skills root (user and project, Claude and Codex) matches the binary's skill set, and has no missing or edited files | no root has a `.rota-manifest.json` (run `rota skills install`) |
| `codex` | `codex` version in the supported range, each slot home logged in, herdr integration per home | `codex` is not on `PATH` and no slot has a home |

The hooks are opt-in, so `statusline` and `stop-hook` skip until `rota hook install` has written
something, and fail only on a partial or broken install. `skills` is opt-in the same way: it skips until `rota skills install` has written a manifest. `switch` cannot tell whether the orchestrator
runs under `rota keepalive run`. See [unattended rounds](unattended-rounds.md). For `codex`, see
[Codex workers](codex-workers.md).

Run it before `rota round start`, and again after changing accounts, hooks or the host.

## rota reap

Rounds leave things behind: merged branches, worktrees no slot owns, tabs with no agent in them, a
lease whose orchestrator died. `rota reap` finds them.

```sh
rota reap                          # preview: list candidates, delete nothing
rota reap --apply                  # delete the candidates that hold no work
rota reap --kind branch,lease      # only these kinds
```

The default is a preview (`warning: preview only; pass --apply`). `--apply` needs no `--confirm`,
because everything it deletes is proven unowned and merged, and anything holding work is skipped.

| Kind | A candidate is | Deletable when |
|---|---|---|
| `worktree` | a `.worktrees/<name>` checkout with no live owner, or one git marks prunable | it holds no work |
| `branch` | a local `<agent>/<issue>-slug` branch checked out nowhere and owned by no registered slot | it is reachable from the base or `origin/<base>`, proven from git alone |
| `tab` | a herdr tab inside `.worktrees/` whose panes run only plain shells | always (tmux has no agent state, so it yields none) |
| `process` | a process under a tab with no agent | a real host lists none; only test fakes supply any |
| `lease` | the round lease, when its holder is gone | the staleness is proven again under the lease lock |

**What reap never removes.**

- A running agent. A tab or process with a live agent under it is not listed.
- Work. A candidate with uncommitted changes, commits not on the base, or a branch not reachable from
  the base is listed with `held: <why>` and never deleted. There is no flag to override that.
- A `stalled` slot. Reap reclaims `dead` slots only: a worker in a long test run makes no commits and
  looks stalled, and reaping it would kill it. Reclaiming a stalled slot is an explicit
  [`rota round reclaim`](parallel-rounds.md#moving-an-issue-that-is-assigned).
- A parked slot. A `park/<agent>` worktree, clean or not, and any `park/*` branch are never candidates.
- A live or foreign lease. Only a stale one, whose holder is gone on this host.

**Exit codes.** `0` after a preview or a clean apply. `1` under `--apply` when a deletion failed (the
rest were still removed; the failures are in the output). `2` for an unknown `--kind`. `3` with no
project root. `5` when git fails.

Reap and [`rota round reconcile`](parallel-rounds.md#moving-an-issue-that-is-assigned) split the work:
reconcile reports drift between the registry, host, git and forge and repairs only the safe kinds;
reap deletes. After [`rota round wind-down`](parallel-rounds.md#winding-down), which deletes no branch,
run `rota reap`. It is not the verb for a registered slot: `rota worker pool reap` deregisters a named
slot, and removes its worktree and branch whether or not it holds work.
