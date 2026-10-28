# Configuration options

This page lists every config key with its type, default, allowed values and description, grouped by section, plus the five core settings (Q1-Q5) `rota setup` asks. For a concept-first walk through each key, see [`usage/configuration.md`](../usage/configuration.md).

The key tables are generated from the schema in `internal/config/keys.go` (`go generate ./internal/config`); do not edit them here. Four verbs cover configuration:

- **`rota setup`** asks the machine role first (`round.role`; `support` takes the support path, see [Support role](../contributing/rounds.md#support-role)). On main it runs `rota init`, then asks the main choices on a terminal (where the issues live, isolation, merge strategy, dispatch, autonomy, review, QA), then optional ones: the worker harness (`round.harnesses`), the orchestrator harness (`orchestrator.harness`), the base branch (`git.baseBranch`) and the test commands (`test.fast`, `test.full`). Skip one and the key stays unset. Where the repo gives a signal (go.mod, CI files, the origin host, harnesses on PATH), the question suggests that value and shows where it was read. `--yes` takes the suggestion, else the default; `--list` prints the questions with each suggestion and its evidence.
- **`rota init`** writes `.rota/config.json` on first setup, fills any missing required project keys with their defaults on later runs, and stamps `rota.version`. It writes no machine or taste key, so those come from the global config or the defaults. It never overwrites a value you set.
- **`rota config show`** and **`rota config set`** read and change keys later. `rota config show --json` adds each key's `type`, `group`, `desc` and `choices`.
- **`rota config edit`** opens the config screen in a terminal (also `rota config --ui`): keys grouped by section with a detail pane, booleans toggle, enum keys pick from their choices, the rest take a typed value checked as `config set` checks it. See [configuration](../usage/configuration.md#the-config-screen).

The "(Recommended)" tag on each option marks the default `rota init` writes.

## rota config invocation shapes

`rota config` takes positional arguments:

| Shape | Behavior |
|-------|----------|
| `rota config show` | Prints every key, value, source layer (`local`, `project`, `global` or `default`) and scope. |
| `rota config show <key>` | Prints one key. |
| `rota config set <key> <value>` | Writes the layer the key's scope names: `.rota/config.local.json` for a machine key, `.rota/config.json` for a project or taste key. `--local`, `--project` and `--global` pick the layer instead. |

Values for list and object keys are JSON (`rota config set round.accounts '[...]'`). A key outside the schema exits 2 and nothing is written. The value is not checked against the allowed values on write, except for the strict enums (`autonomy.level`, `work.contextGuard`, `plan.criticHarness`); `rota config check` flags a missing required key, a key the schema does not know, an `autonomy.level` outside `off`, `roundtrip` and `auto`, and a legacy `refactor.verifyCommands` that still holds commands while `test.full` is empty.

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
| Roundtrip | One item start to finish: brainstorm (when the planning dial calls for it), plan, work, review and ship chain with no process questions, then stop. |
| Auto | Keeps taking the next ready item after each ship, and a round runs the autopilot tick. Stops only for a human-reserved decision, no recommendation, no ready item or a session limit. |

Manual gates and `ship.mergeApproval` hold at every level. `autonomy.level: "loop"` was removed; `rota config check` fails on it and on any value other than `off`, `roundtrip` and `auto`. Before 0.21.0, `auto` meant one-hop chaining: `rota config fill` rewrites an `auto` in a config stamped older than 0.21.0 (and in a global config whose `config.version` stamp file is missing or older) to `roundtrip` once (see [Removed keys](#removed-keys) for `round.autopilot`). See [Autonomy](../usage/autonomy.md).

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
| Q5 Roundtrip | `autonomy.level: "roundtrip"` |
| Q5 Auto | `autonomy.level: "auto"` |

## Keys

Every key, by group. Scope is the layer the key belongs in, with the file `rota config set` writes it to by default; Default is what a missing key reads as; Values lists the allowed values of an enum key.

### orchestrator

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `orchestrator.model` | machine (local) | string | `"opus"` |  | Model for planning, exploration, verification and design. Usually opus, sonnet or haiku. |
| `orchestrator.command` | machine (local) | string | unset |  | Rounds: Command that starts the orchestrator. Empty builds claude --model <orchestrator.model> --permission-mode auto (with --continue for rota worker session ensure). |
| `orchestrator.handoffThreshold` | machine (local) | int | `75` |  | Context percentage, 1 to 100, at which the Stop hook blocks until the orchestrator writes a handoff. |
| `orchestrator.restartPrompt` | machine (local) | string | `"Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round."` |  | Text appended as the last argument of a restart. Must not be empty. |
| `orchestrator.escalateIssue` | machine (local) | int | `0` |  | Issue number, or key like PROJ-123 on a key-based tracker, the breaker's escalation comment goes on. 0 leaves it unset: a host notification and a warning only. |
| `orchestrator.harness` | machine (local) | enum | `"claude"` | `claude`, `codex`, `hermes`, `opencode` | Which agent rota orchestrate, and bare rota in an initialized project, starts as the orchestrator. |
| `orchestrator.switchOnUsage` | machine (local) | bool | `false` |  | Opt-in: block the Stop hook for a handoff when usage reaches orchestrator.usageThreshold, so the supervisor restarts under another account. |
| `orchestrator.usageThreshold` | machine (local) | int | `90` |  | Percent of the 5-hour or weekly limit, 1 to 100, at which orchestrator.switchOnUsage triggers. |

### work

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `work.isolation` | taste (tracked) | enum | `"branch"` | `branch`, `worktree` | /rota-work: How a cycle isolates changes from main: a feature branch in this checkout, or a separate git worktree. |
| `work.mergeStrategy` | taste (tracked) | enum | `"direct"` | `direct`, `pr` | /rota-work: How finished work lands: merged straight into the base branch with --no-ff, or through a pull request. Ignored when issues.source is a tracker (always pr) or the forge is local (always direct); rota ship strategy prints the answer. |
| `work.tdd` | project (tracked) | bool | `true` |  | /rota-work: Whether a cycle needs a recorded red-first run before a behavior change; round workers follow it too. Off skips the RED requirement. |
| `work.contextGuard` | taste (tracked) | enum | `"warn"` | `warn`, `off`, `auto` | /rota-work: What a session that is not an orchestrator does at orchestrator.handoffThreshold, where rota hooks are installed. warn adds one line to the next prompt recommending /rota-pause; auto also has the Stop hook ask for /rota-pause at the next task boundary, a couple of times; off does nothing. |

### round

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `round.dispatch` | machine (local) | enum | `"subagent"` | `subagent`, `tmux`, `herdr`, `orca` | Rounds: Where round workers run. subagent means detect: herdr in a herdr pane, tmux in tmux, else in-harness subagents. /rota-work ignores it. |
| `round.slots` | machine (local) | int | `3` |  | Rounds: Number of worker slots rota round start provisions (at least 1). --slots overrides it for one round. |
| `round.commands` | machine (local) | object | `{}` |  | Rounds: Command that starts a worker session, per harness kind (claude, codex or opencode), as round.commands.<kind>. Empty or unset builds the default command for the kind, with --model from the tier; {model} receives the tier's model. An unknown kind is an error. |
| `round.accounts` | machine (local) | list | `[]` |  | Rounds: Logins for worker slots, as {kind, name, dir} objects. kind is claude, codex or opencode and dir the harness's config home (CLAUDE_CONFIG_DIR, CODEX_HOME, OPENCODE_CONFIG_DIR); a leading ~ expands to the home directory. A name stands for one dir: it may repeat across kinds only with the same dir. Layers merge by kind: a layer that lists a kind replaces the lower layers' entries of that kind and inherits the others. Empty: every slot uses the orchestrator's login (Claude) or the harness's default home. Machine-specific, so set it in the global config or config.local.json. An empty list in the project or local file counts as unset, so the global value applies. |
| `round.scope` | project (tracked) | enum | `"milestone"` | `milestone`, `slate`, `next`, `open` | Rounds: Which issues a round may take. slate: only those named at rota round start --items. milestone: the active milestones. next: also the next ready milestone. open: every open item no slot holds. |
| `round.roster` | project (tracked) | list | `["ben", "dana", "nia", "kit"]` |  | Rounds: Agent names slots are provisioned under, one slot each. Lowercase letters, digits and -, no duplicates. |
| `round.brief` | machine (local) | path | unset |  | Rounds: Path of the standing worker contract the assignment pointer names. Empty means skills/references/worker-contract.md in the checkout, else the installed copy. A round.brief missing under rota's skills cache (an update removed it) falls back to the current cache copy with a warning; elsewhere it refuses. |
| `round.sharedPaths` | project (tracked) | list | `[]` |  | Rounds: Repo-relative globs the file-overlap readiness check ignores, for files every issue touches. |
| `round.scopeOverlap` | project (tracked) | enum | `"warn"` | `warn`, `block` | Rounds: What a clash on declared scopes (the ## Touches section, else Subsystem) does to the overlap check. warn: reported, the item stays ready. block: fails the check like a shared path; --accept-overlap skips it. |
| `round.adoptPattern` | project (tracked) | string | unset |  | Rounds: Glob over branch names (codex/*, claude/*). rota round reconcile reports a matching branch that no slot holds and that is not merged as unregistered-branch; --apply adopts those whose name carries an issue number. Empty turns the check off. |
| `round.support` | project (tracked) | string | unset |  | Rounds: Name of the support machine (e.g. mac). Non-empty enables a support orchestrator on that machine, and is the prefix of its roster names. Needs an issue tracker. |
| `round.role` | machine (local) | enum | `"main"` | `main`, `support` | Rounds: This machine's role, set in .rota/config.local.json only. support builds machine:<name> issues and opens PRs but never merges or writes tracked .rota/ content. |
| `round.tier` | taste (tracked) | enum | `"standard"` | `light`, `standard`, `heavy` | Rounds: Default worker tier for rota round assign: light for reading, standard for code and tests, heavy for hard reasoning. |
| `round.harnesses` | machine (local) | list | `[]` |  | Rounds: Worker harnesses a round may use, in quota-fallback order, e.g. [claude, codex]. The first entry is the default kind; --kind and an issue's harness: label beat it. When every account of an issue's harness is cooling down, the next harness with headroom takes it, labelled harness:<kind> (file backend: the Harness: field). --kind, a harness: label and a model: label or --model never fall back. Empty: the slot's recorded kind, else claude. |
| `round.profile` | machine (local) | enum | unset | `quality`, `balanced`, `budget`, `inherit` | Rounds: Named model profile that fills round.tiers.claude.* (light/standard/heavy). quality: sonnet/opus/opus. balanced: haiku/sonnet/opus. budget: haiku/haiku/sonnet. inherit: no model, workers launch without --model. Empty keeps the tier defaults. An explicit round.tiers.claude.<tier> beats the profile; Codex and OpenCode tiers are not touched. |
| `round.tiers.claude.light` | machine (local) | string | `"haiku"` |  | Rounds: Model a light-tier Claude worker starts with. |
| `round.tiers.claude.standard` | machine (local) | string | `"sonnet"` |  | Rounds: Model a standard-tier Claude worker starts with. An explicit value beats round.profile. |
| `round.tiers.claude.heavy` | machine (local) | string | `"opus"` |  | Rounds: Model a heavy-tier Claude worker starts with. |
| `round.tiers.codex.light` | machine (local) | string | `"gpt-6-luna"` |  | Rounds: Model a light-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three. |
| `round.tiers.codex.standard` | machine (local) | string | `"gpt-6.1-sol"` |  | Rounds: Model a standard-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three. |
| `round.tiers.codex.heavy` | machine (local) | string | `"gpt-6-astra"` |  | Rounds: Model a heavy-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three. |
| `round.tiers.opencode.light` | machine (local) | string | `"opencode/claude-haiku-5-5"` |  | Rounds: Model a light-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three. |
| `round.tiers.opencode.standard` | machine (local) | string | `"opencode/claude-sonnet-5-5"` |  | Rounds: Model a standard-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three. |
| `round.tiers.opencode.heavy` | machine (local) | string | `"opencode/claude-opus-5-5"` |  | Rounds: Model a heavy-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three. |
| `round.claimStaleMinutes` | project (tracked) | int | `240` |  | Rounds: Minutes without branch or PR activity before a bug, task or Minor/Cosmetic feature claim is stale. 0 disables stale detection for this class. |
| `round.claimStaleMinutesMajor` | project (tracked) | int | `480` |  | Rounds: Minutes without branch or PR activity before a Major feature claim is stale. 0 disables stale detection for this class. |
| `round.stallMinutes` | project (tracked) | int | `30` |  | Rounds: Minutes a slot with a live agent may show no commit, edit or state change before rota round reconcile reports it stalled. 0 turns the check off. |
| `round.maxBounces` | project (tracked) | int | `3` |  | Rounds: How often rota worker gate may send one item's PR back before it parks the item as needs-human. 0 turns the cap off. |
| `round.freshAt` | project (tracked) | int | `2` |  | Rounds: Bounce count at which rota round review-relay stops resuming the worker and transfers the branch to a free slot at the same tier, with a brief of the open findings only. Must stay below round.maxBounces to act. 0 turns it off. |
| `round.ledgerKeep` | project (tracked) | int | `0` |  | Rounds: Rounds of the round ledger (.rota/ledger.jsonl) to keep. A new round trims the entries of older rounds. 0 keeps everything. |
| `round.architectureEvery` | project (tracked) | int | `20` |  | Rounds: Closed non-refactor items between automatic architecture reviews. 0 turns them off. |
| `round.architectureAreas` | project (tracked) | list | `[]` |  | Rounds: Areas an architecture review is split into, one review item each. Empty means the subsystem map's names, else one whole-repo review. |
| `round.architectureIdleMinItems` | project (tracked) | int | `5` |  | Rounds: Closed non-refactor items since the last review before an idle slot with no ready candidate (queue-empty) triggers one. 0 drops the condition. A slate round never fires queue-empty. |
| `round.architectureIdleMinHours` | project (tracked) | int | `24` |  | Rounds: Hours since the last review before the queue-empty trigger may fire again. 0 drops the condition. Applies only where a review time is recorded (issue mode); file mode records none, so the floor never holds a review back there. |
| `round.autopilotCap` | taste (tracked) | int | `3` |  | Rounds: Most assigns, and most merges, one autopilot tick does. 0 means the default. |
| `round.trainMax` | project (tracked) | int | `0` |  | Rounds: Ready PR count that makes a train due, and most PRs per train. 0 disables this threshold; both train thresholds 0 keep immediate autopilot merges and nudges at two reviewed PRs. Without an age threshold a lone ready PR can wait indefinitely. |
| `round.trainFillMinutes` | project (tracked) | int | `0` |  | Rounds: Minutes since the oldest PR became ready that makes a train due, including a lone PR. Uses current-head reviews, or worker done time when review is disabled; a missing done time lands immediately. 0 disables this threshold. |
| `round.batch` | project (tracked) | int | `0` |  | Rounds: Most items a round may have assigned and not yet landed or parked at once. rota round assign refuses the next one past it and rota round tick assigns none. 0 means no limit. |
| `round.bundleMax` | project (tracked) | int | `3` |  | Rounds: Most member issues rota round assign --bundle may hand one slot beside the lead issue. It refuses a bundle past it. 0 means no limit. |
| `round.reviewLoop` | taste (tracked) | enum | `"manual"` | `manual`, `auto` | Rounds: What happens when a reviewer comments on a finished worker's PR. manual reports it and leaves rota round review-relay to the orchestrator; auto relays it to the worker as a counted bounce (round.maxBounces) from rota round watch and rota round tick. |
| `round.envSetup` | project (tracked) | string | unset |  | Rounds: Shell command rota worker pool init runs in each new slot worktree, for example npm ci. Empty: no setup. |
| `round.sharedDirs` | project (tracked) | list | `[]` |  | Rounds: Dependency directories, relative to the repo root, that rota symlinks from the main checkout into each slot worktree, for example node_modules or .venv. rota worker pool init links them and rota round assign re-links them on a parked slot. When the slot's lockfile differs from main's, or a directory is missing in main, no directory is linked and round.envSetup installs the slot's own. List the directories in .gitignore. Empty: no sharing. |
| `round.portBase` | machine (local) | int | `20000` |  | Rounds: First port of the range rota hands out to worker slots. A new slot takes the lowest range of portBlock ports from here that no other slot holds, exported as ROTA_PORT_BASE; a slot keeps its range until it is reaped or reclaimed. |
| `round.portBlock` | machine (local) | int | `100` |  | Rounds: Ports reserved per worker slot, so servers started by two slots never collide. A slot's block is exported as ROTA_PORT_BASE. |
| `round.herdrGroupSlots` | machine (local) | bool | `true` |  | Rounds: herdr only, on by default: open each worker slot's worktree as a workspace linked to the orchestrator's, so herdr groups the slots under their project; false keeps each slot as a tab in the orchestrator's workspace. `rota layout tabs` puts the slots back in their workspaces. |
| `round.itemTimeoutMinutes` | project (tracked) | int | `0` |  | Rounds: Minutes one item may run from its first assignment before rota round reconcile reports it as timed out and --apply parks it as needs-human. 0 means no cap. |

### refactor

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `refactor.confirmBeforeExecute` | taste (tracked) | bool | `true` |  | Whether /rota-refactor --fix confirms the candidate list before implementing it. Off means no pause. |

### learn

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `learn.verify` | taste (tracked) | bool | `false` |  | Whether /rota-learn always runs a fresh-context verifier on the entries it just wrote. |
| `learn.promoteThreshold` | project (tracked) | int | `3` |  | Confidence threshold at which the knowledge lifecycle auto-promotes an entry. Integer, 0 or more. |

### ship

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `ship.review` | taste (tracked) | enum | `"full"` | `full`, `light`, `none` | How deep a review /rota-ship runs first: full, light (the Spec reviewer only, on the standard tier; the Standards reviewer when the branch references no issue) or none. FAIL blocks, CONCERNS ask, PASS flows through. An object {default, lightBelow, labels, paths, scopeDrift, supportPaths} picks the depth by diff size, label and path, and lifts light to full on a Door: one-way PR or a diff outside the issue's files (docs, tests and golden records beside them don't count); true and false still mean full and none. |
| `ship.secondOpinion` | taste (tracked) | bool | `false` |  | Opt-in fresh-eyes adversarial gate in /rota-ship Step 3.5. |
| `ship.qa` | taste (tracked) | bool | `false` |  | Opt-in product-QA gate: /rota-ship runs /rota-qa run after review and before merge or PR. |
| `ship.uat` | taste (tracked) | bool | `false` |  | Opt-in human acceptance walk-through: /rota-ship runs /rota-qa uat after review and before merge or PR. Never part of the round merge gate. |
| `ship.mergeApproval` | project (tracked) | enum | `"none"` | `none`, `all`, `paths` | Which merges need a human: none, all, or only those touching ship.mergeApprovalPaths. The merge verbs enforce it at every autonomy level. |
| `ship.mergeApprovalPaths` | project (tracked) | list | `[]` |  | Repo-relative paths or globs that need human approval when ship.mergeApproval is paths. |

### qa

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `qa.gate` | taste (tracked) | enum | `"advisory"` | `advisory`, `blocking` | How /rota-ship treats a /rota-qa verdict. advisory surfaces findings and never blocks; blocking halts the ship on FAIL. |
| `qa.afterWork` | taste (tracked) | bool | `false` |  | Whether /rota-work runs /rota-qa after a cycle when touched files match a QA target's watch globs. |

### autonomy

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `autonomy.level` | taste (tracked) | enum | `"off"` | `off`, `roundtrip`, `auto` | How much rota chains on its own, in solo skills and rounds. off: skills suggest the next step and stop. roundtrip: one item runs brainstorm to ship with no process questions, then stops. auto: keeps taking the next ready item, and a round runs the autopilot tick; it stops only for a human-reserved decision, no recommendation, no ready item or a session limit. Manual gates and ship.mergeApproval hold at every level. |

### docs

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `docs.path` | project (tracked) | path | `"docs"` |  | Project-relative documentation folder that /rota-docs reads and writes. |
| `docs.autoCreate` | taste (tracked) | bool | `false` |  | Whether the after-work docs flow writes proposed updates without waiting for approval. |
| `docs.afterWork` | taste (tracked) | bool | `false` |  | Whether /rota-work, /rota-ship and /rota-release run the docs after-work flow when their primary action finishes. |

### git

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `git.baseBranch` | project (tracked) | string | unset |  | Base branch the skills merge into and diff against. Empty auto-detects it. |

### umbrella

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `umbrella.enabled` | project (tracked) | bool | `false` |  | Umbrella mode: .rota/ stays at the umbrella and verbs operate per registered sub-repo. Turning it off keeps .rota/repos.json. |

### rota

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `rota.version` | project (tracked) | string | unset |  | Release of rota that wrote this config. Managed by rota init and rota update; do not set it by hand. |

### issues

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `issues.labels.inProgress` | project (tracked) | string | `"in-progress"` |  | Tracker label for an item a worker holds. |
| `issues.labels.needsReview` | project (tracked) | string | `"needs-review"` |  | Tracker label for an item whose work awaits review. |
| `issues.labels.changesRequested` | project (tracked) | string | `"changes-requested"` |  | Tracker label for an item sent back with review feedback. |
| `issues.labels.released` | project (tracked) | string | `"released"` |  | Tracker label for an item that shipped in a release. |
| `issues.labels.notPlanned` | project (tracked) | string | `"not-planned"` |  | Tracker label for an item closed as not planned. |
| `issues.labels.blocked` | project (tracked) | string | `"blocked"` |  | Label rota item complete --reason blocked sets; the issue stays open. |
| `issues.labels.milestoneTracker` | project (tracked) | string | `"milestone-tracker"` |  | Tracker label for a milestone's tracking issue. |
| `issues.labels.types.bug` | project (tracked) | string | `"type:bug"` |  | Tracker label for the bug item type. |
| `issues.labels.types.feature` | project (tracked) | string | `"type:feature"` |  | Tracker label for the feature item type. |
| `issues.labels.types.task` | project (tracked) | string | `"type:task"` |  | Tracker label for the task item type. |
| `issues.labels.priorityPrefix` | project (tracked) | string | `"p"` |  | Prefix of priority labels. |
| `issues.labels.sizePrefix` | project (tracked) | string | `"size:"` |  | Prefix of feature size labels, such as size:Major. |
| `issues.autoCreateLabel` | project (tracked) | bool | `true` |  | Create a tracker label the first time something asks for it. When off, adding a missing label fails. |
| `issues.homeRepo` | project (tracked) | string | unset |  | Umbrella mode with the issues backend only: the registered sub-repo that holds milestone tracking issues. Empty means the first registered sub-repo. |
| `issues.source` | project (tracked) | enum | `"file"` | `file`, `github`, `gitlab`, `forge`, `jira`, `linear`, `itsaplan` | Where issues live: BACKLOG.md in the repo (file), or a tracker. github and gitlab must match the forge. forge means each repo's own forge, for an umbrella whose sub-repos sit on different hosts. jira reads issues.connection and issues.jira.project; linear reads issues.connection and issues.linear.team; itsaplan reads issues.connection and issues.itsaplan.project. Switch with rota migrate issues. |
| `issues.labels.needsHuman` | project (tracked) | string | `"needs-human"` |  | Label rota round transfer --to human puts on an issue handed to the human. rota round candidates skips an issue that carries it. |
| `issues.connection` | project (tracked) | string | unset |  | Name of the machine-wide tracker connection the issues backend uses for Jira, Linear or It's a Plan, added with rota connect add. Holds a name only, never a secret. A teammate whose connection has another name overrides it in config.local.json. |
| `issues.jira.project` | project (tracked) | string | unset |  | Jira project key, such as ROTA, that issues.connection reads from and writes to. |
| `issues.linear.team` | project (tracked) | string | unset |  | Linear team key, such as ROT, that issues.connection reads from and writes to. |
| `issues.itsaplan.project` | project (tracked) | string | unset |  | It's a Plan project key, such as MKT, that issues.connection reads from and writes to. |
| `issues.labels.extra` | project (tracked) | object | `{}` |  | Project tracker labels as name to description. Label sync treats these as known; registry labels retain their own metadata. |

### release

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `release.checklistPath` | project (tracked) | path | `".rota/RELEASE.md"` |  | Project's release checklist, whose open items /rota-release walks before it tags. |
| `release.confirmLargePushCommits` | project (tracked) | int | `10` |  | Unpushed commits /rota-release pushes without asking under autonomy.level roundtrip or auto. At or above this it asks once. |
| `release.nudgeAfterCommits` | project (tracked) | int | `10` |  | Commits since the last release tag before rota release pending suggests /rota-release. |
| `release.nudgeAfterDays` | project (tracked) | int | `14` |  | Days since the last release tag before rota release pending suggests /rota-release. |
| `release.versionFile` | project (tracked) | path | unset |  | Project-relative file /rota-release and rota release version read and bump. Empty auto-detects; a path outside the project is refused. |

### test

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `test.fast` | project (tracked) | list | `[]` |  | Shell commands for the quick per-task and worker checks. {files} expands to the changed files, single-quoted; put -- before it. |
| `test.full` | project (tracked) | list | `[]` |  | Shell commands for the full suite, run by rota worker gate and the merge train on the merged tree. Empty with test.e2e also empty: the gate refuses to merge without --no-verify. |
| `test.e2e` | project (tracked) | list | `[]` |  | Shell commands for slow end-to-end checks, run by the gate and merge train after test.full passes. Empty skips the step. |
| `test.fullWhere` | project (tracked) | enum | `"local"` | `local`, `ci` | Where the gate and train run the full tier: here, or by pushing the merge result and waiting for the forge's checks (needs test.ciChecks). |
| `test.vacuousCheck` | project (tracked) | bool | `false` |  | Refuse tests that still pass after reverting the merge's non-test changes in a disposable worktree. Off by default. |
| `test.filePattern` | project (tracked) | string | `"_test\\.go$"` |  | Regular expression matching test-file paths for test.vacuousCheck. Defaults to Go test files. |
| `test.vacuousCommand` | project (tracked) | string | `"go test -json -count=1 {packages} -run {tests}"` |  | Command for test.vacuousCheck. Shell-quoted {files} selects changed test files; {packages} and {tests} select Go packages and test names. |
| `test.ciTimeoutMinutes` | project (tracked) | int | `60` |  | How long a ci run waits for its checks, 1 to 1440 minutes. ROTA_CI_TIMEOUT (seconds) overrides it for one run. |
| `test.ciChecks` | project (tracked) | list | `[]` |  | Check names that must all succeed on the pushed commit under test.fullWhere ci: GitHub check-run names or commit-status contexts, GitLab job names. |
| `test.slots` | machine (local) | int | `1` |  | How many heavy test runs may hold the machine-wide test lock at once, 1 or more. The merge gate, rota test run and rota proof record queue on it; a run that finds every slot taken waits and says so on stderr. |
| `test.isolate` | project (tracked) | bool | `true` |  | Whether rota test run scrubs HERDR_*, TMUX*, ssh-agent variables and pins HOME and XDG_* to a temp root. The merge gate and train do not read it. |

### forge

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `forge.provider` | project (tracked) | enum | `"auto"` | `auto`, `github`, `gitlab`, `local` | Where code lands: pull requests, checks and releases. auto reads the origin remote and is local with no remote; local means direct merge, no PR. Set it by hand only when detection misses (self-hosted GitLab without gitlab in the host, SSH host aliases, mirrors). |

### review

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `review.fileDeferred` | taste (tracked) | enum | `"major"` | `minor`, `major`, `off` | Which deferred review findings rota verdict add --file-deferred files, as one follow-up item per PR. minor: minor and major. major: only major (minor stays in the verdict record). off: none. A PR that closes a deferred-findings item files none. |
| `review.crossLabels` | project (tracked) | list | `["size:Major", "risk:high"]` |  | Issue labels that make a PR need a cross-harness review: one read-only review on a harness other than the author's, recorded as a review-cross verdict the merge gate requires. Applies only when round.harnesses is set. A review:<harness> label (claude or codex) picks the reviewer, review:none skips it. Empty: no PR needs one. |
| `review.lenses` | project (tracked) | object | `{}` |  | Review lenses: per lens (security, perf, ux), the path globs and issue labels that make /rota-review run one focused reviewer on that axis, as {"security": {"paths": ["internal/git/**"], "labels": ["risk:security"]}}. Globs allow * within a segment and a trailing /** for a directory. The merge gate requires a review-<lens> verdict at head for each triggered lens. An unknown lens or a malformed glob is an error. Empty: no lens triggers. |

### roles

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `roles.explorer.tier` | machine (local) | enum | `"light"` | `light`, `standard`, `heavy` | Tier of the rota-explorer agent, which picks its model from round.tiers. |
| `roles.explorer.effort` | machine (local) | enum | unset | `low`, `medium`, `high`, `xhigh`, `max` | Reasoning effort of the rota-explorer agent. Empty leaves the harness default; Codex has no max. |
| `roles.implementer.tier` | machine (local) | enum | `"standard"` | `light`, `standard`, `heavy` | Tier of the rota-implementer agent, which picks its model from round.tiers. |
| `roles.implementer.effort` | machine (local) | enum | unset | `low`, `medium`, `high`, `xhigh`, `max` | Reasoning effort of the rota-implementer agent. Empty leaves the harness default; Codex has no max. |
| `roles.reasoner.tier` | machine (local) | enum | `"heavy"` | `light`, `standard`, `heavy` | Tier of the rota-reasoner agent, which picks its model from round.tiers. |
| `roles.reasoner.effort` | machine (local) | enum | unset | `low`, `medium`, `high`, `xhigh`, `max` | Reasoning effort of the rota-reasoner agent. Empty leaves the harness default; Codex has no max. |

### plan

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `plan.maxTasks` | project (tracked) | int | `3` |  | Tasks a plan may hold before rota plan check warns and suggests a split into dependent items. Advisory: the check still exits 0. 0 turns the warning off. |
| `plan.maxFilesPerTask` | project (tracked) | int | `5` |  | Paths a task's Files: line may name before rota plan check warns about that task. Advisory: the check still exits 0. 0 turns the warning off. |
| `plan.criticHarness` | machine (local) | enum | unset | `claude`, `codex`, `opencode` | Harness that runs the /rota-plan level-4 critic, read-only, with the same brief as the same-harness subagent. Empty: the planning harness runs it. A harness that is not installed, or a failed run, falls back to the same-harness critic with one printed line. Levels 1-3 never read it. |

### map

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `map.staleCommits` | project (tracked) | int | `50` |  | Commits HEAD may be past a map file's last_mapped_commit before rota map stats warns that the subsystem is stale. Advisory: the verb still exits 0. 0 turns the warning off. |
| `map.softcap_subsystems` | project (tracked) | int | `20` |  | Subsystems the project map may name before rota decisions check --cap and /rota-refactor --map nudge to merge or retire stale entries. An integer of 1 or more. |

### brainstorm

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `brainstorm.edgeProbe` | taste (tracked) | bool | `false` |  | Opt-in edge and prohibition probe in /rota-brainstorm Step 3: walks eight edge categories, records each as covered or dismissed, asks what the change must never do and writes each answer as a negative acceptance criterion. Off leaves /rota-brainstorm unchanged. |

### research

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `research.packageCheck` | taste (tracked) | bool | `false` |  | Opt-in registry check in /rota-spike Step 2 and /rota-plan Step 2: each new package they would recommend is looked up on its registry (npm, PyPI, crates.io, Go proxy); missing ones are dropped, suspect ones need a user answer. Makes network calls. Off makes no registry call. |

### limits

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `limits.mode` | machine (local) | enum | `"switch"` | `switch`, `sleep` | How the usage-limit watcher reacts to a limit. switch moves a worker's issue to an idle slot on an account with headroom, else sleeps. sleep always waits for the reset. |
| `limits.resumePrompt` | machine (local) | string | `"The usage limit has reset. Continue where you left off."` |  | Text typed into the limited pane to resume it. Must not be empty. |

### doctor

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `doctor.minFreeDiskPercent` | machine (local) | int | `10` |  | rota doctor warns when the free share of the disk falls below this percent, 0 to 100. 0 turns the check off. The same threshold drives the go-cache-disk warning for the volume holding GOCACHE. |

### knowledge

| Key | Scope | Type | Default | Values | Description |
|-----|-------|------|---------|--------|-------------|
| `knowledge.external` | machine (local) | path | unset |  | Local path of the cloned external knowledge repo (README.md, topics/, rota-knowledge.json) that rota knowledge --external reads and writes, and that /rota-learn and the knowledge consult also use. Machine-specific, so set it in the global config. Empty means the feature is off. |
| `knowledge.externalTTL` | machine (local) | int | `10` |  | Minutes a fetch of the external knowledge clone stays fresh. A read inside the window uses the local copy; past it, rota fetches and fast-forwards first. 0 fetches on every read. |
| `knowledge.externalMaxBullets` | machine (local) | int | `25` |  | Bullets a topic file of the external knowledge repo may hold before rota doctor warns that it has outgrown a cheap read. 0 turns the check off. |
| `knowledge.externalMaxBytes` | machine (local) | int | `8192` |  | Bytes a topic file of the external knowledge repo may hold before rota doctor warns. 0 turns the check off. |
| `knowledge.externalMaxTopics` | machine (local) | int | `60` |  | Topics the external knowledge index may list before rota doctor warns. 0 turns the check off. |

## Removed keys

`issues.filterMineOnly`, `issues.providers.github` and `issues.providers.gitlab` were seeded by older `rota init` runs but nothing ever read them (use `issues.source` and `forge.provider`). `round.autopilot` was retired in 0.21.0: `autonomy.level: "auto"` turns the round autopilot on, and `rota config fill` deletes the key from `.rota/config.json` and `.rota/config.local.json`, saying so when it was `true`. `rota config check` lists any still in `.rota/config.json` as `removed` with a hint and does not fail on them; `rota config fill` deletes them. `rota config set` refuses them as unknown keys.

## Legacy keys

`backlog.backend` and `issues.provider` were replaced by `issues.source` (where the issues live) and `forge.provider` (where code lands). `rota migrate source` rewrites them: `backlog.backend: file` becomes `issues.source: file`; `backlog.backend: issues` plus `issues.provider` becomes `issues.source: <provider>`, with `auto` read from the origin remote; an explicit provider the origin would miss also becomes `forge.provider`. It removes both keys and a second run changes nothing. `rota config fill` and `rota update` run the same migration. While a config still holds the legacy keys they keep deciding, so an upgraded project never flips to the file source, and `rota config check` lists them as `legacy`. An umbrella whose sub-repos sit on different hosts migrates to `issues.source: forge` (each sub-repo on its own forge). When `issues.provider` is `auto` and the origin names neither GitHub nor GitLab, the keys stay until you set `issues.source` by hand.

## Validation rules

- **`rota config set`** checks only that the key is in the schema. It accepts any value (JSON when it parses, else the string) and checks no enum or range.
- **Enums without a reader check.** `models.*` (`opus`, `sonnet` or `haiku`), `work.isolation` (`branch` or `worktree`) and `work.mergeStrategy` (`direct` or `pr`) are not validated by any verb. The skills and the launch command read them as plain strings, so a bad value surfaces as that skill's or the model's failure. `rota config check` only reports a missing key, an `autonomy.level` that is not `off`, `roundtrip` or `auto`, or a `refactor.verifyCommands` that still holds commands `test.full` lacks (run `rota config fill` to move them).
- **Checked where a verb reads them.** A bad value exits 70 (`rota round` verbs for the `round.*` keys, `rota keepalive` and `rota limit` for the `orchestrator.*` keepalive and switch keys and the `limits.*` keys, and any verb that reads `issues.source` or `forge.provider`). The checks are these: booleans take `true` or `false`; integers must be in the minimum-to-maximum range stated in the key's description; `round.scope` and `limits.mode` take their listed enums; `round.roster` entries are unique lowercase names. `ship.mergeApproval` is also checked at read time, but a bad value there exits 2.

For the full per-key behavior (defaults, value semantics, and how each setting affects skill execution), see [`usage/configuration.md`](../usage/configuration.md).
