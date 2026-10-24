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

{{KEYS}}
## Removed keys

`issues.filterMineOnly`, `issues.providers.github` and `issues.providers.gitlab` were seeded by older `rota init` runs but nothing ever read them (use `issues.source` and `forge.provider`). `round.autopilot` was retired in 0.21.0: `autonomy.level: "auto"` turns the round autopilot on, and `rota config fill` deletes the key from `.rota/config.json` and `.rota/config.local.json`, saying so when it was `true`. `rota config check` lists any still in `.rota/config.json` as `removed` with a hint and does not fail on them; `rota config fill` deletes them. `rota config set` refuses them as unknown keys.

## Legacy keys

`backlog.backend` and `issues.provider` were replaced by `issues.source` (where the issues live) and `forge.provider` (where code lands). `rota migrate source` rewrites them: `backlog.backend: file` becomes `issues.source: file`; `backlog.backend: issues` plus `issues.provider` becomes `issues.source: <provider>`, with `auto` read from the origin remote; an explicit provider the origin would miss also becomes `forge.provider`. It removes both keys and a second run changes nothing. `rota config fill` and `rota update` run the same migration. While a config still holds the legacy keys they keep deciding, so an upgraded project never flips to the file source, and `rota config check` lists them as `legacy`. An umbrella whose sub-repos sit on different hosts migrates to `issues.source: forge` (each sub-repo on its own forge). When `issues.provider` is `auto` and the origin names neither GitHub nor GitLab, the keys stay until you set `issues.source` by hand.

## Validation rules

- **`rota config set`** checks only that the key is in the schema. It accepts any value (JSON when it parses, else the string) and checks no enum or range.
- **Enums without a reader check.** `models.*` (`opus`, `sonnet` or `haiku`), `work.isolation` (`branch` or `worktree`) and `work.mergeStrategy` (`direct` or `pr`) are not validated by any verb. The skills and the launch command read them as plain strings, so a bad value surfaces as that skill's or the model's failure. `rota config check` only reports a missing key, an `autonomy.level` that is not `off`, `roundtrip` or `auto`, or a `refactor.verifyCommands` that still holds commands `test.full` lacks (run `rota config fill` to move them).
- **Checked where a verb reads them.** A bad value exits 70 (`rota round` verbs for the `round.*` keys, `rota keepalive` and `rota limit` for the `orchestrator.*` keepalive and switch keys and the `limits.*` keys, and any verb that reads `issues.source` or `forge.provider`). The checks are these: booleans take `true` or `false`; integers must be in the minimum-to-maximum range stated in the key's description; `round.scope` and `limits.mode` take their listed enums; `round.roster` entries are unique lowercase names. `ship.mergeApproval` is also checked at read time, but a bad value there exits 2.

For the full per-key behavior (defaults, value semantics, and how each setting affects skill execution), see [`usage/configuration.md`](../usage/configuration.md).
