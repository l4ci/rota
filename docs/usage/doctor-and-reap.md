# Doctor and reap

Two verbs bracket a [round](parallel-rounds.md). `rota doctor` checks the machine before it starts.
`rota reap` clears what the round left behind after it ends.

## rota doctor

```sh
rota doctor            # one line per check: status, name, detail
rota doctor --json     # {"ok": true, "data": {"ok": bool, "checks": [...]}}
```

It is read-only. It never writes, never calls a usage endpoint (so it spends no quota) and runs
without `.rota/`, falling back to default config. Each line is `pass`, `fail`, `skip` or `warn`. Every `fail`
carries a hint: the one command or edit that fixes it.

**Exit codes.** `0` when every check passes, skips or warns. `1` when any check fails; `--json` still prints
the full result, so a caller reads `data.ok`. A missing tool is a failed check, not an error, so doctor
never exits 5.

| Check | Looks at | Skips when |
|---|---|---|
| `git` | git on `PATH`, and `.worktrees/` gitignored | never |
| `jq` | `jq` on `PATH`; the skills read fields from `rota … --json` output with it | never |
| `host` | the host a round would run on: `work.dispatch` as named, or with it unset or `subagent`, herdr inside a herdr pane, else tmux inside tmux. herdr on `PATH` and 0.9.x, or tmux on `PATH` | no host is detected (solo needs none) |
| `tracker` | `gh` or `glab` on `PATH` and authenticated, for the provider `origin` names (else `issues.provider`) | `origin` names neither GitHub nor GitLab, and `issues.provider` is not set |
| `accounts` | every account in `work.accounts` has an existing `configDir` with a credentials file | no accounts configured |
| `hook` | herdr's agent integration for each account (`herdr integration status`) | the host is not herdr, or no account is configured |
| `statusline` | the effective statusline runs `rota statusline dump` | hooks not installed (opt-in) |
| `stop-hook` | a `Stop` and a `SessionStart` entry marked `# rota-hook`, and the command resolves | hooks not installed (opt-in) |
| `switch` | with `orchestrator.switchOnUsage` on: the Stop hook and two accounts with a `configDir` | the key is off |
| `skills` | every installed skills root (user and project, Claude and Codex) matches the binary's skill set, and has no missing or edited files | no root has a `.rota-manifest.json` (run `rota skills install`) |
| `codex` | `codex` runs, the default Codex home (or each `work.codexAccounts` account) logged in, herdr integration per home | `codex` is not on `PATH` and no `work.codexAccounts` are configured |

`verify` also appears only when something is wrong: it warns when `test.full` and `test.e2e` are both empty under a local verify (`test.fullWhere` is `local`), because the merge gate refuses to merge then (`blockedBy: no-verify`). The hint is `run: rota config set test.full '[...]'`. It never fails the run. `rota init` and `rota round start` print the same warning.

`agents` also appears only when something is wrong: it warns when a generated subagent file (`.claude/agents/rota-*.md`, and `.codex/agents/rota-*.toml` when Codex is configured) is missing or no longer matches the `roles.*` config. The hint is `run: rota agents write`. It never fails the run. See [`roles` keys](configuration.md#roles-keys-and-rota-agents-write).

`binary` also appears only when something is wrong. Run from a checkout of rota's own source (go.mod module `github.com/l4ci/rota`), doctor warns when the installed binary's commit is not the checkout's HEAD and HEAD changed non-test Go files under `cmd/` or `internal/` since it. The hint prints a rebuild command that keeps `Version` equal to `VERSION`, so the `skills` check still passes. `rota round start` prints the same warning, and `rota round watch` repeats it once per round. Anywhere else, nothing changes.

`ports` appears only when something is wrong. For each live slot that holds a port block (see [`work.portBase`](configuration.md#workportbase-and-workportblock-a-port-range-per-worker-slot)), doctor lists the machine's TCP listeners with `ss -ltnp`, else `lsof`, and prints `warn ports` when one inside the block belongs to a process whose working directory is outside that slot's worktree, or whose owner cannot be read. It never fails the run; the hint names the config key that moves the range. Where `/proc` is absent (macOS) the process directory comes from `lsof`. When neither tool is installed or both fail, doctor prints `skip ports` saying the blocks were not checked.

`disk` is the one line that appears only when something is wrong. When the free share of the volume holding the project (else the working directory) is under `doctor.minFreeDiskPercent` (default 10; `0` turns it off), doctor prints `warn disk`, which never fails the run, and its hint names what rota left behind that would give space back: temp dirs a smoke or gate run leaked (`rota-smoke.*`, `rota-gate-logs-*`, older than an hour, in the temp root) and git worktrees whose directory is gone. `rota reap` lists the rest of the stale scratch worktrees. See [`doctor.minFreeDiskPercent`](configuration.md#doctorminfreediskpercent).

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

**Merged branches of adopted work.** A slot adopted with
[`rota worker adopt`](parallel-rounds.md#adopting-work-another-tool-started) is released after its PR
merges, and rota keeps the worktree and branch because another tool made them. When that branch is
merged but still checked out in a worktree outside `.worktrees/`, reap lists it as a `branch` with
`held: checked out at <path>; remove that worktree first`. Remove the worktree yourself (or let the
other tool do it), then `rota reap --apply` deletes the branch. `rota worker gate --prune` and
`rota round wind-down --prune` do both at release time.

**Exit codes.** `0` after a preview or a clean apply. `1` under `--apply` when a deletion failed (the
rest were still removed; the failures are in the output). `2` for an unknown `--kind`. `3` with no
project root. `5` when git fails.

Reap and [`rota round reconcile`](parallel-rounds.md#moving-an-issue-that-is-assigned) split the work:
reconcile reports drift between the registry, host, git and forge and repairs only the safe kinds;
reap deletes. After [`rota round wind-down`](parallel-rounds.md#winding-down), which deletes no branch,
run `rota reap`. It is not the verb for a registered slot: `rota worker pool reap` deregisters a named
slot, and removes its worktree and branch whether or not it holds work.
