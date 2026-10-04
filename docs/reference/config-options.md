# Configuration options

This page lists every config key with its allowed values. The options below are the five core settings (Q1-Q5) with their labels. For a concept-first walk through each key, see [`usage/configuration.md`](../usage/configuration.md).

There is no interactive config UI. Two verbs cover it:

- **`rota init`** writes `.rota/config.json` on first setup, fills any missing keys with the Recommended defaults on later runs, and stamps `rota.version`. It never overwrites a value you set.
- **`rota config show`** and **`rota config set`** read and change keys later.

The "(Recommended)" tag on each option marks the default `rota init` writes.

## rota config invocation shapes

`rota config` takes positional arguments:

| Shape | Behavior |
|-------|----------|
| `rota config show` | Prints every key, value and source layer: `local`, `project` or `default`. |
| `rota config show <key>` | Prints one key. |
| `rota config set <key> <value>` | Validates the value, writes `.rota/config.json` (never `.rota/config.local.json`), prints the one-line diff. |

Values for list and object keys are JSON (`rota config set work.accounts '[...]'`). An unknown key or an invalid value exits non-zero with the allowed values; nothing is written.

## Q1: Models

`header: "Models"`, single-select.

> *"Which model profile should rota use for orchestration and implementation?"*

| Label | Description |
|-------|-------------|
| Balanced: Opus + Sonnet (Recommended) | Opus plans and verifies, Sonnet executes. Strong reasoning where it matters; fast execution elsewhere. |
| Premium: Opus only | Opus for everything. Highest quality, highest cost. |
| Fast: Sonnet only | Sonnet for both roles. Faster and cheaper; fine for well-specified tasks. |
| Minimal: Sonnet + Haiku | Sonnet plans, Haiku executes. Cheapest. Best for mechanical, low-risk work. |

## Q2: Isolation

`header: "Isolation"`, single-select.

> *"How should `/rota-work` isolate changes from main?"*

| Label | Description |
|-------|-------------|
| Branch (Recommended) | Feature branch in the current worktree. Simple, works everywhere. |
| Worktree | Isolated directory under `.claude/worktrees/`. Lets you keep using main while agents work; supports parallel sessions. |

## Q3: Integration

`header: "Integration"`, single-select.

> *"How should `/rota-work` and `/rota-ship` integrate finished work?"*

| Label | Description |
|-------|-------------|
| Direct merge (Recommended) | Merge into main with `--no-ff` and delete the branch. Fast solo iteration. |
| GitHub PR | Push the branch and open a PR with `gh pr create`. Required for team review. |

## Q4: Quality gates

`header: "Gates"`, `multiSelect: true`. A checklist where users can pick any subset (or none).

> *"Which quality gates should run by default? (Uncheck anything you want off.)"*

| Label | Description |
|-------|-------------|
| Review before ship (Recommended) | `/rota-ship` runs `/rota-review` first. FAIL blocks, CONCERNS ask, PASS flows through. |
| Verify learnings (Recommended) | `/rota-learn` dispatches an Opus verifier for a cold pass on new entries. Knowledge quality compounds. |
| Confirm before refactor (Recommended) | `/rota-refactor` pauses for approval after finding friction and after selecting a design. Off = full autonomy. |
| Competing hypotheses (debug) | `/rota-debug` dispatches 3 parallel hypothesis agents from different angles. Better diversity on hard bugs, ~3× orchestrator cost. |

## Q5: Autonomy

`header: "Autonomy"`, single-select.

> *"How autonomously should rota chain to the next logical step?"*

| Label | Description |
|-------|-------------|
| Off (Recommended) | Skills nudge with a one-line suggestion at decision points. You stay in the driver's seat. |
| Auto chain | One-hop chaining: `/rota-work` → `/rota-learn`, `/rota-debug` → `/rota-ship`, `/rota-ship` → `/rota-learn`, refactor threshold → `/rota-refactor`. Stops after the chained step. |
| Full loop | Auto chain + after each cycle, run `/rota-work` with no argument and start the next item. Runs until the backlog drains, a guard fails, or a brief is genuinely ambiguous. |

