# Configuration

All settings live in [`.rota/config.json`](../reference/rota-folder.md). Run `rota config show` to list every key with its value and source, `rota config edit` (or `rota config --ui`) to change keys on [a screen](#the-config-screen) (terminal only; also the Config entry of the bare-`rota` menu), and `rota config set <key> <value>` to change one from a script (positional arguments). `rota init` fills any missing required project key with its default (see [Scopes](#scopes-which-file-a-key-belongs-in)). Don't hand-edit the file. `rota config save-global` merges this project's `config.json` keys (not `config.local.json`; without `rota.version`, `git.baseBranch` and `umbrella.enabled`) into `config.json` in the rota config dir, keeping the global keys the project does not set (see [the global config dir](#the-global-config-dir)), beside `connections.json`. The global file is a runtime layer: values resolve as schema default < global < `.rota/config.json` < `.rota/config.local.json`, so a value set once applies to every project that does not set that key, and `rota config show` reports `global` as its source (a key the project overrides also carries the global value in `--json`, as `global`). `rota config show --global`, `set --global` and `edit --global` read and write the global file itself, from any directory and with no project; in your home directory (no `.rota/` there or above) they do so without `--global`, and `config set` says `global:` in its output. A project-owned key is refused there. Bare `rota` in your home directory is a launcher whose Config entry opens the global config. On the config screen, `g` switches a key between inheriting the global value and overriding it in this project. The project-owned keys (`rota.version`, `git.baseBranch`, `umbrella.enabled`) are never read from it. A missing file changes nothing; a corrupt one warns and is skipped (every write to it, `set --global`, `save-global` and setup's save offer included, refuses with exit 70 and leaves the file as it is), and `rota config fill` then writes no defaults (so they are not pinned over the repaired global) while `rota config check` names the corrupt global. `rota init` and `rota setup` do not copy global values into the new `config.json`; the project inherits them, and `rota setup` offers each as the question's default. Interactive `rota setup` ends by offering to save its answers there: all of them when no global file exists yet, later only those that differ from it, merged into the file. `--yes`, `--set` and runs without a terminal never ask; `rota setup --save-global` saves the same answers without asking.

Allowed values and option labels of each key are in [Configuration options](../reference/config-options.md).

Default config (what `rota init --no-blocks` writes in a fresh repo; `rota.version` is stamped with the release that wrote it):

```json
{
  "learn": {
    "promoteThreshold": 3
  },
  "docs": {
    "path": "docs"
  },
  "git": {
    "baseBranch": ""
  },
  "umbrella": {
    "enabled": false
  },
  "rota": {
    "version": ""
  },
  "issues": {
    "autoCreateLabel": true,
    "source": "file"
  },
  "test": {
    "fast": [],
    "full": [],
    "e2e": []
  }
}
```

Every other key reads from the global config, else its default, until you set it.

## Scopes: which file a key belongs in

Each key has a scope, which `rota config show` prints beside the source (`(source: default, scope: machine)`, and `scope` in `--json`). The [options reference](../reference/config-options.md) lists it per key.

| Scope | What it describes | `rota config set` writes it to | `rota init`, `setup`, `fill` |
|-------|-------------------|--------------------------------|------------------------------|
| `project` | the repo: issues, tests, forge, base branch, release, shared paths | `.rota/config.json` (tracked) | write the required ones |
| `machine` | one developer's machine: accounts, dispatch, slots, worker commands, harnesses, tiers, models, ports, the orchestrator and limits keys | `.rota/config.local.json` (this machine only) | never |
| `taste` | a working preference: isolation, merge strategy, autonomy, review, QA | `.rota/config.json`, as a deliberate team pin | never at the default |

`rota config set` picks the file from the scope and says so: a machine key prints `local: <key> = <value> (.rota/config.local.json: a machine key, this machine only; ...)`. `--local` sends any key to `config.local.json`, `--global` to the global config, and `--project` to `.rota/config.json` (on a machine key it warns, after the write, that the tracked value shadows every developer's global config). `rota config show --local` lists `config.local.json`'s own values. On the config screen, Enter edits a machine key in `config.local.json`.

`rota setup` writes a project answer to `.rota/config.json`, a machine answer to `config.local.json`, and a taste answer to `.rota/config.json` only when you typed or `--set` it and it differs from what the project would inherit. A suggestion you accept, such as `worktree` because `.worktrees/` exists, is not written, so `rota setup --yes` pins no taste key. `rota config check` warns, without failing, about a machine key in `.rota/config.json` and about a taste key pinned there at its default, and fails on a key the schema does not know.

### Moving keys to their layer

A config written before scopes can hold machine keys in the tracked file, where they shadow the global config (an empty `round.accounts` hides the global accounts). `rota migrate config` lists what it would change, with each key's effective value before and after, and writes nothing:

- a machine key that `config.local.json` already holds is deleted (the local value wins);
- a machine key equal to the global value or to its default is deleted;
- any other machine key moves to `config.local.json` with its value;
- a taste key pinned at its default is deleted.

`rota migrate config --apply` writes exactly that, `config.local.json` first (created when missing, holding only the moved keys), then `.rota/config.json`; a second run finds nothing. `--keep <key>` (repeatable, or comma-separated) leaves a key where it is.

### Folding the account keys

`round.accounts` holds every worker account in one list of `{ "kind", "name", "dir" }` entries. `round.codexAccounts` (`{name, codexHome}`) and `round.opencodeAccounts` (`{name, configDir}`) are removed keys: `rota config check` names them as removed, and `rota config fill` migrates them, then deletes them. A Claude entry written the old way, `{ "name", "configDir" }` with no `kind`, still reads as `kind: "claude"`.

`rota migrate config` runs an accounts step after the `work.*` rename and before the scope step. It previews unless `--apply`. It rewrites the global config, `.rota/config.json` and `.rota/config.local.json`, lowest layer first: it folds `round.codexAccounts` and `round.opencodeAccounts` into `round.accounts` and rewrites legacy Claude entries to the new shape. A kind that a file defines nothing for is copied from the layers below it, so no layer's accounts vanish. If one file holds two entries with the same name and different dirs, it refuses with exit 4 (`blockedBy: "account conflict"`, `data.conflicts` lists the file, name and dirs) and writes nothing. Fix the names by hand, then run it again. Its `--json` data has an `accounts` list of `{file, from, entries}`.

Worker slots cached under the old keys keep working: the registry stores the account name and its expanded dir, and neither changes.

Keys split by flow. `work.*` keys apply to `/rota-work` and other solo cycles: `work.isolation`, `work.mergeStrategy`, `work.tdd` and `work.contextGuard`. `round.*` keys apply only to parallel rounds (`rota round`, `rota worker`, `rota orchestrate`). The round-only keys that used to sit under `work.*` now live under `round.*`; see [Moving old `work.*` names](#moving-old-work-names-to-round).

## Models: orchestrator.model and round.tiers.claude.standard

`orchestrator.model` (default `opus`) drives planning, exploration, verification and design. `round.tiers.claude.standard` (default `sonnet`) is the model of implementation subagents and of standard-tier round workers; `/rota-work` and rounds read the same key. Set them independently to trade quality against cost and speed. `round.profile` fills the Claude tiers when `round.tiers.claude.standard` is unset (see [round keys](#round-keys)).

| Value | Best for |
|-------|----------|
| `"opus"` | Deep reasoning: planning, exploration, verification, design |
| `"sonnet"` | Fast execution of well-specified tasks |
| `"haiku"` | Quick, cheap fixes and small tasks |

## work.isolation: branch or worktree

Affects `/rota-work` (solo). Sets where skill work happens.

| Mode | How it works | When to use |
|------|-------------|-------------|
| `"branch"` | Feature branch in current worktree | Solo work, simple workflows |
| `"worktree"` | Isolated directory under `.claude/worktrees/` | Parallel work streams, keep main clean while agents work |

Use `"worktree"` to keep several work streams in flight without context bleeding between them. See [parallel work](parallel-work.md).

## work.mergeStrategy: direct or pr

Affects `/rota-work` and `/rota-ship` (solo). Sets how [`/rota-ship`](review-and-ship.md) integrates completed work.

| Strategy | How it works | When to use |
|----------|-------------|-------------|
| `"direct"` | Merge to main, delete branch | Solo work, fast iteration |
| `"pr"` | Push branch, open a PR (GitHub) or MR (GitLab) | Team work, code review required |

With `issues.source` set to `github` or `gitlab` this key is ignored: `/rota-work`, `/rota-debug` and `/rota-ship` always open a PR or MR and never merge. A local forge (no `origin` remote, or `forge.provider: local`) lands `direct` whatever this key says. The routing lives in one place, `ship.Strategy` in `internal/ship`, and `rota ship strategy` prints its answer. See [issue backend](issue-backend.md).

## round.dispatch: subagent, tmux or herdr

Affects rounds only. `/rota-work` always runs in-process `Agent` workers that write files while the orchestrator commits, whatever this key says. Standing workers in tmux windows or herdr tabs (each its own Claude Code session in its own worktree, opening a PR behind a merge gate) belong to [`/rota-orchestrate`](parallel-rounds.md); this key only chooses their host.

| Mode | Effect on a round |
|------|-------------------|
| `"subagent"` (default) | `rota round start` detects the host: herdr inside a herdr pane, tmux inside tmux, otherwise solo. |
| `"tmux"` | One tmux window per worker. Needs a `tmux` binary and a working `claude` on `PATH`. |
| `"herdr"` | One herdr tab per worker in the orchestrator's workspace; herdr reports each worker's state, notifies when one needs you, and rota titles each tab `<slot> · <task> · <state>`. The orchestrator must run inside a herdr pane. |

```bash
rota config set round.dispatch tmux
```

**A round in herdr needs no setting.** With `round.dispatch` unset or `subagent` (the default `rota init` writes), `rota round start` detects the host: herdr inside a herdr pane, tmux inside tmux, and otherwise **solo mode**, where the orchestrator runs each worker as a Claude `Agent` subagent in the slot's own worktree. An explicit `tmux` or `herdr` is used as set, and fails if unavailable rather than falling back. The round records its host when it starts and keeps it until wind-down. Solo workers share the orchestrator's account, rate window and context, so one usage limit stops them all. Solo mode runs Claude workers only: a Codex subagent cannot be given a working directory. See [Parallel rounds](parallel-rounds.md).

Two things behave differently under `tmux` and `herdr` than in `/rota-work`:

- **`work.isolation` stops applying.** Every slot has its own worktree, so its own git index, by construction.
- **Workers commit.** The orchestrator's per-task commit step is skipped; integration happens through the merge gate instead, which re-verifies the *merged* tree. Two workers can each be green and still break the cycle branch together (one widens a signature while another adds a caller, one stops emitting a constant while another starts reading it), and a clean merge does not rule that out. So the gate runs `test.full` after every merge rather than trusting the branches.

Related keys: `round.slots` (pool size, default `3`), `round.commands` (the launch command per harness: `round.commands.claude` defaults to `claude --model <round.tiers.claude.standard> --dangerously-skip-permissions`, `round.commands.opencode` to `opencode --model <model>`, `round.commands.codex` to `codex --model <model> --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen`), `orchestrator.command` (starts the orchestrator for `rota orchestrate` and `rota worker session ensure`; the default builds `claude --model <orchestrator.model> --permission-mode auto`, with `--continue` added for `session ensure`; `rota keepalive run` restarts it), and `round.accounts` (the named worker accounts of every harness, a list of `{ "kind", "name", "dir" }`: `kind` is `claude`, `codex` or `opencode`, and `dir` is that harness's config home, `CLAUDE_CONFIG_DIR`, `CODEX_HOME` or `OPENCODE_CONFIG_DIR`; a leading `~` or `~/` in `dir` expands to the home directory; names are unique across kinds; with no entry of a kind, workers of that kind use the harness's default home).

**Workers run with permissions skipped; the operator does not.** A worker is briefed to commit, open a PR and run tests with nobody in its pane to answer a prompt, so a narrower mode just stalls it. What bounds a worker is scope, not gating: a throwaway branch in its own worktree, with `rota worker gate` verifying a scratch merge before anything reaches the cycle branch. The operator keeps `auto` because it performs the merges and is the window a human watches. Narrow either via its config key; a worker that then stops on a prompt reports `NEEDS-PERMISSION` rather than hanging.

## brainstorm.edgeProbe

Opt-in, `false` by default. With `true`, Step 3 adds an edge and prohibition probe to the one draft: it lists boundary, empty, ordering, idempotency, concurrency, encoding, precision and adjacency, records each as `covered: <how>` or `dismissed: <reason>`, asks once "What must this never do?" and writes each answer as a `- [ ] AC-n:` line that states a prohibition. It adds no approval step and is skipped when the issue body already has `## Acceptance`. `/rota-plan` and `--grill` are unaffected.

```bash
rota config set brainstorm.edgeProbe true
```

## research.packageCheck

Opt-in, `false` by default (no skill makes a registry call). With `true`, [`/rota-spike`](spikes.md) Step 2 and [`/rota-plan`](vision-and-plans.md) Step 2 look up each new package they would recommend on its registry (npm, PyPI, crates.io, Go proxy), so it makes network calls. A package that is not on the registry is `missing` and is not recommended. One first published under 90 days ago, or with no source repository link, is `suspect`: it is listed with its reason and appears in a plan or finding only after you say yes. Dependencies already in the repo are not checked. The verdicts are defined in `skills/references/package-check.md`.

```bash
rota config set research.packageCheck true
```

## refactor.confirmBeforeExecute

Applies to `/rota-refactor --fix` only; the default findings run files issues and writes no code. When `true` (default), `--fix` confirms the list of candidates before implementing them. Set to `false` to fix without that pause.

## round.itemTimeoutMinutes: wall-clock cap per item

Affects rounds only. Integer 0 or more, silent default `0` (off). Counted per item from its first assignment, not per slot, so handing the item to another worker does not reset it. Once an item is past the limit, `rota round reconcile` reports an `item-timeout` finding; `rota round reconcile --apply` parks the item for a human the way `round.maxBounces` does: the slot is freed, the issue gets the `needs-human` label and a comment naming the limit, and the PR stays open. Parking needs the round lease.

```bash
rota config set round.itemTimeoutMinutes 240
```

## round.envSetup: install dependencies in new worker worktrees

Affects rounds only. Free text, silent default `""` (nothing runs). `rota worker pool init` runs it once in each new slot worktree, with that worktree as the working directory, so a JS or Python worker does not start in a tree with no dependencies. It is skipped while the hash of the command and the lockfiles (`package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `uv.lock`, `poetry.lock`, `requirements*.txt`, `go.sum`) matches the one stored by the slot's last successful run, so re-running `pool init` is cheap and a changed lockfile or command reruns it. The hash sits in the worktree's git dir, outside tracked files. A non-zero exit stops `pool init` with exit 1 and a message naming the slot and the command; no hash is stored, so the next `pool init` retries.

```bash
rota config set round.envSetup "npm ci"
```

## round.sharedDirs: share dependency directories across worker worktrees

Affects rounds only. List of paths relative to the repo root, silent default `[]` (nothing shared). `rota worker pool init` symlinks each from the main checkout into a new slot worktree and adds it to the repo's `info/exclude`, so the link does not show in `git status`. `rota round assign` re-applies it on a task dispatch into a parked slot: a link that vanished is made again, and a slot whose branch changed the lockfile (any of `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `uv.lock`, `poetry.lock`, `requirements*.txt`, `go.sum`, `Cargo.lock`, `bun.lockb`) stops sharing. Sharing is all or nothing per slot: when a lockfile differs from main's, a directory is missing in main or the slot holds a real one, rota removes the links it made and runs `round.envSetup` so the slot installs its own, and with sharing on `envSetup` runs only then. Both verbs report what happened in a `shared` array in `--json` (`slot`, `dir`, `action` of `linked`, `kept`, `unlinked` or `skipped`, and a `reason` for the last two). Go needs no entry: `GOMODCACHE` and `GOCACHE` are per user. For cargo, set `CARGO_TARGET_DIR` instead of linking `target/`.

```bash
rota config set round.sharedDirs '["node_modules"]'
```

## round.portBase and round.portBlock: a port range per worker slot

Affects rounds only. Integers, silent defaults `20000` and `100`. Each slot reserves one block of `round.portBlock` ports starting at the lowest range from `round.portBase` that no other slot's range overlaps; `pool init` records it as `portBase` and `portBlock` on the slot in `.rota/workers.json`. The slot keeps its block until `rota worker pool reap` or `rota round reclaim` drops it (the next dispatch picks a new one), and two live slots never share a port. A range that would pass 65535 is refused. Every slot's agent, and `round.envSetup` in its worktree, get four variables:

| Variable | Value |
|---|---|
| `ROTA_SLOT` | the slot name, for example `dana` |
| `ROTA_PORT_BASE` | the first port of the slot's block |
| `ROTA_PORT_BLOCK` | how many ports the block holds: the width the slot was allocated with |
| `ROTA_DB_SUFFIX` | `_` plus the slot name with anything but letters and digits turned into `_` |

A worker starts servers on `ROTA_PORT_BASE` up to `ROTA_PORT_BASE + ROTA_PORT_BLOCK - 1` and names databases `<name>${ROTA_DB_SUFFIX}`. rota does not rewrite a project's test suites to read them. `rota doctor` warns when a process outside a live slot's worktree listens inside its block. Changing `round.portBase` or `round.portBlock` moves blocks only for slots created afterwards; a registered slot keeps the one it has, with the width it was allocated with, and later slots are placed around it.

```bash
rota config set round.portBase 30000
rota config set round.portBlock 200
```

## round.herdrGroupSlots: slots as linked herdr workspaces

Affects rounds only. `true` (default): dispatch opens the slot's worktree as a herdr workspace linked to the orchestrator's (`herdr worktree open`), so herdr groups the slots under their project in the sidebar. This helps when several projects share one herdr session. Workers sit in their own workspaces instead of beside the orchestrator, and each slot costs one extra workspace while it runs. `false`: under herdr, each worker slot is a tab in the orchestrator's own workspace. It changes nothing under tmux. The worktrees stay in `.worktrees/`.

`rota layout split` folds the slots' panes into the orchestrator's tab, so they leave their workspaces (herdr closes a workspace with its last tab); the command says so. `rota layout tabs` opens each slot's workspace again and moves its pane back in. With the key `false`, layout is unchanged.

Closing the orchestrator's workspace by hand needs `herdr workspace close --group`, which also closes every slot's workspace; rota never passes `--group`. See [`herdr-dispatch.md`](../../skills/rota-orchestrate/references/herdr-dispatch.md#linked-workspaces-per-slot).

```bash
rota config set round.herdrGroupSlots false
```

## round.claimStaleMinutes: remote claim activity

`round.claimStaleMinutes` defaults to `240` for bugs, tasks and Minor/Cosmetic features.
`round.claimStaleMinutesMajor` defaults to `480` for Major features. Both are project keys;
`0` disables stale detection for that class.

A remote claim is stale only when its claim time, open PR activity and origin branch tip
are all older than the threshold. Unknown claim times and failed activity reads do not
permit reclaim. `round status`, `round candidates` and `doctor` show the holder and last
activity. Reclaim requires maintainer approval followed by
`rota round assign <ID> --reclaim-stale <claim-id>`; see [parallel rounds](parallel-rounds.md).

## work.tdd: red-first on or off

Affects `/rota-work` (solo) and the worker contract. `true` (default): a behavior change needs a recorded red run, a FAIL proof row from a test that failed on an assertion, before its PASS row. A FAIL whose evidence is a build, compile or setup failure (missing import, typo, undefined symbol) is not a red run; the task gets a fix dispatch. `false`: `/rota-work` Step 7 and the worker contract require no RED row. PASS rows are still recorded.

```bash
rota config set work.tdd false
```

## plan keys

| Key | Default | Meaning |
| --- | --- | --- |
| `plan.maxTasks` | `3` | Tasks a plan may hold before `rota plan check` warns and suggests splitting it into dependent items. `0` turns the warning off. |
| `plan.criticHarness` | unset | Harness that runs the level-4 `/rota-plan` critic, read-only: `claude`, `codex` or `opencode`. Unset: the planning harness's own subagent. A missing binary or failed run falls back to that subagent with one printed line. Levels 1-3 never read it. `rota config set` refuses other values; `rota config set plan.criticHarness ""` clears it. |
| `plan.maxFilesPerTask` | `5` | Paths a task's `Files:` line may name before `rota plan check` warns about that task. `0` turns the warning off. |

The two limits are advisory warnings: the check still exits 0. Both take an integer of 0 or more, and `rota config set` refuses anything else.

```bash
rota config set plan.maxTasks 5
```

## work.contextGuard: warn or pause a long session

Affects `/rota-work` and other solo sessions. `warn` (default), `auto` or `off`. Acts only where `rota hook install` has run, on a session that is not the orchestrator, when its fresh statusline reading is at or above `orchestrator.handoffThreshold`. `warn` adds one line to the next prompt recommending `/rota-pause`, once per crossing. `auto` also lets the Stop hook block up to 2 times, asking for `/rota-pause` at the next task boundary. See [pausing and resuming](pausing-and-resuming.md#context-guard).

```bash
rota config set work.contextGuard auto
```

## Checking whether tests depend on a change

`test.vacuousCheck` defaults to `false`. Set it to `true` to make `rota worker gate`
and `rota worker train` check whether added or changed tests fail without their
non-test changes. The settings come from the base checkout, so a PR cannot disable
its own check.

```sh
rota config set test.vacuousCheck true
```

`test.filePattern` is a regular expression over repository-relative paths. Its
Go default, `_test\.go$`, selects Go test files. `test.vacuousCommand` defaults to
`go test -json -count=1 {packages} -run {tests}`: it runs test, example and fuzz entry
points in the changed test files, including their subtests, with caching disabled.
`{packages}` expands to those files' Go packages and `{tests}` to an anchored
expression of their entry-point names. `{files}` expands to the changed test paths.
Every expansion is shell-quoted; leave the placeholders unquoted in the template.
The command must prepare any dependencies needed in a fresh checkout and disable
its test cache.
For Python, for example:

```sh
rota config set test.filePattern '(^|/)(test_[^/]+|[^/]+_test)\.py$'
rota config set test.vacuousCommand 'python -m pytest {files}'
```

After normal verification passes, the check makes a disposable worktree of the
merge. The selected command must pass there. It then restores the merge's changed
non-test files from the preceding base, keeping the test changes, and runs the
same command again. If it still passes, the gate refuses with exit 4 and
`blockedBy: vacuous-tests`, naming the test files. A nonzero test exit passes this
check; this establishes dependence on the change, not assertion quality. A
cancelled run or a counterfactual command that cannot execute is `check-broke`.

To resolve `vacuous-tests`, add an assertion that fails with the non-test changes
reverted, then re-run the gate or train. For refactors or tests-only work, disable
`test.vacuousCheck` in the base configuration or use `--no-verify` to skip this
check for that gate or train run.

A tests-only PR skips the check because it has no non-test changes to revert.
A PR with no surviving added or changed test files, only docs or skills, or no
runnable Go entry points also gets a `VACUOUS-SKIP` note. With the default Go
command, JSON events must show at least one selected test completing without a
skip on each successful run. If build constraints exclude all selected tests or
they all skip on either run, the check reports an inconclusive `VACUOUS-SKIP`
instead of refusing the PR. Custom commands supply their own test selection and
execution semantics. Tests that already fail on the merge are `verify-failed`.
The train checks each member against its preceding merged prefix, so an earlier
member's code stays present.
A failing member refuses the whole train, including with `--land-green`; the
full-tier cache never substitutes for this check. The check runs locally even
with `test.fullWhere: ci`. Gate `--check-only` and a train's landing gates do not
run it; train `--plan` also skips it. Both gate and train `--no-verify` skip this
check.

## test.fast, test.full, test.e2e

Three tiers of shell commands, each an array defaulting to `[]`:

- `test.fast`: quick checks for a task or worker to run while working.
- `test.full`: the full suite. The merge gate (`rota worker gate`) and the merge train run it on the merged tree, and [`/rota-refactor`](../skills/rota-refactor.md) runs it in `--fix` verification as CI-shape gates before committing. Empty means read-only verification: the gate and the train refuse to merge (exit 4, `blockedBy: no-verify`) unless `test.e2e` is set or you pass `--no-verify`, which merges and reports `NO-VERIFY`.
- `test.e2e`: slow end-to-end checks. `rota worker gate` and the merge train run it on the merged tree after `test.full` passes, so a round pays for it at merge instead of once per branch (a train runs it once for all members). A red train run bisects like a red `test.full`. Empty skips the step.

### The exclusion ledger

A known-red or flaky test may be passed over by the merge gate and the merge train only through `.rota/test-ledger.json`, a tracked file you create by hand when the first entry is needed (a missing file or `[]` means no exclusions):

```json
[{"test": "TestFlaky", "owner": "dana", "receipt": "#378", "expires": "2026-11-01"}]
```

- `test`: the top-level Go test name, as `go test` prints it on its `--- FAIL: <name>` line. Its subtests are covered by it.
- `owner`: who fixes it. `receipt`: the issue or PR tracking the fix. `expires`: `YYYY-MM-DD` (valid through the end of that day, UTC) or an RFC 3339 time. All four are required.

`rota worker gate` and `rota worker train` read the file from the base before merging. A `test.full` or `test.e2e` command that fails still passes the verify when every test it names has an unexpired entry; the result lists them under `excluded`. A failing test with no entry fails as before, and so does a failure the output does not name (a build error, a panic, a timeout) or output that does not end in go test's own `FAIL` line (a later step of the same command failed). Once an entry expires the gate and the train fail with `verify-failed` and nothing lands, naming the test, owner and receipt, whether or not the test still fails: fix the test, or renew the entry in a reviewed commit. A malformed entry is exit 2. `rota test ledger check` reports expired and malformed entries without running anything. The ledger applies to the local run, not to `test.fullWhere ci`, where CI decides.

### test.isolate

`rota test run <tier>` isolates the environment of every command by default (`test.isolate`, bool, default `true`): `HERDR_*`, `TMUX*`, `SSH_AUTH_SOCK` and `SSH_AGENT_PID` are removed, and `HOME` and the `XDG_*` dirs point into a temp root that is deleted when the run ends. The Go caches (`GOCACHE`, `GOMODCACHE`, `GOPATH`, `GOENV`) keep their real locations, so an isolated run does not rebuild from cold. A project whose tests need the real home or an ssh-agent opts out with `rota config set test.isolate false`. It applies to `rota test run` only, not to the merge gate or train.

`test.full` replaces `refactor.verifyCommands`. `rota config fill` moves the old key's commands to `test.full` and deletes it. Until then the gate still reads the old key when `test.full` is empty, and warns on stderr that it is deprecated.

When `test.full` is non-empty, the verifier executes each command in order and refuses to PASS unless every command exits zero. This catches formatter drift, import-sort failures and type errors locally instead of on push. See [rota #9](https://github.com/l4ci/rota/issues/9) for the motivating incident.

Example for a Python project using ruff + pytest:

```json
"test": {
  "full": [
    "uv run ruff check .",
    "uv run ruff format --check .",
    "uv run pytest -q"
  ]
}
```

Commands run from the repo root (or, in umbrella mode, the sub-repo's root), one after the other. To use several cores, put the concurrency in one command: this repo sets the list to `["bash test/gate.sh"]`, which runs validate, `go vet`, `go test -race` and the smoke suite together (smoke split into [`ROTA_SMOKE_SHARDS`](#smoke-shards) shards, the sharded gate takes about 2 to 3 minutes, serial smoke is several times slower). Set via `rota config set` (which parses argv[2] as JSON):

```bash
rota config set test.full '["uv run ruff check .","uv run ruff format --check ."]'
```

### Running the full tier on CI

`test.fullWhere` is `local` (default) or `ci`. With `ci`, `rota worker gate` and `rota worker train` do not run `test.full` here. They build the merge result (base plus PR head, `--no-ff`) in a scratch worktree, push it to `origin` as `rota/ci/<slot>` (`rota/ci/train` for a train, one push per bisect step), and poll the forge's checks on that commit: GitHub check suites, check runs and commit statuses, GitLab the jobs of the newest pipeline for the ref. The branch is deleted after each run. `test.ciTimeoutMinutes` (default `60`, 1-1440) bounds the wait.

`ci` needs `test.ciChecks`, the names of the checks that must pass:

```json
{
  "test": {
    "fullWhere": "ci",
    "ciChecks": ["test", "lint"]
  }
}
```

or `rota config set test.fullWhere ci` and `rota config set test.ciChecks '["test","lint"]'`. `rota init` does not write it. With `ci` and an empty list, the gate and the train refuse up front (exit 70, the hint names `test.ciChecks`) and never fall back to a local run. The list is read from the base's config before the merge, so a branch cannot change its own required checks.

Only a green result merges: every listed check finished with success and no check on the commit failed, listed or not. Green must hold over two polls in a row, since a later workflow or job can appear after the first ones finish. Unlisted checks still running do not hold the gate up; an unlisted check that fails makes it red. A listed check that finishes skipped or neutral counts as failed, because it tested nothing. If several checks share a name, all of them must succeed.

A GitHub Actions check is named after its job (`test`, or `test (ubuntu-latest)` for a matrix job), without the workflow name the PR page puts in front of it; a commit status is named by its context. `gh api repos/{owner}/{repo}/commits/<sha>/check-runs --jq '.check_runs[].name'` prints the exact names for a commit. On GitLab, list job names. GitLab also reports each pipeline as `pipeline #<id> (<ref>)`, and a failed pipeline makes the run red. A failed job with `allow_failure: true` counts as skipped, and so does a `manual` job that was not run.

This moves verification to CI. A red result lands nothing and the slot goes back (`verify-failed`, the first red check ends the wait); a local gate also verifies before the merge, on a scratch merge. Before merging, the gate checks the base did not move (`base-moved`, nothing landed); after merging, that the landed tree is the tree CI verified. `--no-verify` skips CI too.

The project's CI must run on pushes to `rota/ci/**`. If a listed check never appears on the pushed commit, the verdict is `ci-not-run` and nothing lands; that is the preflight. It is decided once the 5 minute start window has passed and no check on the commit is still pending, so a job gated behind `needs:` that shows up late is waited for. The message names the missing checks. If CI is still running when `test.ciTimeoutMinutes` runs out with a listed check missing or pending, the verdict is `verify-timeout`, and it names them. A missing `origin` remote is `check-broke`.

CI runs the definition in the pushed tree, so a merge that changes it would choose its own verification. A merge (or any train member) that touches `.github/workflows/`, `.github/actions/`, `.gitlab-ci.yml` or `.gitlab/ci/` is refused as `ci-config-changed` before anything is pushed; review the CI change and land it with `local` or by hand. A pipeline file kept elsewhere is not recognised.

- GitHub Actions: add the branch pattern to the workflow, next to what is already there.

  ```yaml
  on:
    push:
      branches: ['rota/ci/**']
  ```

- GitLab: pipelines run on branch pushes unless `workflow:rules` exclude them. Make sure `rota/ci/` branches are allowed.

Trade-off: a CI queue can be slower than a 2-3 minute local gate. `ci` saves local or VPS CPU and avoids running the suite twice.

Not covered: the `rota round wind-down` base re-verify and `rota test run full` still run `test.full` locally. Tuning, in seconds: `ROTA_CI_POLL` (default 20), `ROTA_CI_START_WAIT` (300), `ROTA_CI_TIMEOUT` (overrides the config timeout).

## Smoke shards

`bash test/gate.sh` splits the smoke suite into concurrent shards: `ROTA_SMOKE_SHARDS=<N>` (integer >= 1) for one run, else one per two cores (1 to 4). The old `gate.smokeShards` key is removed. Each shard is its own `test/runner.sh` run with its own temp root and its own log (the gate prints the log directory, and keeps it on a failure). A shard that ends without a summary line is reported with its exit code and last output. Shards only help while sections stay independent: `bash test/gate.sh --smoke-only --random` deals the sections out at random, and a scheduled CI job runs it on `main` to catch a section that needs another's state. Only one gate runs per machine at a time; a second waits on `/tmp/rota-gate.lock`.

```bash
ROTA_SMOKE_SHARDS=6 bash test/gate.sh
```

## doctor.minFreeDiskPercent

Free-disk threshold for `rota doctor`, as a percent of the volume holding the project. Integer 0-100, default `10`; `0` turns the check off. Below it doctor adds a `warn disk` line (the run still passes) whose hint names the rota leftovers that would give space back: leaked temp dirs and worktrees whose directory is gone. The same threshold gates a `warn go-cache-disk` line when the volume holding the Go build cache is that low; its hint is `go clean -cache`. A parallel round writes worktrees, builds and logs, and a full disk fails it midway.

```bash
rota config set doctor.minFreeDiskPercent 5
```

## learn.verify

Whether [`/rota-learn`](learning.md) always runs a second-opinion pass on what it just wrote. The verifier is a fresh Opus subagent with no session context that reads only the updated `KNOWLEDGE.md` diff. It judges each new bullet on four criteria: durable (not ephemeral), sharp (a concrete claim), correctly topic'd, and non-duplicate. It can demote weak entries, sharpen vague wording, re-file wrong-topic bullets, or delete restatements of existing knowledge.

| Value | Behavior |
|-------|----------|
| `true` | After writing, dispatches the verifier, catching weak, duplicate or wrong-topic entries before they accrete in `KNOWLEDGE.md`. Adds one Opus roundtrip per `/rota-learn` call. |
| `false` (default) | Skip the verifier. `/rota-learn` writes and reports. Pass `--strict` to `/rota-learn` to run the verifier for one call anyway. |

A weak bullet consulted by 20 future `/rota-work` runs costs more than one extra Opus call. Set `true` to check every capture, or use `--strict` when one matters.

See [learning](learning.md) for the full `/rota-learn` workflow.

## learn.promoteThreshold

The F03 knowledge promotion lifecycle: how many `rota knowledge hit` events a `provisional` bullet must accumulate before auto-promotion to `confirmed`. Integer ≥ 0; default `3`.

A "hit" registers when `/rota-work` or `/rota-review` consumes the bullet during a cycle's K+D consult (the bullet shows up in a worker brief's `Known gotchas:` section) AND the user doesn't push back with a correction that overlaps the bullet's body. At the threshold the bullet becomes `confirmed`: `rota knowledge query` drops the `(provisional)` suffix and consumers see it as established knowledge.

| Value | Behavior |
|-------|----------|
| `3` (default) | Auto-promote at the third clean hit. |
| `≥4` | Stricter. Use when weak bullets reach `confirmed` too quickly. |
| `1` or `2` | Looser. Almost every new bullet auto-promotes on its first or second consult. Use when the manual `/rota-learn --promote` flow feels heavy. |
| `0` | Auto-promote on `rota knowledge add` itself, which disables the `provisional` tier. |

Pending contradictions block auto-promotion regardless of hit count. The user must resolve them via `/rota-learn` Step 9 (Demote / Keep / Defer) before the bullet can flow forward.

See [learning](learning.md) for the full `/rota-learn` workflow.

## ship.review

How deep a review `/rota-ship` runs before integrating.

| Value | Behavior |
|-------|----------|
| `"full"` (default) | `/rota-ship` runs `/rota-review` (Spec and Standards reviewers) before integrating. FAIL blocks, CONCERNS ask, PASS flows through. |
| `"light"` | `/rota-review` dispatches the Spec reviewer only, on the `standard` tier, and skips Standards (the deterministic gate covers its mechanical half). A branch that references no issue has no spec and gets the Standards reviewer instead. |
| `"none"` | `/rota-ship` integrates directly without a review pass. Use when you already reviewed manually. |
| `true` / `false` | The old boolean, still read: `true` is `full`, `false` is `none`. |
| object | Picks the depth per branch, see below. |

```json
{"ship": {"review": {"default": "full", "lightBelow": 50, "labels": {"risk:high": "full", "best-of:2": "light", "partial-slice": "none"}, "paths": {"migrations/**": "full"}, "supportPaths": ["docs/**", "internal/*/testdata/golden/**"]}}}
```

- `default`: `full`, `light` or `none`; `full` when omitted.
- `lightBelow`: a diff of fewer changed lines (added plus deleted, against the base) than this gets `light`. It only lowers a `default` of `full`. `0` or omitted turns it off.
- `labels`: a label on the item's issue or PR maps to a depth and beats the size rule, so `risk:high` forces `full` on a small diff. `best-of:2` and `partial-slice` are ordinary labels here. When several labels match, the strictest depth wins. A label that maps to `none` is final: the rules below never lift it.
- `paths`: a glob maps (`*` within a segment, or a trailing `/**` for a whole directory; other `**` is rejected) to the least depth a changed path matching it gets; the strictest match wins and it never lowers a depth.
- `scopeDrift`: `true` when omitted. A diff that touches files the issue never named (its `## Files` entries and the path entries of its `## Touches`) resolves `full`, and the reason lists them. A directory entry (`internal/gate`, with or without `/` or `/**`) covers the files under it. Support files do not count as drift: a `_test.go` file in the directory of a declared file or under a declared directory, and anything matching `supportPaths`. An issue that declares neither section has no set to drift from. `false` turns the rule off.
- `supportPaths`: globs (same syntax as `paths`) of files that never count as scope drift. Omitted, it is `docs/**` (the generated `docs/reference/config-options.md` included), `cmd/rota/testdata/frozen/**`, `internal/*/testdata/golden/**` and `test/sections/**`. A list you set replaces these; `[]` makes every undeclared file count.
- A PR body line `Door: one-way` (the line every worker PR ends with) resolves `full` whatever the size.

Door and scope drift only lift a `light` review; a `default` of `none` is the review switched off and they leave it. `rota review depth --json` names the deciding signal in `signal`: `label`, `door`, `scope-drift`, `path`, `size` or `default`.

`rota review depth [branch] [--labels a,b]` prints the depth and why. It reads the issue behind a `<agent>/<issue>-<slug>` branch itself (its labels, and its files for scope drift) and the branch's open PR for the Door line, when the depth could be light; `--labels` adds to the labels. A branch with no PR yet has no Door line to read. `/rota-ship` prints it, and `rota worker gate` applies it: the branch must have the verdicts the depth needs on record at its current head (`full` both, `light` the Standards one, `none` none; a verdict from an older commit is stale and does not count, as in `/rota-review`) or the gate refuses with `blockedBy: review-missing`. `rota config check` fails on a malformed policy.

See [review and ship](review-and-ship.md) for the full `/rota-ship` workflow.

## ship.secondOpinion

Whether `/rota-ship` runs a second adversarial review with a fresh subagent after `/rota-review` passes. `/rota-review` shares the project's context (conventions, KNOWLEDGE, plan) with the work it reviews, which can hide blind spots. A fresh subagent with only the diff and the goal reasons from scratch and catches what the contextualized reviewer normalized.

| Value | Behavior |
|-------|----------|
| `false` (default) | `/rota-ship` skips the gate. `/rota-review` alone gates merges. |
| `true` | After `/rota-review` returns PASS, `/rota-ship` dispatches a fresh `general-purpose` Sonnet subagent with the goal + diff only (no KNOWLEDGE/DECISIONS/conventions) and an adversarial framing. Returns PASS/CONCERNS/FAIL via the same routing as `/rota-review`. |

The gate adds one Sonnet roundtrip per ship, and most cycles don't need it. Enable it for work that touches load-bearing surfaces (release tooling, security paths, data migrations).

The reviewer is the same model with fresh context. Cross-model second opinions (Codex, Gemini) are not wired.

See [review and ship](review-and-ship.md) for the full `/rota-ship` workflow.

## ship.qa

Whether `/rota-ship` invokes [`/rota-qa run`](qa.md) between `/rota-review` (and the optional second-opinion gate) and the merge/PR step. `/rota-review` asks whether the diff makes sense; `/rota-qa` asks whether the product works, by executing the per-target strategy in `.rota/qa/<target>.md`.

| Value | Behavior |
|-------|----------|
| `false` (default) | `/rota-ship` skips QA. Diff-level review alone gates merges. |
| `true` | After `/rota-review` (and second-opinion if on), `/rota-ship` calls `/rota-qa run` scoped to the active repo. QA findings route per `qa.gate`. If no strategy file exists for the active scope, `/rota-ship` surfaces a one-line note pointing at `/rota-qa first-run` and proceeds without QA. |

Product QA needs strategy files (`/rota-qa first-run` bootstraps them) and often infra (dev server, sandbox creds, installed runners), and most cycles don't need it. Enable it for repos with a QA strategy wired up, where regressions cost more than the runner time.

## ship.uat

Whether `/rota-ship` runs [`/rota-qa uat`](qa.md#uat-human-acceptance) for each item on the branch, after review and before the merge/PR step.

| Value | Behavior |
|-------|----------|
| `false` (default) | `/rota-ship` skips the walk-through. `/rota-qa uat <ID>` still works when you call it. |
| `true` | `/rota-ship` walks each item's acceptance with you: criteria with a PASS test proof row auto-pass, the rest (and every `(human)` criterion) are asked one at a time. A failure is captured as a linked item and does not block the ship. |

It is never part of the round merge gate: `rota worker gate` and the round merge path behave the same with the key on, so round PRs stay unattended.

## ship.mergeApproval and ship.mergeApprovalPaths

Whether a merge needs a human. The merge verbs (`rota ship merge`, `rota ship pr-merge`, `rota worker gate`) read it, so it binds `/rota-ship`, `/rota-review --queue` and the `/rota-work` gate step at every `autonomy.level`. In a round, `rota worker gate` enforces it; see [parallel rounds](parallel-rounds.md).

| `ship.mergeApproval` | Behavior |
|-------|----------|
| `"none"` (default) | Merges run after their own gates (review, proof, verify) with no extra approval. |
| `"all"` | Every merge needs a human. The verb refuses with exit 4 until the skill asks and re-runs it with `--confirm --confirm-note "<answer>"`. |
| `"paths"` | Only merges that change a file matching `ship.mergeApprovalPaths` need a human. The refusal lists the matching files. |

`ship.mergeApprovalPaths` is a list of repo-relative entries. A changed file matches an entry when it equals it, lies under it (`"skills/rota-release"` matches `skills/rota-release/SKILL.md`), or matches it as a glob against the whole path (`"*.md"` matches top-level Markdown only). Set them with `rota config set ship.mergeApproval paths` and `rota config set ship.mergeApprovalPaths '["migrations", "*.lock"]'`. Every approval lands in `.rota/gate-audit.jsonl`; see [`references/manual-gates.md`](../../skills/references/manual-gates.md).

## Machine pins and the retired support label

With `round.support` enabled, main is named `main` and the support machine takes the name
in `round.support`. `machine:<name>` pins an issue to that machine. Unpinned issues are
available to both machines. Support prefers its pinned issues, then unpinned work within
its configured `round.scope`. The shared claim decides which machine takes an issue.
Candidates show an issue pinned elsewhere as blocked, and assignment refuses it. `round.role: support` requires a
nonempty `round.support`; `main` is reserved for the main machine.

`rota round support offer <ID>` adds `machine:<round.support>`; `--withdraw` removes it.
An offer refuses an issue pinned only to another machine until that pin is removed.
The support machine hands an issue back with `rota round support return`.

To upgrade existing `site:support` issues, run `rota migrate labels` on main to preview,
then `rota migrate labels --apply`. The migration reads the configured `issues.labels.support`
value when present, otherwise `site:support`, and relabels only open issues. A conflicting
machine pin leaves that issue untouched and keeps the old key; tracker errors also keep
it, so applying again resumes. Only a fully successful apply removes the old key from
`.rota/config.json`. `rota config check` reports the deprecation; `rota config fill` keeps
the value until migration finishes. The migration removes the key only from the tracked
config; a value in `.rota/config.local.json` remains and is still reported by `config check`.
Without `round.support`, apply can still retire the tracked key when no open issue uses
the legacy label. Closed issues keep their labels. Until migration, start, candidates and tick warn about
legacy-labelled issues and keep them blocked so main cannot assign them as unpinned work.

## round keys

Settings for `rota round` (parallel rounds; see [the rounds guide](parallel-rounds.md), [`rota round` verbs](../reference/cli-helpers.md#rota-round) and [`rota doctor`](../reference/preflight.md#rota-doctor)). All are silent defaults; none is written by `rota init`.

| Key | Default | Meaning |
|-----|---------|---------|
| `round.scope` | `"milestone"` | Which issues `rota round assign` accepts. `"slate"`: only the issues given to `rota round start --items`. `"milestone"`: the open items of the active milestones. `"next"`: the same, then the first planned milestone whose dependencies are shipped once none of the active milestones has an item left to take. With no planned or active milestone at all, `"milestone"` and `"next"` offer every open item, as `"open"` does. `"open"`: every open item no slot holds. Assign refuses anything outside the scope. |
| `round.roster` | `["ben","dana","nia","kit"]` | Agent names, one slot each (`.worktrees/<agent>`, parked on `park/<agent>`, working on `<agent>/<issue>-<slug>`). Lowercase letters, digits and `-`; no duplicates. |
| `round.brief` | `""` | Path of the standing worker contract the assignment pointer names. Empty means `skills/references/worker-contract.md` in the project checkout, else the installed `rota-orchestrate/references/worker-contract.md`. |
| `round.sharedPaths` | `[]` | Repo-relative globs the file-overlap readiness check ignores, for files every issue touches (a command registry, a contract doc). |
| `round.scopeOverlap` | `warn` | What a clash on declared scopes does to the overlap readiness check. Scopes come from an issue's `## Touches` section, else its `Subsystem:` field. `warn` lists the clash and keeps the item ready; `block` fails the check like a shared file. `--accept-overlap` skips both. |
| `round.adoptPattern` | `""` | Glob over branch names, for work another tool starts (`codex/*`, `claude/*`; `*` stops at `/`). `rota round reconcile` reports a matching local or remote branch that no slot holds and that is not merged into the base as `unregistered-branch`. `--apply` adopts those whose name carries an issue number (`<agent>/<issue>-<slug>`, `issue-N` or `#N`) as an external slot, after the overlap check; a blocking overlap becomes a warning and the branch stays listed. The rest are listed with `needs --issue`. Empty turns the check off. |
| `round.stallMinutes` | `30` | Minutes without a commit, an uncommitted edit or a state change before `rota round reconcile` reports a slot that holds an issue and has a live agent as `stalled`. `0` turns the check off. A slot waiting on an escalation is never stalled; a dead agent is `dead`, not stalled. A relay whose `[relay K]` line never renders is given up on after this many minutes, or after a fixed 30 when this is `0`. |
| `round.maxBounces` | `3` | How often `rota worker gate` may send one item's PR back to its worker (a `stale` or `provenance-fail` verdict on a real run, never `--check-only`) before it parks the item: the slot is freed, the issue gets the `needs-human` label and a comment, and the PR stays open. The count is per item and resets on a pass or a park. `rota round bounce` adds the orchestrator's own bounces to it and refuses at the cap. `0` turns the cap off. |
| `round.ledgerKeep` | `0` | Rounds of the round ledger (`.rota/ledger.jsonl`) to keep. `rota round start` on a new round drops the entries of rounds older than the last N; a renewed start trims nothing. Entries with no round (solo, no lease) stay. `0` keeps everything. |
| `round.architectureEvery` | `20` | Closed non-refactor items between automatic architecture reviews; `0` turns them off. A review also fires when a slot is idle and nothing is assignable, rate-limited by the two keys below. See `rota round architecture`. |
| `round.architectureIdleMinItems` | `5` | Closed non-refactor items since the last review the queue-empty trigger needs; `0` drops the condition. A slate round never fires queue-empty. |
| `round.architectureIdleMinHours` | `24` | Hours since the last review before queue-empty may fire again; `0` drops the condition. The floor applies only where a review time is recorded (issue mode); in file mode there is none, so it never holds a review back. A first look that seeds the review time starts the clock at that moment. |
| `round.architectureAreas` | `[]` | The areas an architecture review is split into, one review item each. Empty means the subsystem map's names, else one whole-repo review. |
| `round.autopilotCap` | `3` | The most assigns, and the most merges, one autopilot tick does. `0` means the default. The autopilot runs only under `autonomy.level: auto`; see [Autopilot](parallel-rounds.md#autopilot). |
| `round.batch` | `0` | The most items a round may have assigned and not yet merged, closed or parked at once. `rota round assign` refuses the next one (exit 4, `blockedBy: batch`; `--accept-batch` overrides one call) and `rota round tick` assigns none (`batchFull`). `0` means no limit. See [Batch](parallel-rounds.md#assigning-an-issue). |
| `round.freshAt` | `2` | The bounce at which `rota round review-relay` stops resuming the worker and instead runs `round transfer` to a free slot at the same tier, with a brief of the findings still NOT ADDRESSED in the last review plus the review link (no relay history). The transfer counts as that bounce and is ledgered with `--cap-reason fresh`. With no free slot, no open findings or a refused transfer the relay goes to the same worker and says why. Only acts below `round.maxBounces`. `0` turns it off. |
| `round.reviewLoop` | `"manual"` | What happens when a reviewer comments on a finished worker's PR. `manual` reports it (`review/<slot>` change in `rota round watch`, a `review` item in `rota round tick`) and leaves `rota round review-relay <slot>` to the orchestrator. `auto` relays it to the worker as a counted bounce from `round watch` and `round tick`; at `round.maxBounces` it escalates on the PR instead. See [Review loop](parallel-rounds.md#review-loop). |
| `round.harnesses` | `[]` | Harnesses a round may use, `claude`, `codex` or `opencode`, in quota-fallback order. The first entry is the default worker harness. Order: `--kind`, the issue's `harness:` label, the first entry, the slot's recorded kind, else `claude`. A slot resuming its own issue keeps its kind ahead of the first entry (`kindSource` `slot (resume)`). Autopilot, `round architecture` and `round transfer` follow it, so a Codex-first project (`["codex"]`) needs no flag or label. Any other value is a config error. `rota round status` and `assign --check-only` show where the kind came from (`kindSource`). `rota setup` asks for the worker harness and for `orchestrator.harness`; Enter leaves each unset. |
| `round.commands` | `{}` | Launch command per harness kind (`claude`, `codex`, `opencode`), set one at a time: `rota config set round.commands.codex '...'`. Unset kinds use the built-in command. See below. |
| `round.tier` | `"standard"` | Default worker tier: `light` (reading, searching), `standard` (code and tests) or `heavy` (hard reasoning). `rota round assign --tier heavy --tier-reason "…"` goes above it; a tier above the default needs the reason, which lands on the slot. |
| `round.profile` | unset | A named model profile that fills the three Claude tiers (light / standard / heavy): `quality` sonnet / opus / opus, `balanced` haiku / sonnet / opus, `budget` haiku / haiku / sonnet, `inherit` empty / empty / empty (Claude workers and subagents launch without `--model`). Unset keeps the tier defaults below. Precedence per Claude tier: an explicit `round.tiers.claude.<tier>`, then the profile, then the default. Codex and OpenCode tiers are not touched. `rota config show` prints each tier's effective model and its source (`explicit`, `profile`, `default`, or `harness default` when empty), for Claude, Codex and OpenCode. |
| `round.tiers.claude.light` / `.standard` / `.heavy` | `haiku` / `sonnet` / `opus` | The model each tier starts a Claude worker with. `standard` is also the model of `/rota-work`'s implementation subagents, so the two agree. |
| `round.tiers.codex.light` / `.standard` / `.heavy` | `gpt-6-luna` / `gpt-6.1-sol` / `gpt-6-astra` | The same for Codex, lighter to heavier. Set all three to empty and a Codex worker runs on Codex's own default model (the default `round.commands.codex` drops `--model`). Overriding one tier leaves the other two on their defaults; setting one tier empty beside a model on another is refused. `assign --kind codex --check-only` shows the model it would use. |
| `round.tiers.opencode.light` / `.standard` / `.heavy` | `opencode/claude-haiku-5-5` / `opencode/claude-sonnet-5-5` / `opencode/claude-opus-5-5` | The same for OpenCode, with `provider/model` ids from `opencode models`; the defaults need the built-in `opencode` provider, so set your own ids when `opencode models` does not list them. Set all three to empty and OpenCode picks its own model (the default `round.commands.opencode` drops `--model`). See [OpenCode workers](opencode-workers.md). |

A custom `round.commands.claude` receives the tier's model only through a `{model}` placeholder in the command; without one, `rota round assign` warns and records the tier but not a model. `round.commands.codex` and `round.commands.opencode` work the same way for Codex and OpenCode workers. `round.commands` is one key holding one command per harness kind; set a single entry with `rota config set round.commands.codex '...'`. An unknown kind under it is an error.

`autonomy.level: auto` turns on the round autopilot; `round.scope` says which issues a round may take. Set with `rota config set round.scope slate`.

## Moving old `work.*` names to `round.*`

Fourteen keys moved out of `work.*` into `round.*` because only rounds read them. Old names are no longer read, and there is no fallback.

| Old name | New name |
|----------|----------|
| `work.workerSlots` | `round.slots` |
| `work.dispatch` | `round.dispatch` |
| `work.workerCommand` | `round.commands.claude` |
| `work.operatorCommand` | `orchestrator.command` |
| `work.accounts` | `round.accounts` |
| `work.codexAccounts` | `round.codexAccounts`, then folded into `round.accounts` (see [Folding the account keys](#folding-the-account-keys)) |
| `work.codexCommand` | `round.commands.codex` |
| `work.opencodeAccounts` | `round.opencodeAccounts`, then folded into `round.accounts` |
| `work.opencodeCommand` | `round.commands.opencode` |
| `work.envSetup` | `round.envSetup` |
| `work.portBase` | `round.portBase` |
| `work.portBlock` | `round.portBlock` |
| `work.herdrGroupSlots` | `round.herdrGroupSlots` |
| `work.itemTimeoutMinutes` | `round.itemTimeoutMinutes` |

`work.isolation`, `work.mergeStrategy`, `work.tdd` and `work.contextGuard` stay under `work`.

`rota migrate config` shows what it would change and writes nothing. `rota migrate config --apply` moves each old name to its new name in both `.rota/config.json` and `.rota/config.local.json`, with the value unchanged. If an old name and its new name are both set to different values, it refuses with exit 4 and writes nothing. Resolve that pair by hand with `rota config set`, then run it again. `rota config fill` and `rota init` run the same migration. The same verb then runs the accounts step from [Folding the account keys](#folding-the-account-keys) and the scope step from [Moving keys to their layer](#moving-keys-to-their-layer).

While a config still holds an old name, these verbs exit 4 with `blockedBy: config migration` and list the old keys: `rota round ...`, `rota worker ...`, `rota doctor`, `rota orchestrate`, `rota keepalive`, `rota layout`, `rota limit` and bare `rota`. Other verbs only print a warning. `rota config check` lists an old name as `unknown`.

```bash
rota migrate config
rota migrate config --apply
```

### Slot count and draining

`rota round start` uses exactly the first N names of `round.roster`, where N is `--slots` or `round.slots`. A registered slot beyond N stays registered and parked. If it still holds work (an item, a claim or a queued PR), it stays in the round marked draining. A draining slot gets no new assignment until its work lands or is transferred.

## roles keys and rota agents write

`rota agents write` emits one subagent definition per tier role, so a worker can hand work to a subagent by name instead of by model. The roles are fixed: `explorer` (light tier: reading, searching), `implementer` (standard: code and tests) and `reasoner` (heavy: design, hard debugging). All keys are silent defaults; `rota init` does not write them. It does run `rota agents write` itself (best effort, skipped with `--no-blocks`), so the agent files exist after init.

| Key | Default | Meaning |
|-----|---------|---------|
| `roles.explorer.tier` | `"light"` | The tier whose model the role runs on: `light`, `standard` or `heavy`. The model is `round.tiers.<kind>.<tier>`; there is no per-role model key. |
| `roles.implementer.tier` | `"standard"` | The same for the implementer. |
| `roles.reasoner.tier` | `"heavy"` | The same for the reasoner. |
| `roles.<role>.effort` | unset | Reasoning effort for the role. Claude accepts `low`, `medium`, `high`, `xhigh` or `max`. A Codex file takes `low`, `medium`, `high` or `xhigh` (`max` and `ultra` exist on some Codex models only), so a role with a bigger effort is refused (exit 4) once Codex is configured. Unset leaves the harness default. |

Each role prompt bounds the report the subagent hands back, because the worker re-reads it on every later turn. The explorer answers in at most 15 lines (`path:line: what`) then a `Not found:` line. The implementer answers in at most 15 lines (one `path: what changed` per file, the check it ran and its result) then a `Not done:` line. The reasoner answers in at most 20 lines (assumptions, alternatives, one recommendation) then a `Not checked:` line. Every role ends with one last-line token the caller branches on: `done.`, `too-big.` or `ambiguous.`. A brief that names its own closing tokens (the scope scout's `scoped.`, `too-vague.`, `no-match.`) overrides them. Every role quotes code, paths and errors verbatim. The `Not ...` line is `none` when nothing is missing.

```bash
rota agents write            # write or refresh the files
rota agents write --check    # report drift, write nothing, exit 1 on drift
```

It writes `.claude/agents/rota-<role>.md` (frontmatter `name`, `description`, `model`, `effort` when set, and `tools: Read, Grep, Glob, Bash` for the explorer only) and, when OpenCode is configured, `.opencode/agents/rota-<role>.md` (frontmatter `description`, `mode: subagent`, `model` when the tier map is set, and `permission: edit: deny, bash: deny` for the explorer; OpenCode has no effort field) and, when Codex is configured, `.codex/agents/rota-<role>.toml` (`name`, `description`, `model`, `model_reasoning_effort` when set, `developer_instructions`, and `sandbox_mode = "read-only"` for the explorer). Codex counts as configured when any `round.tiers.codex.*` is set, `round.commands.codex` is non-empty, `round.accounts` has a `codex` entry, or the first entry of `round.harnesses` is `codex`. With no Codex tier map the Codex files omit `model` and Codex picks its own. The explorer keeps `Bash`, so on Claude it is read-mostly, not strictly read-only.

Each file starts with a "generated by rota, do not edit" header. The verb never overwrites a same-named file without it. Commit the files like other project config. `rota init` runs the verb once (skipped under `--no-blocks`, and a refusal there only warns); `rota skills update` does not, so rerun it after changing a role or a tier map. `rota doctor` warns on a missing or drifted file and names the verb. The [worker contract](../../skills/references/worker-contract.md) tells workers to use these agents and to fall back to the tier table when the files are absent.

## orchestrator keys

Settings for the orchestrator handoff (`rota hook stop`, `rota hook session-start`; see [unattended rounds](unattended-rounds.md)) and its restart (`rota keepalive run`; see [unattended rounds](unattended-rounds.md)). All are silent defaults; none is written by `rota init`.

| Key | Default | Meaning |
|-----|---------|---------|
| `orchestrator.handoffThreshold` | `75` | Context percentage (integer, 1 to 100) at which the Stop hook blocks the orchestrator until it has written a handoff. |
| `orchestrator.model` | `opus` | Model of the orchestrator session and of planning, exploration and verification subagents. |
| `orchestrator.command` | unset | Command `rota orchestrate` and `rota worker session ensure` start for a Claude orchestrator. Unset builds `claude --model <orchestrator.model> --permission-mode auto`. |
| `orchestrator.restartPrompt` | `Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round.` | Appended as the last argument of a restart, never of the first start (non-empty string). |
| `orchestrator.harness` | `claude` | The agent `rota orchestrate` starts as the orchestrator: `claude`, `codex`, `hermes` or `opencode`. It runs under `rota keepalive run` with the orchestrate skill as its first prompt. See [parallel rounds](parallel-rounds.md#-launching-a-round) and [orchestrator harnesses](orchestrator-harnesses.md). |
| `orchestrator.switchOnUsage` | `false` | Opt in to moving the orchestrator to another account before a usage limit: the Stop hook then also blocks for a handoff at `usageThreshold`, and `rota keepalive run` restarts under another account (boolean). See [unattended rounds](unattended-rounds.md). |
| `orchestrator.usageThreshold` | `90` | Percent (integer, 1 to 100) of the 5-hour or weekly window at which the Stop hook asks for that handoff. Read only when `switchOnUsage` is `true`. |
| `orchestrator.escalateIssue` | `0` | Issue number, or a key like `PROJ-123` on a key-based tracker, the breaker's escalation comment goes on (integer or string). `0` or empty is unset: the breaker raises a host notification and a warning only. |

An out-of-range value exits 70 in a verb that reads it (`rota keepalive run` included); the hooks treat it as a pass and never block. The state age limit (120 s), handoff age limit (900 s), block cap (2), restart cap (10), breaker (3) and backoff (5 s) are fixed; see [Upgrading from the old keys](#upgrading-from-the-old-keys).

## limits keys

Settings for the usage-limit watcher (`rota limit watch`, and the loop inside `rota keepalive run`; see [unattended rounds](unattended-rounds.md)). All are silent defaults; none is written by `rota init`.

| Key | Default | Meaning |
|-----|---------|---------|
| `limits.mode` | `switch` | `switch` moves a limited worker's issue to an idle slot on another account with headroom, and sleeps when there is none. `sleep` always waits for the reset. The orchestrator always sleeps. |
| `limits.resumePrompt` | `The usage limit has reset. Continue where you left off.` | Typed into the limited pane at the reset (non-empty string). |

An out-of-range value exits 70 in a verb that reads it.

## map.staleCommits: warn when the map falls behind

Commits HEAD may be past a map file's `last_mapped_commit` before `rota map stats` warns. Default `50`; `0` turns the warning off. A map file without `last_mapped_commit` is never checked. The warning is advisory: `rota map stats` exits 0 either way. `rota config set` takes an integer of 0 or more. `/rota-refactor --map` stamps the field.

```bash
rota config set map.staleCommits 100
```

## map.softcap_subsystems: cap the project map

Subsystems the map may name before `rota decisions check --cap` and `/rota-refactor --map` nudge you to merge or retire stale entries. Default `20`. `rota config set` takes an integer of 1 or more.

```bash
rota config set map.softcap_subsystems 30
```

## qa.gate

How `/rota-ship` routes a `/rota-qa run` verdict when `ship.qa: true`. Independent of `/rota-review` verdict routing.

| Value | Behavior |
|-------|----------|
| `"advisory"` (default) | All verdicts surface findings (PASS silently, CONCERNS / FAIL with the `QA concerns:` carrier label) and continue to merge / PR. The ship is never blocked on QA. |
| `"blocking"` | PASS continues silently. CONCERNS asks (`AskUserQuestion` Address / Ship anyway / Stop) at every `autonomy.level`. FAIL stops the ship; user fixes via `/rota-work` or `/rota-debug` and reruns `/rota-ship`. |

`INFRA-FAIL` (dev server / creds / binary missing) is always treated as advisory regardless of `qa.gate`. Missing infrastructure is not a quality signal. The missing requirements surface as a note and the ship continues.

## qa.afterWork

Whether `/rota-work` invokes `/rota-qa run` post-cycle when touched files match a target's `Watch globs`.

| Value | Behavior |
|-------|----------|
| `false` (default) | `/rota-work` never invokes `/rota-qa`. QA only runs from `/rota-ship` (when `ship.qa: true`) or manual `/rota-qa run`. |
| `true` | After `/rota-work` finishes a cycle, if any touched file matches a `Watch globs` entry in a `.rota/qa/<target>.md` strategy, `/rota-qa run` fires scoped to that target. Verdict is advisory at this stage (the cycle is already complete) but findings surface for the next session. |

Turn this on only once strategies are stable and you want coverage on every cycle; otherwise the runner noise outweighs the value.

## autonomy.level

How far a run goes before it stops. Three levels: `"off"` (default), `"roundtrip"`, `"auto"`. `"auto"` also turns on the round autopilot. `"loop"` was removed; `rota config check` fails on it and on any other value. See [autonomy levels](autonomy.md) for when each level fires, which gates still apply, and how to pick.

## docs.path

- **Type:** string
- **Default:** `"docs"`

Relative path (from the project root) to the documentation folder that [`/rota-docs`](../skills/rota-docs.md) reads and writes. Set it when your project keeps docs elsewhere, for example `"documentation"`, `"site/content"` or `"wiki"`.

```json
{ "docs": { "path": "documentation" } }
```

## docs.autoCreate

- **Type:** boolean
- **Default:** `false`

Whether `/rota-docs` after-work mode writes proposed doc updates without pausing for approval. When `false`, the after-work flow proposes changes and waits for your confirmation. When `true`, it skips the approval gate and commits the doc updates directly.

| Value | Behavior |
|-------|----------|
| `false` (default) | After-work mode proposes updates and waits for approval before writing. |
| `true` | After-work mode commits doc updates without the approval gate. Assumes you trust the agent's judgment on doc prose; review the `git diff` per cycle. |

## docs.afterWork

- **Type:** boolean
- **Default:** `false`

Gate for the after-work docs flow. When `true`, the skills [`/rota-work`](running-work.md), `/rota-ship`, and [`/rota-release`](../skills/rota-release.md) trigger the docs after-work flow after their primary action completes. `/rota-work` and `/rota-ship` only fire on cycles that resolve 2+ items or touch 5+ files (small fixes don't trigger); `/rota-release` fires on every successful release (release notes are inherently user-facing). `/rota-work` only nudges, in a one-line terminal report, at either autonomy level. `/rota-ship` runs the after-work flow inline, with its approval gate as the checkpoint. `/rota-release` nudges under `autonomy.level: off` and auto-dispatches `/rota-docs` under `auto`.

```json
{ "docs": { "afterWork": true } }
```

Leave `false` while you shape docs by hand; turn it on once the structure is stable.

## release.checklistPath

- **Type:** string
- **Default:** `.rota/RELEASE.md`

Path to the project's release checklist: a flat markdown file with `- [ ]` items that `/rota-release` walks as gates before bumping the version (Step 1.5). Each open checkbox becomes an `AskUserQuestion` interjection: *Yes, continue* / *Fix now and continue* / *Skip* / *Abort*. Items marked `- [x]` are ignored. Items whose text ends with `(manual)` always interject, even under `autonomy.level: roundtrip` or `auto`; use them for sensitive gates (staging migrations, infra rollouts).

The file is per-project and tracked by default, so the checklist is shared with the team. When absent under `autonomy.level: off`, the skill offers to scaffold a starter template; under `roundtrip` or `auto`, the skill silently skips the gate rather than interrupt an unattended run. To keep the checklist per-contributor instead, add it to `.gitignore`.

No release step is hardcoded in the skill. Catch drift such as a forgotten sibling version file bump (a `marketplace.json` that went stale by two majors) by adding an item.

```json
{ "release": { "checklistPath": "docs/RELEASE-CHECKLIST.md" } }
```

## release.versionFile

- **Type:** string
- **Default:** empty (auto-detect)

Project-relative path of the version file. Empty means `/rota-release` and `rota release version` search `plugin.json`, `package.json`, `pyproject.toml`, `Cargo.toml` and `VERSION` in that order. Set it when a project has several, or keeps the version somewhere else. `rota config set` refuses absolute paths and paths that leave the project.

```json
{ "release": { "versionFile": "VERSION" } }
```

## release.confirmLargePushCommits

- **Type:** integer
- **Default:** `10`

Number of unpushed commits above which `/rota-release` asks for one confirmation before pushing, even under `autonomy.level: roundtrip` or `auto`. At or below it, those levels push the unpushed range silently as part of the release.

```json
{ "release": { "confirmLargePushCommits": 25 } }
```

Set it higher to suppress the prompt at your usual velocity, or lower (for example `5`) where every push matters.

## release.nudgeAfterCommits

- **Type:** integer
- **Default:** `10`

Number of commits since the last release tag at which [`/rota-work` (no argument)](picking-work.md) (terminal paths only) and `/rota-ship` (post-ship report) start surfacing a one-line nudge: *"<N> commits since <tag>; consider `/rota-release`."* Informational only; no skill is auto-invoked.

## release.nudgeAfterDays

- **Type:** integer
- **Default:** `14`

Companion to `release.nudgeAfterCommits`. The nudge fires when either threshold is reached. Raise one or both to nudge less, lower them to release more often.

## git.baseBranch

- **Type:** string
- **Default:** `""` (auto-detect)

Overrides the base branch that `rota git base` resolves to. When empty, `rota` auto-detects by probing `main`, `master`, `trunk`, then `origin/HEAD` in that order. A set branch that does not exist is an error (`rota git base` exits 3), never replaced by one of those names. Set it when your project uses a base branch auto-detection won't find, such as `develop` (gitflow) or `release`.

```json
{ "git": { "baseBranch": "develop" } }
```

Skills that use the base branch (`/rota-ship`, `/rota-review`, `/rota-work`) call `rota git base` and pick up this override.

## Issues backend keys

- **Keys:** `issues.source`, `forge.provider`, `issues.homeRepo`, `issues.labels.*`
- **Required:** no. Existing configs stay valid without them.
- **Legacy keys:** `backlog.backend` and `issues.provider` were replaced by `issues.source` and `forge.provider`. Run `rota migrate source --apply` (or `rota config fill`) to convert; until then the old keys keep deciding.

| Key | Default | Values |
|-----|---------|--------|
| `issues.source` | `"file"` | Where the issues live: `"file"`, `"github"`, `"gitlab"`, `"forge"`, `"jira"`, `"linear"` or `"itsaplan"`. `github` and `gitlab` put the backlog on the tracker and must match the forge; `forge` puts each sub-repo of an umbrella on its own forge (mixed hosts); see [issue backend](issue-backend.md). |
| `forge.provider` | `"auto"` | Where code lands: `"auto"` (from `origin`; no remote is `local`), `"github"`, `"gitlab"` or `"local"`. `local` means direct merge: no PR, the gate never looks for one, and `rota ship strategy` answers `direct`. |
| `issues.homeRepo` | `""` | Umbrella mode only: sub-repo holding milestone tracking issues. Empty means the first registered sub-repo. |
| `issues.connection` | `""` | Name of the machine-wide tracker connection (Jira, Linear, It's a Plan) added with `rota connect add`. A name only, never a secret. A teammate whose connection has another name overrides it in `config.local.json`. See [Tracker connections](#tracker-connections). |
| `issues.jira.project` | `""` | Jira project key the connection reads from and writes to. |
| `issues.linear.team` | `""` | Linear team key the connection reads from and writes to. |
| `issues.itsaplan.project` | `""` | It's a Plan project key, such as `MKT`, the connection reads from and writes to. |
| `issues.labels.inProgress` | `"in-progress"` | Label name. |
| `issues.labels.needsReview` | `"needs-review"` | Label name. |
| `issues.labels.changesRequested` | `"changes-requested"` | Label name. |
| `issues.labels.released` | `"released"` | Label name. |
| `issues.labels.notPlanned` | `"not-planned"` | Label name. |
| `issues.labels.blocked` | `"blocked"` | Label name set by `rota item complete --reason blocked`; the issue stays open. |
| `issues.labels.needsHuman` | `"needs-human"` | Label `rota round transfer --to human` puts on an issue handed to the human. `rota round candidates` skips an issue that carries it; removing the label puts it back in the set. Silent default, not written by `rota init`. |
| `issues.labels.milestoneTracker` | `"milestone-tracker"` | Label name. |
| `issues.labels.types.bug` / `.feature` / `.task` | `"type:bug"` / `"type:feature"` / `"type:task"` | Label names per item type. |
| `issues.labels.priorityPrefix` | `"p"` | Prefix for priority labels. |
| `issues.labels.sizePrefix` | `"size:"` | Prefix for feature size labels (`size:Major`). |

### Tracker connections

Credentials and sites belong to you, not the repo, so they never go in `.rota/`. `rota connect` keeps named connections in `connections.json` under the global rota config dir (`~/.config/rota`); a project only names one with `issues.connection`. GitHub and GitLab keep their own login through `gh` and `glab`; this is for trackers rota reaches over HTTP.

```sh
echo "$JIRA_TOKEN" | rota connect add work --kind jira --site https://acme.atlassian.net --user me@acme.com --token-stdin
echo "$ITSAPLAN_API_KEY" | rota connect add plan --kind itsaplan --site https://api.plan.example.com --token-stdin   # It's a Plan: --site is the api URL
rota connect list          # name, kind, site, user and which store holds the secret; never the secret
rota connect test work     # one authenticated read
rota connect remove work   # drops the connection and its secret
```

The secret goes to the OS keychain (`security` on macOS, `secret-tool` on Linux) and, where there is none (headless Linux), to a `0600` `secrets.json` in `$XDG_DATA_HOME/rota` (`~/.local/share/rota`), kept out of the config dir because that is often synced to a dotfiles repo. A `secrets.json` left beside `connections.json` by an older rota is moved there on first use, and rota forces it to `0600`. If that old file is a symlink (a dotfiles setup), rota copies the target and removes only the link, so the secret leaves the synced repo. Anything that is not a regular file is left alone. `rota connect add` says which store it used and why it fell back; `rota connect list` shows it per connection and prints the file store path. `ROTA_<NAME>_TOKEN` (the name upper-cased, other characters as `_`, so `work` reads `ROTA_WORK_TOKEN`) overrides the stored secret, for CI. `rota connect add` takes the token on stdin only, so it never reaches your shell history or a process list; on macOS the keychain write goes through `security -i`, again over stdin. Adding a name whose `ROTA_<NAME>_TOKEN` equals another connection's (`a-b` and `a_b`) is refused. A jira connection says whether the site is Jira Cloud or Data Center with `--deployment cloud|datacenter`. An `*.atlassian.net` site defaults to `cloud`, and any other site must say which. Cloud also needs `--user`, the account email the API token belongs to. OAuth is not supported: API tokens only.

`rota doctor` checks the project's `issues.connection`: it fails with a hint when the connection is unknown, has no secret, or its test read fails. A kind with no test read yet in your build shows as a warning.

`issues.label`, the legacy alias of `issues.labels.inProgress`, is removed; see [Upgrading from the old keys](#upgrading-from-the-old-keys). `rota config show` lists all of these keys with their effective value and source.

### Where rota keeps machine files

Outside a project, rota follows the XDG Base Directory spec. An `XDG_*` variable counts only when it is an absolute path.

| Kind | Location | Holds |
|---|---|---|
| Config | `$XDG_CONFIG_HOME/rota`, default `~/.config/rota` | `config.json` (the global config layer), its `config.version` stamp and `connections.json` |
| State | `$XDG_STATE_HOME/rota`, default `~/.local/state/rota` | `projects.json` (the project registry) and its lock. `rota doctor` prints the path. |
| Cache | `$XDG_CACHE_HOME/rota/skills` (the OS user cache dir) | the extracted embedded skills |
| Data | `$XDG_DATA_HOME/rota`, default `~/.local/share/rota` | the secrets fallback file (`secrets.json`), in a `0700` dir |

An older rota kept `projects.json` in the config dir. The first run after upgrading moves it to the state dir and removes the old copy; a registry already in the state dir wins.

`rota init` and `rota setup` leave projects in temporary storage out of this registry: paths under `$TMPDIR`, `/tmp` or macOS's `/var/folders` (including `/private/var/folders`), after resolving symlinks. Init still seeds the project and prints `project not registered: temporary directory`. `rota projects cleanup` removes older temp entries as well as missing projects and projects that no longer hold `.rota/`; it lists each removed entry and leaves the project files alone. Temporary removals have a `(temporary)` column in text output; JSON rows add `reason: "temporary"` or `reason: "stale"`. When nothing is removed, text output says `noop: no stale or temporary projects`. Plain `rota projects` keeps listing existing entries until you remove them.

## rota.version (auto-managed)

- **Type:** string
- **Default:** `""` (unstamped until `rota init` first runs)

The rota release (binary version) that `rota init` last ran with. `rota init` re-stamps it on every run, including STALE migrations. Don't edit by hand.

`rota version --drift` compares the stamped value with the installed `rota` binary, and [`rota init check`](../reference/preflight.md) surfaces the same drift as a warning. `--json` returns `stamped`, `installed` and `status` (`match`, `drift` or `unknown`).

There are no project files to refresh. A stamp written before the rename to rota is read as a fallback and moved to `rota.version` by `rota init` / `rota config fill`. This is *project drift* (`rota` was upgraded and the project was not re-stamped), distinct from `rota update`, which compares the installed binary with the latest GitHub release.

When `rota version --drift` reports drift, re-run `rota init` after an upgrade to clear it.

## Upgrading from the old keys

Some keys were removed, merged or renamed. `rota config check` names a leftover as removed, `rota config fill` deletes it, and `rota migrate config` moves the value of a merged or renamed key. The merges step runs after the accounts step and before the scope step. It previews unless `--apply`, and works on the global config, `.rota/config.json` and `.rota/config.local.json`, lowest layer first. Idempotent; emptied parent objects (`models`) are removed.

Merged and renamed (the value moves unchanged):

| Old key | New key |
|---------|---------|
| `models.orchestrator` | `orchestrator.model` |
| `round.operatorCommand` | `orchestrator.command` |
| `models.worker` | `round.tiers.claude.standard` |
| `round.workerCommand` | `round.commands.claude` |
| `round.codexCommand` | `round.commands.codex` |
| `round.opencodeCommand` | `round.commands.opencode` |
| `round.workerKind` | first entry of `round.harnesses` |

`round.workerKind` becomes the head of `round.harnesses`. When a file sets `workerKind` but not `harnesses`, the migration writes `harnesses = [workerKind, ...the harnesses of the layers below, minus workerKind]`. When both are set and agree, `workerKind` is dropped.

Two keys in one file that disagree are a conflict: `round.workerKind` against `round.harnesses[0]`, and `models.worker` against `round.tiers.claude.standard`. The verb writes nothing and exits 4, as the other steps do. Resolve the pair with `rota config set`, then run it again. `issues.label` is the one special case: it moves to `issues.labels.inProgress` when that key is unset in the same file, and is simply dropped when it is set (never a conflict).

Removed (no migration):

| Old key | What happened |
|---------|---------------|
| `ship.secondOpinionRunner` | The second-opinion gate always runs the subagent. A leftover `"codex"` no longer selects advisory mode. |
| `gate.smokeShards` | Use `ROTA_SMOKE_SHARDS` for one run; the default is one shard per two cores (1 to 4). |
| `issues.label` | Merged into `issues.labels.inProgress`, see above. |

Fixed constants (no longer configurable; a leftover value is ignored):

| Old key | Fixed value |
|---------|-------------|
| `orchestrator.stateMaxAgeSeconds` | 120 |
| `orchestrator.handoffMaxAgeSeconds` | 900 |
| `orchestrator.handoffMaxBlocks` | 2 |
| `orchestrator.keepaliveMaxRestarts` | 10 |
| `orchestrator.keepaliveBreaker` | 3 |
| `orchestrator.keepaliveBackoffSeconds` | 5 |
| `limits.resumeMarginSeconds` | 60 |
| `limits.fallbackSleepSeconds` | 1800 |
| `limits.maxResumes` | 3 |
| `issues.retryWaitSeconds` | 60 |
| `issues.bulkPaceMs` | 1000 |

## The config screen

`rota config edit`, `rota config --ui`, `rota config show --ui` and the Config entry of the bare-`rota` menu open the same screen. It needs a terminal that can take raw mode: off a terminal, with `--json` or under `TERM=dumb`, the verb refuses and points at `rota config set <key> <value>`. There is no line-prompt fallback.

Keys sit under their section (`orchestrator`, `work`, `ship`, `round`, ...). Each row reads `key  value  [source]`, where the source is `default`, `project` (`.rota/config.json`) or `local` (`.rota/config.local.json`). A value that differs from the schema default is highlighted, and the group header counts how many do. The pane on the right shows the key's description, type, default, current value, where it is set and, for enum keys, the choices.

| Key | Does |
| --- | --- |
| `↑` `↓` / `j` `k` | move; `PgUp` `PgDn` scroll the detail pane |
| `Enter` | on a key, edit it; on a header, fold or unfold the group (also `←` `→`) |
| `l` | edit the key and write it to `config.local.json` instead of `config.json` |
| `r` | reset the key to its default: drops the `config.local.json` entry and sets (a required key) or removes (any other) the `config.json` entry |
| `/` | filter the rows by any part of the row; `Enter` ends typing, `Esc` clears |
| `q` / `Esc` | back (`Esc` clears a filter first) |

While editing, a boolean is a toggle (`Space`), a key with choices is a picker (`←` `→`), and any other key is a text field. `Enter` saves and `Esc` cancels. The text is checked as `rota config set` checks it: JSON when it parses, else a string, and a project-relative path key such as `release.versionFile` refuses a path outside the project. Nothing else is checked, so a number key accepts text just as `config set` does. Every save is written at once, through the code behind `config set`, so leaving the screen never loses or half-applies a change. A key set in `config.local.json` still wins on this machine after a project edit; the screen says so.

## The global config dir

`connections.json` and the saved global `config.json` live in the global rota config dir. rota picks it in this order:

1. `ROTA_CONFIG_DIR`, when it is an absolute path. Use it for a separate rota profile, or to isolate rota alone in CI and tests without moving `XDG_CONFIG_HOME` for every other tool.
2. `$XDG_CONFIG_HOME/rota`, when `XDG_CONFIG_HOME` is an absolute path.
3. `~/.config/rota`.

A relative `ROTA_CONFIG_DIR` is ignored with a warning on stderr, and rota falls through to the next rule. `rota doctor` shows the `config dir` line: the dir in use and whether it came from the override, XDG or the default. The state and data dirs have no override: `ROTA_CONFIG_DIR` moves only the config dir, so CI isolation also needs `XDG_STATE_HOME` and `XDG_DATA_HOME` (the project registry lives in the state dir).
