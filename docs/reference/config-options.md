# Configuration options

This page lists every config key with its type, default, allowed values and description, grouped by section, plus the five core settings (Q1-Q5) `rota setup` asks. For a concept-first walk through each key, see [`usage/configuration.md`](../usage/configuration.md).

The key tables are generated from the schema in `internal/config/keys.go` (`go generate ./internal/config`); do not edit them here. Four verbs cover configuration:

- **`rota setup`** runs `rota init`, then asks the main choices on a terminal (backlog backend, tracker, isolation, merge strategy, dispatch, autonomy, review, QA), then two optional ones: the worker harness (`round.workerKind`) and the orchestrator harness (`orchestrator.harness`). Skip either and the key stays unset. `--yes` takes the defaults; `--list` prints the questions.
- **`rota init`** writes `.rota/config.json` on first setup, fills any missing keys with the Recommended defaults on later runs, and stamps `rota.version`. It never overwrites a value you set.
- **`rota config show`** and **`rota config set`** read and change keys later. `rota config show --json` adds each key's `type`, `group`, `desc` and `choices`.
- **`rota config edit`** opens the config screen in a terminal (also `rota config --ui`): keys grouped by section with a detail pane, booleans toggle, enum keys pick from their choices, the rest take a typed value checked as `config set` checks it. See [configuration](../usage/configuration.md#the-config-screen).

The "(Recommended)" tag on each option marks the default `rota init` writes.

## rota config invocation shapes

`rota config` takes positional arguments:

| Shape | Behavior |
|-------|----------|
| `rota config show` | Prints every key, value and source layer: `local`, `project` or `default`. |
| `rota config show <key>` | Prints one key. |
| `rota config set <key> <value>` | Writes `.rota/config.json` (never `.rota/config.local.json`) and prints `key = value`. |

Values for list and object keys are JSON (`rota config set work.accounts '[...]'`). A key outside the schema exits 2 and nothing is written. The value is not checked against the allowed values on write; `rota config check` only flags a missing key, retired values such as `autonomy.level: "loop"`, and a legacy `refactor.verifyCommands` that still holds commands while `test.full` is empty.

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
| Pull request | Push the branch and open a PR (GitHub) or MR (GitLab). Required for team review. |

## Q4: Quality gates

`header: "Gates"`, `multiSelect: true`. A checklist where users can pick any subset (or none).

> *"Which quality gates should run by default? (Uncheck anything you want off.)"*

| Label | Description |
|-------|-------------|
| Review before ship (Recommended) | `/rota-ship` runs `/rota-review` first. FAIL blocks, CONCERNS ask, PASS flows through. |
| Verify learnings | `/rota-learn` dispatches an Opus verifier for a cold pass on new entries. Off by default; `--strict` runs it once. |
| Confirm before refactor (Recommended) | `/rota-refactor --fix` confirms the candidate list before implementing. Off = no pause. The default findings run never edits code. |

## Q5: Autonomy

`header: "Autonomy"`, single-select.

> *"How autonomously should rota chain to the next logical step?"*

| Label | Description |
|-------|-------------|
| Off (Recommended) | Skills nudge with a one-line suggestion at decision points. You stay in the driver's seat. |
| Auto chain | One-hop chaining: `/rota-ship` → `/rota-learn` when the cycle is big enough to warrant it. Stops after the chained step. |

`autonomy.level: "loop"` was removed; `rota config check` fails on it (rounds and automatic reviews cover unattended work).

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
| Q3 Pull request | `work.mergeStrategy: "pr"` |
| Q4 includes "Review before ship" | `ship.review: "full"` (else `"none"`) |
| Q4 includes "Verify learnings" | `learn.verify: true` (else `false`) |
| Q4 includes "Confirm before refactor" | `refactor.confirmBeforeExecute: true` (else `false`) |
| Q5 Off | `autonomy.level: "off"` |
| Q5 Auto chain | `autonomy.level: "auto"` |

## Keys

Every key, by group. Default is what a missing key reads as; Values lists the allowed values of an enum key.

### models

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `models.orchestrator` | string | `"opus"` |  | Model for planning, exploration, verification and design. Usually opus, sonnet or haiku. |
| `models.worker` | string | `"sonnet"` |  | Model for implementation sub-tasks and round workers. Usually opus, sonnet or haiku. |

### work

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `work.isolation` | enum | `"branch"` | `branch`, `worktree` | How /rota-work isolates changes from main: a feature branch in this checkout, or a separate git worktree. |
| `work.mergeStrategy` | enum | `"direct"` | `direct`, `pr` | How finished work lands: merged straight into the base branch with --no-ff, or through a pull request. Ignored under the issues backlog backend, which always opens a PR. |
| `work.dispatch` | enum | `"subagent"` | `subagent`, `tmux`, `herdr` | Where round workers run. subagent means detect: herdr in a herdr pane, tmux in tmux, else in-harness subagents. /rota-work ignores it. |
| `work.workerSlots` | int | `3` |  | Number of worker slots rota round start provisions (at least 1). --slots overrides it for one round. |
| `work.workerCommand` | string | unset |  | Command that starts a worker session in its tmux window. Empty builds claude --model <models.worker> --dangerously-skip-permissions; {model} receives the tier's model. |
| `work.accounts` | list | `[]` |  | Claude accounts for worker slots, as {name, configDir} objects, each a separate CLAUDE_CONFIG_DIR. Empty: every slot inherits the ambient config dir. Machine-specific, so set it in config.local.json. |
| `work.operatorCommand` | string | unset |  | Command that starts the orchestrator. Empty builds claude --model <models.orchestrator> --permission-mode auto (with --continue for rota worker session ensure). |
| `work.codexAccounts` | list | `[]` |  | Codex homes for Codex workers, as {name, codexHome} objects, the counterpart of work.accounts. Empty: the default Codex home. Machine-specific, so set it in config.local.json. |
| `work.codexCommand` | string | unset |  | Command that starts a Codex worker session. Empty builds the default codex command, with --model from the tier when one is chosen; {model} receives it. |
| `work.envSetup` | string | unset |  | Shell command rota worker pool init runs in each new slot worktree, for example npm ci. Empty: no setup. |
| `work.portBase` | int | `20000` |  | First port of the range rota hands out to worker slots. A new slot takes the lowest range of portBlock ports from here that no other slot holds, exported as ROTA_PORT_BASE; a slot keeps its range until it is reaped or reclaimed. |
| `work.portBlock` | int | `100` |  | Ports reserved per worker slot, so servers started by two slots never collide. A slot's block is exported as ROTA_PORT_BASE. |
| `work.tdd` | bool | `true` |  | Whether /rota-work and workers require a recorded red-first run before a behavior change. Off skips the RED requirement. |
| `work.itemTimeoutMinutes` | int | `0` |  | Minutes one item may run from its first assignment before rota round reconcile reports it as timed out and --apply parks it as needs-human. 0 means no cap. |

### refactor

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `refactor.confirmBeforeExecute` | bool | `true` |  | Whether /rota-refactor --fix confirms the candidate list before implementing it. Off means no pause. |

### learn

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `learn.verify` | bool | `false` |  | Whether /rota-learn always runs a fresh-context verifier on the entries it just wrote. |
| `learn.promoteThreshold` | int | `3` |  | Confidence threshold at which the knowledge lifecycle auto-promotes an entry. Integer, 0 or more. |

### ship

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `ship.review` | enum | `"full"` | `full`, `light`, `none` | How deep a review /rota-ship runs first: full, light (the Standards reviewer only) or none. FAIL blocks, CONCERNS ask, PASS flows through. An object {default, lightBelow, labels} picks the depth by diff size and label; true and false still mean full and none. |
| `ship.secondOpinion` | bool | `false` |  | Opt-in fresh-eyes adversarial gate in /rota-ship Step 3.5. |
| `ship.secondOpinionRunner` | enum | `"subagent"` | `subagent` | Who runs the /rota-ship second-opinion gate. The codex value was removed in 5.0: /rota-ship notes it and runs the subagent in advisory mode. |
| `ship.qa` | bool | `false` |  | Opt-in product-QA gate: /rota-ship runs /rota-qa run after review and before merge or PR. |
| `ship.mergeApproval` | enum | `"none"` | `none`, `all`, `paths` | Which merges need a human: none, all, or only those touching ship.mergeApprovalPaths. The merge verbs enforce it at every autonomy level. |
| `ship.mergeApprovalPaths` | list | `[]` |  | Repo-relative paths or globs that need human approval when ship.mergeApproval is paths. |

### qa

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `qa.gate` | enum | `"advisory"` | `advisory`, `blocking` | How /rota-ship treats a /rota-qa verdict. advisory surfaces findings and never blocks; blocking halts the ship on FAIL. |
| `qa.afterWork` | bool | `false` |  | Whether /rota-work runs /rota-qa after a cycle when touched files match a QA target's watch globs. |

### autonomy

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `autonomy.level` | enum | `"off"` | `off`, `auto` | How much rota chains on its own. off: skills suggest the next step. auto: chain one hop, then stop. The old loop value was removed. |

### docs

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `docs.path` | path | `"docs"` |  | Project-relative documentation folder that /rota-ship --docs reads and writes. |
| `docs.autoCreate` | bool | `false` |  | Whether the after-work docs flow writes proposed updates without waiting for approval. |
| `docs.afterWork` | bool | `false` |  | Whether /rota-work, /rota-ship and /rota-release run the docs after-work flow when their primary action finishes. |

### git

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `git.baseBranch` | string | unset |  | Base branch the skills merge into and diff against. Empty auto-detects it. |

### umbrella

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `umbrella.enabled` | bool | `false` |  | Umbrella mode: .rota/ stays at the umbrella and verbs operate per registered sub-repo. Turning it off keeps .rota/repos.json. |

### rota

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `rota.version` | string | unset |  | Release of rota that wrote this config. Managed by rota init and rota update; do not set it by hand. |

### issues

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `issues.label` | string | `"in-progress"` |  | Legacy alias of issues.labels.inProgress, read only when the new key is unset. |
| `issues.provider` | enum | `"auto"` | `auto`, `github`, `gitlab` | Which tracker the issues backend talks to. auto detects it from the git remote. |
| `issues.retryWaitSeconds` | int | `60` |  | Seconds to wait before the single retry after a primary rate limit. |
| `issues.bulkPaceMs` | int | `1000` |  | Milliseconds rota migrate issues waits between tracker writes, to stay under secondary rate limits. 0 disables the pause. |
| `issues.labels.inProgress` | string | `"in-progress"` |  | Tracker label for an item a worker holds. |
| `issues.labels.needsReview` | string | `"needs-review"` |  | Tracker label for an item whose work awaits review. |
| `issues.labels.changesRequested` | string | `"changes-requested"` |  | Tracker label for an item sent back with review feedback. |
| `issues.labels.released` | string | `"released"` |  | Tracker label for an item that shipped in a release. |
| `issues.labels.notPlanned` | string | `"not-planned"` |  | Tracker label for an item closed as not planned. |
| `issues.labels.blocked` | string | `"blocked"` |  | Label rota item complete --reason blocked sets; the issue stays open. |
| `issues.labels.milestoneTracker` | string | `"milestone-tracker"` |  | Tracker label for a milestone's tracking issue. |
| `issues.labels.types.bug` | string | `"type:bug"` |  | Tracker label for the bug item type. |
| `issues.labels.types.feature` | string | `"type:feature"` |  | Tracker label for the feature item type. |
| `issues.labels.types.task` | string | `"type:task"` |  | Tracker label for the task item type. |
| `issues.labels.priorityPrefix` | string | `"p"` |  | Prefix of priority labels. |
| `issues.labels.sizePrefix` | string | `"size:"` |  | Prefix of feature size labels, such as size:Major. |
| `issues.autoCreateLabel` | bool | `true` |  | Create a tracker label the first time something asks for it. When off, adding a missing label fails. |
| `issues.homeRepo` | string | unset |  | Umbrella mode with the issues backend only: the registered sub-repo that holds milestone tracking issues. Empty means the first registered sub-repo. |
| `issues.labels.needsHuman` | string | `"needs-human"` |  | Label rota round transfer --to human puts on an issue handed to the human. rota round candidates skips an issue that carries it. |

### backlog

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `backlog.backend` | enum | `"file"` | `file`, `issues` | Where the backlog lives: BACKLOG.md in the repo, or issues on the tracker. Switch with rota migrate issues. |

### release

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `release.checklistPath` | path | `".rota/RELEASE.md"` |  | Project's release checklist, whose open items /rota-release walks before it tags. |
| `release.confirmLargePushCommits` | int | `10` |  | Unpushed commits /rota-release pushes without asking under autonomy.level auto. At or above this it asks once. |
| `release.nudgeAfterCommits` | int | `10` |  | Commits since the last release tag before rota release pending suggests /rota-release. |
| `release.nudgeAfterDays` | int | `14` |  | Days since the last release tag before rota release pending suggests /rota-release. |
| `release.versionFile` | path | unset |  | Project-relative file /rota-release and rota release version read and bump. Empty auto-detects; a path outside the project is refused. |

### test

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `test.fast` | list | `[]` |  | Shell commands for the quick per-task and worker checks. {files} expands to the changed files, single-quoted; put -- before it. |
| `test.full` | list | `[]` |  | Shell commands for the full suite, run by rota worker gate and the merge train on the merged tree. Empty with test.e2e also empty: the gate refuses to merge without --no-verify. |
| `test.e2e` | list | `[]` |  | Shell commands for slow end-to-end checks, run by the gate and merge train after test.full passes. Empty skips the step. |
| `test.fullWhere` | enum | `"local"` | `local`, `ci` | Where the gate and train run the full tier: here, or by pushing the merge result and waiting for the forge's checks (needs test.ciChecks). |
| `test.ciTimeoutMinutes` | int | `60` |  | How long a ci run waits for its checks, 1 to 1440 minutes. ROTA_CI_TIMEOUT (seconds) overrides it for one run. |
| `test.ciChecks` | list | `[]` |  | Check names that must all succeed on the pushed commit under test.fullWhere ci: GitHub check-run names or commit-status contexts, GitLab job names. |
| `test.isolate` | bool | `true` |  | Whether rota test run scrubs HERDR_*, TMUX*, ssh-agent variables and pins HOME and XDG_* to a temp root. The merge gate and train do not read it. |

### round

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `round.scope` | enum | `"milestone"` | `milestone`, `slate`, `next`, `open` | Which issues a round may take. slate: only those named at rota round start --items. milestone: the active milestones. next: also the next ready milestone. open: every open item no slot holds. |
| `round.roster` | list | `["ben", "dana", "nia", "kit"]` |  | Agent names slots are provisioned under, one slot each. Lowercase letters, digits and -, no duplicates. |
| `round.brief` | path | unset |  | Path of the standing worker contract the assignment pointer names. Empty means skills/references/worker-contract.md in the checkout, else the installed copy. |
| `round.sharedPaths` | list | `[]` |  | Repo-relative globs the file-overlap readiness check ignores, for files every issue touches. |
| `round.scopeOverlap` | enum | `"warn"` | `warn`, `block` | What a clash on declared scopes (the ## Touches section, else Subsystem) does to the overlap check. warn: reported, the item stays ready. block: fails the check like a shared path; --accept-overlap skips it. |
| `round.adoptPattern` | string | unset |  | Glob over branch names (codex/*, claude/*). rota round reconcile reports a matching branch that no slot holds and that is not merged as unregistered-branch; --apply adopts those whose name carries an issue number. Empty turns the check off. |
| `round.tier` | enum | `"standard"` | `light`, `standard`, `heavy` | Default worker tier for rota round assign: light for reading, standard for code and tests, heavy for hard reasoning. |
| `round.workerKind` | enum | unset | `claude`, `codex` | Project default worker harness. Empty: the slot's recorded kind, else claude. --kind and an issue's harness: label beat it. |
| `round.tiers.claude.light` | string | `"haiku"` |  | Model a light-tier Claude worker starts with. |
| `round.tiers.claude.standard` | string | unset |  | Model a standard-tier Claude worker starts with. Empty follows models.worker. |
| `round.tiers.claude.heavy` | string | `"opus"` |  | Model a heavy-tier Claude worker starts with. |
| `round.tiers.codex.light` | string | unset |  | Model a light-tier Codex worker starts with. Empty: Codex's own default; a configured kind must set all three tiers. |
| `round.tiers.codex.standard` | string | unset |  | Model a standard-tier Codex worker starts with. Empty: Codex's own default; a configured kind must set all three tiers. |
| `round.tiers.codex.heavy` | string | unset |  | Model a heavy-tier Codex worker starts with. Empty: Codex's own default; a configured kind must set all three tiers. |
| `round.stallMinutes` | int | `30` |  | Minutes a slot with a live agent may show no commit, edit or state change before rota round reconcile reports it stalled. 0 turns the check off. |
| `round.maxBounces` | int | `3` |  | How often rota worker gate may send one item's PR back before it parks the item as needs-human. 0 turns the cap off. |
| `round.ledgerKeep` | int | `0` |  | Rounds of the round ledger (.rota/ledger.jsonl) to keep. A new round trims the entries of older rounds. 0 keeps everything. |
| `round.architectureEvery` | int | `20` |  | Closed non-refactor items between automatic architecture reviews. 0 turns them off. |
| `round.architectureAreas` | list | `[]` |  | Areas an architecture review is split into, one review item each. Empty means the subsystem map's names, else one whole-repo review. |
| `round.autopilot` | bool | `false` |  | Lets rota round watch --autopilot and rota round tick assign, gate and merge mechanically. Merges only under ship.mergeApproval none. |
| `round.autopilotCap` | int | `3` |  | Most assigns, and most merges, one autopilot tick does. 0 means the default. |
| `round.reviewLoop` | enum | `"manual"` | `manual`, `auto` | What happens when a reviewer comments on a finished worker's PR. manual reports it and leaves rota round review-relay to the orchestrator; auto relays it to the worker as a counted bounce (round.maxBounces) from rota round watch and rota round tick. |

### roles

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `roles.explorer.tier` | enum | `"light"` | `light`, `standard`, `heavy` | Tier of the rota-explorer agent, which picks its model from round.tiers. |
| `roles.explorer.effort` | enum | unset | `low`, `medium`, `high`, `xhigh`, `max` | Reasoning effort of the rota-explorer agent. Empty leaves the harness default; Codex has no max. |
| `roles.implementer.tier` | enum | `"standard"` | `light`, `standard`, `heavy` | Tier of the rota-implementer agent, which picks its model from round.tiers. |
| `roles.implementer.effort` | enum | unset | `low`, `medium`, `high`, `xhigh`, `max` | Reasoning effort of the rota-implementer agent. Empty leaves the harness default; Codex has no max. |
| `roles.reasoner.tier` | enum | `"heavy"` | `light`, `standard`, `heavy` | Tier of the rota-reasoner agent, which picks its model from round.tiers. |
| `roles.reasoner.effort` | enum | unset | `low`, `medium`, `high`, `xhigh`, `max` | Reasoning effort of the rota-reasoner agent. Empty leaves the harness default; Codex has no max. |

### orchestrator

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `orchestrator.handoffThreshold` | int | `75` |  | Context percentage, 1 to 100, at which the Stop hook blocks until the orchestrator writes a handoff. |
| `orchestrator.stateMaxAgeSeconds` | int | `120` |  | How old the statusline reading may be before the hooks ignore it. |
| `orchestrator.handoffMaxAgeSeconds` | int | `900` |  | How long a handoff counts as fresh. |
| `orchestrator.handoffMaxBlocks` | int | `2` |  | How many times the Stop hook re-blocks a session that still has no handoff before it gives up. |
| `orchestrator.keepaliveMaxRestarts` | int | `10` |  | Restarts rota keepalive run makes before it gives up. 0 stops at the first handoff exit. |
| `orchestrator.keepaliveBreaker` | int | `3` |  | How many restarts in a row may leave no new handoff before the breaker trips. 1 or more. |
| `orchestrator.keepaliveBackoffSeconds` | int | `5` |  | Seconds rota keepalive run waits before a restart. 0 or more. |
| `orchestrator.restartPrompt` | string | `"Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round."` |  | Text appended as the last argument of a restart. Must not be empty. |
| `orchestrator.escalateIssue` | int | `0` |  | Issue number the breaker's escalation comment goes on. 0 leaves it unset: a host notification and a warning only. |
| `orchestrator.harness` | enum | `"claude"` | `claude`, `codex`, `hermes`, `opencode` | Which agent rota orchestrate, and bare rota in an initialized project, starts as the orchestrator. |
| `orchestrator.switchOnUsage` | bool | `false` |  | Opt-in: block the Stop hook for a handoff when usage reaches orchestrator.usageThreshold, so the supervisor restarts under another account. |
| `orchestrator.usageThreshold` | int | `90` |  | Percent of the 5-hour or weekly limit, 1 to 100, at which orchestrator.switchOnUsage triggers. |

### limits

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `limits.mode` | enum | `"switch"` | `switch`, `sleep` | How the usage-limit watcher reacts to a limit. switch moves a worker's issue to an idle slot on an account with headroom, else sleeps. sleep always waits for the reset. |
| `limits.resumeMarginSeconds` | int | `60` |  | Seconds to wait after the reset before the resume prompt is typed. 0 or more. |
| `limits.fallbackSleepSeconds` | int | `1800` |  | Seconds to sleep on a limit with no known reset time. 1 or more. |
| `limits.maxResumes` | int | `3` |  | Resume prompts one limit gets before the entry is marked failed and escalated. 1 or more. |
| `limits.resumePrompt` | string | `"The usage limit has reset. Continue where you left off."` |  | Text typed into the limited pane to resume it. Must not be empty. |

### gate

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `gate.smokeShards` | int | `4` |  | Concurrent shards bash test/gate.sh splits the smoke suite into, 1 or more. ROTA_SMOKE_SHARDS overrides it for one run. |

### doctor

| Key | Type | Default | Values | Description |
|-----|------|---------|--------|-------------|
| `doctor.minFreeDiskPercent` | int | `10` |  | rota doctor warns when the free share of the disk falls below this percent, 0 to 100. 0 turns the check off. |

## Removed keys

`issues.filterMineOnly`, `issues.providers.github` and `issues.providers.gitlab` were seeded by older `rota init` runs but nothing ever read them (use `issues.provider` to pick the tracker). `rota config check` lists any still in `.rota/config.json` as `removed` with a hint and does not fail on them; `rota config fill` deletes them. `rota config set` refuses them as unknown keys.

## Validation rules

- **`rota config set`** checks only that the key is in the schema. It accepts any value (JSON when it parses, else the string) and checks no enum or range.
- **Enums without a reader check.** `models.*` (`opus`, `sonnet` or `haiku`), `work.isolation` (`branch` or `worktree`), `work.mergeStrategy` (`direct` or `pr`) and `autonomy.level` (`off` or `auto`) are not validated by any verb. The skills and the launch command read them as plain strings, so a bad value surfaces as that skill's or the model's failure. `rota config check` only reports a missing key, the removed `autonomy.level` `"loop"`, or a `refactor.verifyCommands` that still holds commands `test.full` lacks (run `rota config fill` to move them).
- **Checked where a verb reads them.** A bad value exits 70 (`rota round` verbs for the `round.*` keys, `rota keepalive` and `rota limit` for the `orchestrator.*` keepalive and switch keys and the `limits.*` keys, and any verb that reads `backlog.backend`). The checks are these: booleans take `true` or `false`; integers must be in the minimum-to-maximum range stated in the key's description; `round.scope` and `limits.mode` take their listed enums; `round.roster` entries are unique lowercase names. `ship.mergeApproval` is also checked at read time, but a bad value there exits 2.

For the full per-key behavior (defaults, value semantics, and how each setting affects skill execution), see [`usage/configuration.md`](../usage/configuration.md).