## Mapping table: options to config values

Each Q1–Q5 option maps to a single `key.path: value` in `.rota/config.json`:

| Answer | Config |
|--------|--------|
| Q1 Balanced | `models: {orchestrator: "opus", worker: "sonnet"}` |
| Q1 Premium | `models: {orchestrator: "opus", worker: "opus"}` |
| Q1 Fast | `models: {orchestrator: "sonnet", worker: "sonnet"}` |
| Q1 Minimal | `models: {orchestrator: "sonnet", worker: "haiku"}` |
| Q2 Branch | `work.isolation: "branch"` |
| Q2 Worktree | `work.isolation: "worktree"` |
| Q3 Direct merge | `work.mergeStrategy: "direct"` |
| Q3 GitHub PR | `work.mergeStrategy: "pr"` |
| Q4 includes "Review before ship" | `ship.review: true` (else `false`) |
| Q4 includes "Verify learnings" | `learn.verify: true` (else `false`) |
| Q4 includes "Confirm before refactor" | `refactor.confirmBeforeExecute: true` (else `false`) |
| Q4 includes "Competing hypotheses" | `debug.competingHypotheses: true` (else `false`) |
| Q5 Off | `autonomy.level: "off"` |
| Q5 Auto chain | `autonomy.level: "auto"` |
| Q5 Full loop | `autonomy.level: "loop"` |

## Additional keys

Five more keys are not part of Q1-Q5. `rota init` writes their defaults; change them with `rota config set`.

### Docs path

Free text. Default: `docs`. Key `docs.path`.

### Docs auto-create

`On` / `Off`. Key `docs.autoCreate`.

### Docs after-work

`On` / `Off` (Recommended `Off`). Key `docs.afterWork`.

### Git base branch

Free text. Default: `""` (auto-detect). Key `git.baseBranch`.

### Umbrella mode

`On` / `Off`. Key `umbrella.enabled`. `.rota/` stays at the umbrella; verbs operate per sub-repo. Toggling off does **not** delete `.rota/repos.json`; registered repos remain and are simply ignored until umbrella mode is re-enabled. To add or remove repos from the registry, run `rota init umbrella` from the umbrella root (idempotent).

## Validation rules

- **Enums.** `models.*` take `opus`, `sonnet` or `haiku`; `work.isolation` takes `branch` or `worktree`; `work.mergeStrategy` takes `direct` or `pr`; `autonomy.level` takes `off`, `auto` or `loop`. Anything else is rejected by `rota config set`.
- **Booleans and integers.** Booleans take `true` or `false`; integer keys state their minimum below. Out-of-range values exit 70 where a verb reads them.

## Silent-default keys

`rota init` fills these with the silent default; set them only when you want something else:

