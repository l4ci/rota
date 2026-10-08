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

{{KEYS}}
## Removed keys

`issues.filterMineOnly`, `issues.providers.github` and `issues.providers.gitlab` were seeded by older `rota init` runs but nothing ever read them (use `issues.provider` to pick the tracker). `rota config check` lists any still in `.rota/config.json` as `removed` with a hint and does not fail on them; `rota config fill` deletes them. `rota config set` refuses them as unknown keys.

## Validation rules

- **`rota config set`** checks only that the key is in the schema. It accepts any value (JSON when it parses, else the string) and checks no enum or range.
- **Enums without a reader check.** `models.*` (`opus`, `sonnet` or `haiku`), `work.isolation` (`branch` or `worktree`), `work.mergeStrategy` (`direct` or `pr`) and `autonomy.level` (`off` or `auto`) are not validated by any verb. The skills and the launch command read them as plain strings, so a bad value surfaces as that skill's or the model's failure. `rota config check` only reports a missing key, the removed `autonomy.level` `"loop"`, or a `refactor.verifyCommands` that still holds commands `test.full` lacks (run `rota config fill` to move them).
- **Checked where a verb reads them.** A bad value exits 70 (`rota round` verbs for the `round.*` keys, `rota keepalive` and `rota limit` for the `orchestrator.*` keepalive and switch keys and the `limits.*` keys, and any verb that reads `backlog.backend`). The checks are these: booleans take `true` or `false`; integers must be in the minimum-to-maximum range stated in the key's description; `round.scope` and `limits.mode` take their listed enums; `round.roster` entries are unique lowercase names. `ship.mergeApproval` is also checked at read time, but a bad value there exits 2.

For the full per-key behavior (defaults, value semantics, and how each setting affects skill execution), see [`usage/configuration.md`](../usage/configuration.md).