- `rota.version`: stamp of the rota release that wrote the config. Auto-managed by `rota init` and `rota update`; do not set it by hand. The stamp a project got before the rename to rota is read as a fallback and moved here by `rota init` / `rota config fill`.
- `refactor.verifyCommands`: array of shell commands run as CI-shape gates by /rota-refactor Step 7. Silent default `[]` (read-only verification). Set via `rota config set refactor.verifyCommands '[...]'`.
- `ship.secondOpinion`: opt-in fresh-eyes adversarial gate in /rota-ship Step 3.5. Silent default `false` (Rule 9). Set via `rota config set ship.secondOpinion true`.
- `ship.secondOpinionRunner`: who runs the /rota-ship Step 3.5 gate when `ship.secondOpinion` is `true`. Enum `subagent` (silent default). The `codex` value was removed in 5.0: /rota-ship prints a one-line note and runs the subagent in advisory mode (FAIL is surfaced, never blocks), as the Codex runner did. See [`usage/configuration.md`](../usage/configuration.md#shipsecondopinionrunner).
- `ship.qa`: opt-in product-QA gate in /rota-ship Step 3.75. Silent default `false` (Rule 9). When `true`, /rota-ship invokes [`/rota-qa run`](../usage/qa.md) after /rota-review (and second-opinion if on) and before merge / PR. Set via `rota config set ship.qa true`. See [`usage/configuration.md`](../usage/configuration.md#shipqa).
- `ship.mergeApproval` / `ship.mergeApprovalPaths`: which merges need a human. Enum `none` (silent default), `all` or `paths`, plus a list of repo-relative paths or globs for `paths`. The merge verbs enforce it at every autonomy level. Set via `rota config set ship.mergeApproval all`. See [`usage/configuration.md`](../usage/configuration.md#shipmergeapproval-and-shipmergeapprovalpaths).
- `round.scope` / `round.roster` / `round.brief` / `round.sharedPaths` / `round.stallMinutes`: how `rota round` runs a round. `round.scope` is which issues a round may take: `slate` (only the issues named at `rota round start --items`), `milestone` (silent default: the active milestones) or `next` (also the next ready milestone). `round.roster` is the agent names slots are provisioned under, silent default `["ben","dana","nia","kit"]`. `round.brief` is the path the worker pointer names, `round.sharedPaths` the globs the file-overlap check ignores; both silent default empty. `round.stallMinutes` is how long a slot with a live agent may show no commit, edit or state change before `rota round reconcile` reports it `stalled`, silent default `30`, `0` turns it off. Not the same axis as `autonomy.level`. Set via `rota config set round.scope slate`. See [`usage/configuration.md`](../usage/configuration.md#round-keys).
- `round.tier` / `round.tiers.<kind>.<tier>`: worker model tiers for `rota round assign`. `round.tier` is the default tier (`light`, `standard` or `heavy`; silent default `standard`). `round.tiers.claude.light|standard|heavy` map a tier to a model (silent defaults `haiku`, the value of `models.worker`, `opus`); `round.tiers.codex.*` default empty and are optional (unset, a Codex worker runs on Codex's own default model), and a configured kind must name all three. An explicit `round.tiers.claude.standard` wins over `models.worker`. Set via `rota config set round.tiers.claude.heavy opus`. See [`usage/configuration.md`](../usage/configuration.md#round-keys).
- `orchestrator.handoffThreshold` / `orchestrator.stateMaxAgeSeconds` / `orchestrator.handoffMaxAgeSeconds` / `orchestrator.handoffMaxBlocks`: how the orchestrator hands off before its context runs out (`rota hook stop`, `rota hook session-start`). `handoffThreshold` is the context percentage, an integer from 1 to 100, at which the Stop hook blocks until a handoff is written (silent default `75`). `stateMaxAgeSeconds` is how old the statusline reading may be before the hook ignores it (`120`). `handoffMaxAgeSeconds` is how long a handoff counts as fresh (`900`). `handoffMaxBlocks` is how many times the hook re-blocks a session that still has no handoff before giving up (`2`). `rota init` does not write them; an out-of-range value exits 70 where a verb reads it, and the hooks treat it as a pass. Set via `rota config set orchestrator.handoffThreshold 80`. See [`usage/unattended-rounds.md`](../usage/unattended-rounds.md).
- `orchestrator.keepaliveMaxRestarts` / `orchestrator.keepaliveBreaker` / `orchestrator.keepaliveBackoffSeconds` / `orchestrator.restartPrompt` / `orchestrator.escalateIssue`: how `rota keepalive run` restarts the orchestrator. `keepaliveMaxRestarts` is the restarts before it gives up, an integer of 0 or more (silent default `10`; `0` stops at the first handoff exit). `keepaliveBreaker` is how many restarts in a row may leave no new handoff before the breaker trips, 1 or more (`3`). `keepaliveBackoffSeconds` is the wait before a restart, 0 or more (`5`). `restartPrompt` is the text appended as the last argument of a restart, a non-empty string (default `Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round.`). `escalateIssue` is the issue number the breaker's escalation comment goes on, 0 or more (`0`: unset, so a host notification and a warning only). `rota init` does not write them; an out-of-range value exits 70. Each of the first four has a `rota keepalive run` flag that overrides it for one run. Set via `rota config set orchestrator.escalateIssue 12`. See [`usage/unattended-rounds.md`](../usage/unattended-rounds.md).
- `orchestrator.harness`: which agent `rota orchestrate` (and bare `rota` in an initialized project) starts as the orchestrator. `claude` (silent default) or `codex`. The launcher starts it under `rota keepalive run` with the orchestrate skill as its first prompt (`/rota-orchestrate` for Claude Code, `$rota-orchestrate` for Codex). Claude runs `work.operatorCommand` when that is set, else `claude --model <models.orchestrator> --permission-mode auto`. Set via `rota config set orchestrator.harness codex`. See [`usage/configuration.md`](../usage/configuration.md).
- `orchestrator.switchOnUsage` / `orchestrator.usageThreshold`: opt-in switch of the orchestrator's account before a usage limit (D4). `switchOnUsage` is a boolean (silent default `false`: D3's sleep is the behavior). With it `true`, the Stop hook also blocks for a handoff when the larger of the 5-hour and weekly `used_percentage` reaches `usageThreshold` (integer 1 to 100, silent default `90`), and only under `rota keepalive run`; the supervisor then restarts under another account that is `free` with headroom above `100 - usageThreshold`, or on the same account with a hold when there is none. `rota init` does not write them; an out-of-range value exits 70 where a verb reads it, and the hook treats it as a pass. Set via `rota config set orchestrator.switchOnUsage true`. See [`usage/unattended-rounds.md`](../usage/unattended-rounds.md).
- `limits.mode` / `limits.resumeMarginSeconds` / `limits.fallbackSleepSeconds` / `limits.maxResumes` / `limits.resumePrompt`: how the usage-limit watcher (`rota limit watch`, and the loop inside `rota keepalive run`) reacts to a 5-hour or weekly limit. `mode` is `switch` (silent default: a worker's issue moves to an idle slot on another account that has headroom, else it sleeps) or `sleep` (always wait for the reset); the orchestrator only ever sleeps. `resumeMarginSeconds` is the wait after the reset before the resume prompt is typed, 0 or more (`60`). `fallbackSleepSeconds` is how long a limit with no known reset time sleeps, 1 or more (`1800`). `maxResumes` is how many resume prompts one limit gets before the entry is `failed` and escalated, 1 or more (`3`). `resumePrompt` is the text typed into the limited pane, a non-empty string (default `The usage limit has reset. Continue where you left off.`). `rota init` does not write them; an out-of-range value exits 70. Set via `rota config set limits.mode sleep`. See [`usage/unattended-rounds.md`](../usage/unattended-rounds.md).
- `qa.gate`: verdict routing for /rota-qa invocations from /rota-ship. Silent default `"advisory"` (surface findings, never block). Alternative `"blocking"` halts the ship on `FAIL`. Set via `rota config set qa.gate blocking`. See [`usage/configuration.md`](../usage/configuration.md#qagate).
- `qa.afterWork`: post-cycle /rota-qa invocation from /rota-work when touched files match a target's `Watch globs`. Silent default `false`. Set via `rota config set qa.afterWork true`. See [`usage/configuration.md`](../usage/configuration.md#qaafterwork).
- `learn.promoteThreshold`: F03 knowledge-lifecycle auto-promotion threshold. Integer ≥ 0; silent default `3`. Set via `rota config set learn.promoteThreshold <N>` when a project wants stricter or looser confidence gating. See [`usage/configuration.md`](../usage/configuration.md#learnpromotethreshold).
- `work.dispatch`: where workers run. Enum `subagent` (default, written by `rota init`), `tmux` or `herdr`. For `/rota-work`, `subagent` means in-process workers, and `tmux` or `herdr` must be set explicitly: `tmux` needs a `tmux` binary and a working `claude` on `PATH`, and `herdr` needs `/rota-work` to run inside a herdr pane. For `rota round`, `subagent` means detect: herdr inside a herdr pane, tmux inside tmux, else solo (in-harness Claude subagents), so a round needs no setting. An explicit `tmux` or `herdr` is used as set. Set via `rota config set work.dispatch tmux`. See [`usage/configuration.md`](../usage/configuration.md#workdispatch-subagent-tmux-or-herdr).
- `work.workerSlots`: number of worker slots. Integer ≥ 1; silent default `3`. `rota round start` provisions this many slots (`--slots` overrides it for one round). For `/rota-work` it sizes the tmux or herdr pool and is ignored under `subagent`. Set via `rota config set work.workerSlots <N>`.
- `work.accounts`: array of `{name, configDir}` mapping worker slots to independent `CLAUDE_CONFIG_DIR`s, so each slot authenticates as its own account. Silent default `[]` (every slot inherits the ambient config dir). Used by rounds in herdr or tmux (`rota round assign` balances slots across accounts) and by `/rota-work` under `tmux` or `herdr`; solo rounds and `subagent` ignore it. The paths are machine-specific, so put the array in the gitignored `.rota/config.local.json` by hand rather than with `rota config set`, which writes the tracked file. `rota worker account list` shows each account's usage verdict. See [`usage/parallel-rounds.md`](../usage/parallel-rounds.md#setup).
- `work.operatorCommand`: `/rota-work` only. Command used to relaunch the `/rota-work` orchestrator inside tmux when it runs `work.dispatch: tmux` from a non-tmux terminal. Rounds don't use it: you start a round's orchestrator yourself, and `rota keepalive run` restarts it (see [`usage/unattended-rounds.md`](../usage/unattended-rounds.md)). Silent default `""`, which builds `claude --continue --model <models.orchestrator> --permission-mode auto` — `--continue` resumes the current conversation so the cycle keeps its context, and the operator keeps a permission gate the workers do not. Set it when your orchestrator needs a wrapper.
- `work.codexCommand`: command used to launch a Codex worker session (`--kind codex`, herdr only). Free text; silent default `""`, which builds `codex --model <tier model> --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen` and drops `--model` when no model is chosen (a bare `rota worker dispatch`, or `round.tiers.codex` unset). A custom command receives the tier's model through a `{model}` placeholder; one that still holds `{model}` with no model chosen is a usage error. It must not carry codex's `resume` or `fork` subcommands.
- `work.workerCommand`: command used to launch a worker session in its tmux window. Free text; silent default `""`, which builds `claude --model <models.worker> --dangerously-skip-permissions`. Workers commit, open PRs and run tests with nobody in the pane to answer a prompt, so a narrower mode stalls them. Set it to narrow the grant or to add a wrapper; a worker that then stops on a prompt reports `NEEDS-PERMISSION` instead of hanging.
- `backlog.backend`: where the backlog lives. `"file"` (silent default) or `"issues"`. Switch to issues mode via `rota migrate issues`. Set via `rota config set backlog.backend issues`. See [`usage/configuration.md`](../usage/configuration.md#issues-backend-keys).
- `issues.provider`: which tracker the issue backend talks to. `"auto"` (silent default), `"github"` or `"gitlab"`. Used by `backlog.backend: "issues"`.
- `issues.retryWaitSeconds`: seconds to wait before retrying a failed tracker call. Integer; silent default `60`.
- `issues.bulkPaceMs`: milliseconds `rota migrate issues` waits between tracker writes, to stay under GitHub's secondary rate limits. Integer; silent default `1000`. Set `0` in tests.
- `issues.homeRepo`: umbrella mode with `backlog.backend: "issues"` only. Name of the registered sub-repo that holds milestone tracking issues. String; silent default `""` (the first registered sub-repo).
- `issues.labels.*`: tracker label names per role. Defaults: `inProgress` `in-progress`, `needsReview` `needs-review`, `changesRequested` `changes-requested`, `released` `released`, `notPlanned` `not-planned`, `blocked` `blocked`, `needsHuman` `needs-human` (set by `rota round transfer --to human`, skipped by `rota round candidates`; silent default), `milestoneTracker` `milestone-tracker`, `types.bug` `type:bug`, `types.feature` `type:feature`, `types.task` `type:task`, `priorityPrefix` `p`, `sizePrefix` `size:` (feature size labels such as `size:Major`). `issues.label` is the legacy alias of `issues.labels.inProgress` and is used when the new key is unset.

For the full per-key behavior (defaults, value semantics, and how each setting affects skill execution), see [`usage/configuration.md`](../usage/configuration.md).
