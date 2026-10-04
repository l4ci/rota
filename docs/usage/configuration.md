# Configuration

All settings live in [`.rota/config.json`](../reference/rota-folder.md). Run `rota config show` to list every key with its value and source, and `rota config set <key> <value>` to change one (positional arguments; see [config options](../reference/config-options.md)). `rota init` fills any missing key with its default. Don't hand-edit the file.

For the allowed values and option labels of each key, see [Configuration options](../reference/config-options.md).

Default config:

```json
{
  "models": {
    "orchestrator": "opus",
    "worker": "sonnet"
  },
  "work": {
    "isolation": "branch",
    "mergeStrategy": "direct"
  },
  "refactor": {
    "confirmBeforeExecute": true
  },
  "learn": {
    "verify": true
  },
  "ship": {
    "review": true
  },
  "autonomy": {
    "level": "off"
  },
  "debug": {
    "competingHypotheses": false
  },
  "docs": {
    "path": "docs",
    "autoCreate": false
  },
  "git": {
    "baseBranch": ""
  },
  "rota": {
    "version": ""
  }
}
```

## models: orchestrator and worker

`models.orchestrator` drives planning, exploration, verification, and design. `models.worker` handles implementation sub-tasks. Set them independently to trade off quality against cost and speed.

| Value | Best for |
|-------|----------|
| `"opus"` | Deep reasoning: planning, exploration, verification, design |
| `"sonnet"` | Fast execution of well-specified tasks |
| `"haiku"` | Quick, cheap fixes and small tasks |

`rota init` writes the Balanced profile. The four profiles (Balanced, Premium, Fast, Minimal) are listed in [config options](../reference/config-options.md); set both keys to switch.

## work.isolation: branch or worktree

Controls where skill work happens.

| Mode | How it works | When to use |
|------|-------------|-------------|
| `"branch"` | Feature branch in current worktree | Solo work, simple workflows |
| `"worktree"` | Isolated directory under `.claude/worktrees/` | Parallel work streams, keep main clean while agents work |

Switch to `"worktree"` when you want multiple work streams in flight without context bleeding between them. See [parallel work](parallel-work.md) for the full pattern.

## work.mergeStrategy: direct or pr

Controls how [`/rota-ship`](review-and-ship.md) integrates completed work.

| Strategy | How it works | When to use |
|----------|-------------|-------------|
| `"direct"` | Merge to main, delete branch | Solo work, fast iteration |
| `"pr"` | Push branch, create GitHub PR | Team work, code review required |

## work.dispatch: subagent, tmux or herdr

`/rota-work` always runs in-process `Agent` workers that write files while the orchestrator commits, whatever this key says. Standing workers in tmux windows or herdr tabs (each its own Claude Code session in its own worktree, opening a PR behind a merge gate) belong to [`/rota-orchestrate`](parallel-rounds.md); this key only chooses their host.

| Mode | Effect on a round |
|------|-------------------|
| `"subagent"` (default) | `rota round start` detects the host: herdr inside a herdr pane, tmux inside tmux, otherwise solo. |
| `"tmux"` | One tmux window per worker. Needs a `tmux` binary and a working `claude` on `PATH`. |
| `"herdr"` | One herdr tab per worker in the orchestrator's workspace; herdr reports each worker's state and notifies when one needs you. The orchestrator must run inside a herdr pane. |

```bash
rota config set work.dispatch tmux
```

**Parallel rounds (`rota round`) pick their host differently, so a round in herdr needs no setting.** With `work.dispatch` unset or `subagent` (the default `rota init` writes), `rota round start` detects one: herdr when it runs inside a herdr pane, tmux when it runs inside tmux, and otherwise **solo mode**, where the orchestrator runs each worker as a Claude `Agent` subagent in the slot's own worktree. An explicit `tmux` or `herdr` is used as set, and fails if unavailable rather than falling back. The round records its host when it starts and keeps it until wind-down. Solo workers share the orchestrator's account, rate window and context, so one usage limit stops them all. Solo mode runs Claude workers only: a Codex subagent cannot be given a working directory. See [Parallel rounds](parallel-rounds.md).

Two things behave differently under `tmux` and `herdr` than in `/rota-work`:

- **`work.isolation` stops applying.** Every slot has its own worktree, so its own git index, by construction.
- **Workers commit.** The orchestrator's per-task commit step is skipped; integration happens through the merge gate instead, which re-verifies the *merged* tree. Two workers can each be honestly green and still break the cycle branch together — a signature one widens while another adds a caller, a constant one stops emitting while another starts reading it. Nothing about a clean merge rules that out, which is why the gate runs `refactor.verifyCommands` after every merge rather than trusting the branches.

Related keys: `work.workerSlots` (pool size, default `3`), `work.workerCommand` (default builds `claude --model <models.worker> --dangerously-skip-permissions`), `work.codexCommand` (the same for Codex workers; default builds `codex --model <model> --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen`), `work.operatorCommand` (`/rota-work` under tmux only; default builds `claude --continue --model <models.orchestrator> --permission-mode auto`; a round's orchestrator is started by you and restarted by `rota keepalive run`), and `work.accounts`.

**Workers run with permissions skipped; the operator does not.** A worker is briefed to commit, open a PR and run tests with nobody in its pane to answer a prompt, so a narrower mode just stalls it. What bounds a worker is scope rather than gating — a throwaway branch in its own worktree, with `rota worker gate` re-verifying the merged tree before anything reaches the cycle branch. The operator keeps `auto` because it performs the merges and it is the window a human is actually watching. Narrow either via its config key; a worker that then stops on a prompt reports `NEEDS-PERMISSION` rather than hanging.

## refactor.confirmBeforeExecute

Applies to `/rota-refactor --fix` only; the default findings run files issues and writes no code. When `true` (default), `--fix` confirms the list of candidates before implementing them. Set to `false` to fix without that pause.

## refactor.verifyCommands

Array of shell commands that [`/rota-refactor`](../reference/slash-commands.md#rota-refactor) runs in `--fix` verification as CI-shape gates before committing. Default: `[]` (read-only verification, behavior unchanged).

When non-empty, the `--fix` verifier executes each command in order and refuses to PASS unless every command exits zero. This catches formatter drift, import-sort failures, and type errors locally instead of on push. See [rota #9](https://github.com/l4ci/rota/issues/9) for the motivating incident.

Example for a Python project using ruff + pytest:

```json
"refactor": {
  "confirmBeforeExecute": false,
  "verifyCommands": [
    "uv run ruff check .",
    "uv run ruff format --check .",
    "uv run pytest -q"
  ]
}
```

Commands run from the repo root (or, in umbrella mode, the sub-repo's root). Set via `rota config set` (which parses argv[2] as JSON):

```bash
rota config set refactor.verifyCommands '["uv run ruff check .","uv run ruff format --check ."]'
```

## learn.verify

Controls whether [`/rota-learn`](learning.md) always runs a second-opinion pass on what it just wrote. The verifier is a fresh Opus sub-agent with no session context that reads only the updated `KNOWLEDGE.md` diff. It judges each new bullet on four criteria: durable (not ephemeral), sharp (concrete claim, not vague), correctly topic'd, and non-duplicate. It can demote weak entries, sharpen vague wording, re-file wrong-topic bullets, or delete restatements of existing knowledge.

| Value | Behavior |
|-------|----------|
| `true` | After writing, dispatches the verifier. Catches weak, duplicate, or wrong-topic entries before they accrete in `KNOWLEDGE.md`. Adds one Opus roundtrip per `/rota-learn` call. |
| `false` (default) | Skip the verifier. `/rota-learn` writes and reports. Fast and cheap. Pass `--strict` to `/rota-learn` to run the verifier for one call anyway. |

A weak bullet consulted by 20 future `/rota-work` runs costs more than one extra Opus call. Set `true` if you want every capture checked, or use `--strict` when a capture matters.

See [learning](learning.md) for the full `/rota-learn` workflow.

## learn.promoteThreshold

Controls the F03 knowledge promotion lifecycle: how many `rota knowledge hit` events a `provisional` bullet must accumulate before auto-promotion to `confirmed`. Integer ≥ 0; default `3`.

A "hit" registers when `/rota-work` or `/rota-review` consumes the bullet during a cycle's K+D consult (the bullet shows up in a worker brief's `Known gotchas:` section) AND the user doesn't push back with a correction that overlaps the bullet's body. The threshold is the cycle count after which the project decides the bullet has earned `confirmed` status. At that point `rota knowledge query` drops the `(provisional)` suffix and the bullet flows into consumers indistinguishable from established knowledge.

| Value | Behavior |
|-------|----------|
| `3` (default) | Auto-promote at the third clean hit. Catches durable bullets after a small handful of validated consults. |
| `≥4` | Stricter. Bullets earn `confirmed` only after more validation. Use when you've seen weak bullets sneak through to `confirmed` too quickly. |
| `1` or `2` | Looser. Almost every new bullet auto-promotes on first or second consult. Use when manual `/rota-learn --promote` flow feels heavy. |
| `0` | Auto-promote on `rota knowledge add` itself, effectively disabling the `provisional` tier. Defeats the lifecycle's purpose; included for completeness only. |

Pending contradictions block auto-promotion regardless of hit count. The user must resolve them via `/rota-learn` Step 9 (Demote / Keep / Defer) before the bullet can flow forward.

See [learning](learning.md) for the full `/rota-learn` workflow.

## ship.review

Controls whether `/rota-ship` runs a review pass before integrating.

| Value | Behavior |
|-------|----------|
| `true` (default) | `/rota-ship` runs `/rota-review` before integrating. FAIL blocks, CONCERNS ask, PASS flows through. |
| `false` | `/rota-ship` integrates directly without a review pass. Use when you want raw speed and already reviewed manually. |

See [review and ship](review-and-ship.md) for the full `/rota-ship` workflow.

## ship.secondOpinion

Controls whether `/rota-ship` runs a second adversarial review with a fresh subagent after `/rota-review` passes. `/rota-review` shares the project's context (conventions, KNOWLEDGE, plan) with the work it produced, and a reviewer with that context naturalizes blind spots. A fresh subagent with only the diff + the goal has to reason from scratch and catches what the contextualized reviewer normalized.

| Value | Behavior |
|-------|----------|
| `false` (default) | `/rota-ship` skips the gate. `/rota-review` alone gates merges. |
| `true` | After `/rota-review` returns PASS, `/rota-ship` dispatches a fresh `general-purpose` Sonnet subagent with the goal + diff only (no KNOWLEDGE/DECISIONS/conventions) and an adversarial framing. Returns PASS/CONCERNS/FAIL via the same routing as `/rota-review`. |

The gate is opt-in because it adds one Sonnet roundtrip per ship and most cycles don't need it. Enable when you ship work that touches load-bearing surfaces (release tooling, security paths, data migrations) and want a second pair of eyes that genuinely don't know what they're "supposed to" see.

Same-model-fresh-context is the cheap MVP; cross-model second-opinions (Codex/Gemini/etc.) would be stronger but aren't currently wired.

See [review and ship](review-and-ship.md) for the full `/rota-ship` workflow.

## ship.secondOpinionRunner

Picks who runs the [`ship.secondOpinion`](#shipsecondopinion) gate. Has no effect while `ship.secondOpinion` is `false`.

| Value | Behavior |
|-------|----------|
| `"subagent"` (default) | A fresh Sonnet subagent reviews the goal + diff brief. |

5.0 removed the `"codex"` runner, which ran Codex headlessly. 5.0 has no headless sessions. A config that still says `"codex"` keeps working: `/rota-ship` prints a one-line note and runs the subagent in advisory mode, as the Codex runner did. A FAIL is surfaced and the ship continues. With `"subagent"` (the default) a FAIL stops the ship, routed like `/rota-review`.

## ship.qa

Controls whether `/rota-ship` invokes [`/rota-qa run`](qa.md) between `/rota-review` (and the optional second-opinion gate) and the merge/PR step. `/rota-review` answers *"does this diff make sense"*. `/rota-qa` answers *"does the product actually work"* by executing the per-target strategy in `.rota/qa/<target>.md`.

| Value | Behavior |
|-------|----------|
| `false` (default) | `/rota-ship` skips QA. Diff-level review alone gates merges. |
| `true` | After `/rota-review` (and second-opinion if on), `/rota-ship` calls `/rota-qa run` scoped to the active repo. QA findings route per `qa.gate`. If no strategy file exists for the active scope, `/rota-ship` surfaces a one-line note pointing at `/rota-qa first-run` and proceeds without QA. |

The gate is opt-in because product QA needs strategy files (`/rota-qa first-run` bootstraps them) and often binds to infra (dev server, sandbox creds, runners installed). Most cycles don't need it. Enable for repos that have a QA strategy wired up and where regressions cost more than the runner time.

## ship.mergeApproval and ship.mergeApprovalPaths

Whether a merge needs a human. The merge verbs (`rota ship merge`, `rota ship pr-merge`, `rota worker gate`) read it, so it binds `/rota-ship`, `/rota-review --queue` and the `/rota-work` gate step at every `autonomy.level`. In a round, `rota worker gate` enforces it; see [parallel rounds](parallel-rounds.md).

| `ship.mergeApproval` | Behavior |
|-------|----------|
| `"none"` (default) | Merges run after their own gates (review, proof, verify) with no extra approval. |
| `"all"` | Every merge needs a human. The verb refuses with exit 4 until the skill asks and re-runs it with `--confirm --confirm-note "<answer>"`. |
| `"paths"` | Only merges that change a file matching `ship.mergeApprovalPaths` need a human. The refusal lists the matching files. |

`ship.mergeApprovalPaths` is a list of repo-relative entries. A changed file matches an entry when it equals it, lies under it (`"rota-release"` matches `rota-release/SKILL.md`), or matches it as a glob against the whole path (`"*.md"` matches top-level Markdown only). Set them with `rota config set ship.mergeApproval paths` and `rota config set ship.mergeApprovalPaths '["migrations", "*.lock"]'`. Every approval lands in `.rota/gate-audit.jsonl`; see [`references/manual-gates.md`](../../references/manual-gates.md).

## round keys

Settings for `rota round` (parallel rounds; see [the rounds guide](parallel-rounds.md), [`rota round` verbs](../reference/cli-helpers.md#rota-round) and [`rota doctor`](../reference/preflight.md#rota-doctor)). All are silent defaults; none is written by `rota init`.

| Key | Default | Meaning |
|-----|---------|---------|
| `round.scope` | `"milestone"` | Which issues `rota round assign` accepts. `"slate"`: only the issues given to `rota round start --items`. `"milestone"`: the open items of the active milestones. `"next"`: the same, then the first planned milestone whose dependencies are shipped once none is left. `"open"`: every open item no slot holds. Assign refuses anything outside the scope. |
| `round.roster` | `["ben","dana","nia","kit"]` | Agent names, one slot each (`.worktrees/<agent>`, parked on `park/<agent>`, working on `<agent>/<issue>-<slug>`). Lowercase letters, digits and `-`; no duplicates. |
| `round.brief` | `""` | Path of the standing worker contract the assignment pointer names. Empty means `references/worker-contract.md` from the plugin or project. |
| `round.sharedPaths` | `[]` | Repo-relative globs the file-overlap readiness check ignores, for files every issue touches (a command registry, a contract doc). |
| `round.stallMinutes` | `30` | Minutes without a commit, an uncommitted edit or a state change before `rota round reconcile` reports a slot that holds an issue and has a live agent as `stalled`. `0` turns the check off. A slot waiting on an escalation is never stalled; a dead agent is `dead`, not stalled. |
| `round.maxBounces` | `3` | How often `rota worker gate` may send one item's PR back to its worker (a `stale` or `provenance-fail` verdict on a real run, never `--check-only`) before it parks the item: the slot is freed, the issue gets the `needs-human` label and a comment, and the PR stays open. The count is per item and resets on a pass or a park. `0` turns the cap off. |
| `round.architectureEvery` | `20` | Closed non-refactor items between automatic architecture reviews; `0` turns them off. A review also fires when a slot is idle and nothing is assignable. See `rota round architecture`. |
| `round.architectureAreas` | `[]` | The areas an architecture review is split into, one review item each. Empty means the subsystem map's names, else one whole-repo review. |
| `round.tier` | `"standard"` | Default worker tier: `light` (reading, searching), `standard` (code and tests) or `heavy` (hard reasoning). `rota round assign --tier heavy --tier-reason "…"` goes above it; a tier above the default needs the reason, which lands on the slot. |
| `round.tiers.claude.light` / `.standard` / `.heavy` | `haiku` / `models.worker` / `opus` | The model each tier starts a Claude worker with. `standard` follows `models.worker` (so `/rota-work` and rounds agree) until set explicitly. |
| `round.tiers.codex.light` / `.standard` / `.heavy` | empty | The same for Codex, and optional: unset, a Codex worker runs on Codex's own default model (the default `work.codexCommand` drops `--model`). A kind with any tier set must set all three. `assign --kind codex --check-only` shows the model it would use. |

A custom `work.workerCommand` receives the tier's model only through a `{model}` placeholder in the command; without one, `rota round assign` warns and records the tier but not a model. `work.codexCommand` works the same way for Codex workers.

`round.scope` is a different axis from `autonomy.level`: the level says how far skills chain on their own, the scope says which issues a round may take. Set with `rota config set round.scope slate`.

## orchestrator keys

Settings for the orchestrator handoff (`rota hook stop`, `rota hook session-start`; see [unattended rounds](unattended-rounds.md)) and its restart (`rota keepalive run`; see [unattended rounds](unattended-rounds.md)). All are silent defaults; none is written by `rota init`.

| Key | Default | Meaning |
|-----|---------|---------|
| `orchestrator.handoffThreshold` | `75` | Context percentage (integer, 1 to 100) at which the Stop hook blocks the orchestrator until it has written a handoff. |
| `orchestrator.stateMaxAgeSeconds` | `120` | A statusline reading older than this is not acted on. |
| `orchestrator.handoffMaxAgeSeconds` | `900` | A handoff younger than this counts as fresh: the Stop hook passes, and the SessionStart fallback injects it. |
| `orchestrator.handoffMaxBlocks` | `2` | Times the hook re-blocks a session that still has no handoff, then passes and records `handoffFailed` in the session state. `0` blocks once. |
| `orchestrator.keepaliveMaxRestarts` | `10` | Restarts `rota keepalive run` makes before it stops with `max-restarts` (integer, 0 or more; `0` stops at the first handoff exit). |
| `orchestrator.keepaliveBreaker` | `3` | Restarts in a row that leave no new handoff before the breaker stops the loop (integer, 1 or more). |
| `orchestrator.keepaliveBackoffSeconds` | `5` | Seconds to wait before a restart (integer, 0 or more). |
| `orchestrator.restartPrompt` | `Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round.` | Appended as the last argument of a restart, never of the first start (non-empty string). |
| `orchestrator.harness` | `claude` | The agent `rota orchestrate` starts as the orchestrator: `claude`, `codex`, `hermes` or `opencode`. It runs under `rota keepalive run` with the orchestrate skill as its first prompt. See [parallel rounds](parallel-rounds.md#your-first-round) and [orchestrator harnesses](orchestrator-harnesses.md). |
| `orchestrator.switchOnUsage` | `false` | Opt in to moving the orchestrator to another account before a usage limit: the Stop hook then also blocks for a handoff at `usageThreshold`, and `rota keepalive run` restarts under another account (boolean). See [unattended rounds](unattended-rounds.md). |
| `orchestrator.usageThreshold` | `90` | Percent (integer, 1 to 100) of the 5-hour or weekly window at which the Stop hook asks for that handoff. Read only when `switchOnUsage` is `true`. |
| `orchestrator.escalateIssue` | `0` | Issue number the breaker's escalation comment goes on (integer, 0 or more). `0` is unset: the breaker raises a host notification and a warning only. |

An out-of-range value exits 70 in a verb that reads it (`rota keepalive run` included); the hooks treat it as a pass and never block.

## limits keys

Settings for the usage-limit watcher (`rota limit watch`, and the loop inside `rota keepalive run`; see [unattended rounds](unattended-rounds.md)). All are silent defaults; none is written by `rota init`.

| Key | Default | Meaning |
|-----|---------|---------|
| `limits.mode` | `switch` | `switch` moves a limited worker's issue to an idle slot on another account with headroom, and sleeps when there is none. `sleep` always waits for the reset. The orchestrator always sleeps. |
| `limits.resumeMarginSeconds` | `60` | Seconds after the reset before the resume prompt is typed (integer, 0 or more). |
| `limits.fallbackSleepSeconds` | `1800` | Sleep for a limit whose reset time is unknown (integer, 1 or more). |
| `limits.maxResumes` | `3` | Resume prompts one limit gets before the entry is `failed` and escalated (integer, 1 or more). |
| `limits.resumePrompt` | `The usage limit has reset. Continue where you left off.` | Typed into the limited pane at the reset (non-empty string). |

An out-of-range value exits 70 in a verb that reads it.

## qa.gate

Controls how `/rota-ship` routes a `/rota-qa run` verdict when `ship.qa: true`. Independent of the `/rota-review` verdict routing.

| Value | Behavior |
|-------|----------|
| `"advisory"` (default) | All verdicts surface findings (PASS silently, CONCERNS / FAIL with the `QA concerns:` carrier label) and continue to merge / PR. Advisory means advisory: the ship is never blocked on QA. |
| `"blocking"` | PASS continues silently. CONCERNS branches on `autonomy.level` (`AskUserQuestion` Address / Ship anyway / Stop). FAIL stops the ship; user fixes via `/rota-work` or `/rota-debug` and reruns `/rota-ship`. |

`INFRA-FAIL` (dev server / creds / binary missing) is always treated as advisory regardless of `qa.gate`. Missing infrastructure isn't a quality signal; ship shouldn't break because the dev server happened to be down. The missing requirements surface as a note and the ship continues.

## qa.afterWork

Controls whether `/rota-work` invokes `/rota-qa run` post-cycle when touched files match a target's `Watch globs`.

| Value | Behavior |
|-------|----------|
| `false` (default) | `/rota-work` never invokes `/rota-qa`. QA only runs from `/rota-ship` (when `ship.qa: true`) or manual `/rota-qa run`. |
| `true` | After `/rota-work` finishes a cycle, if any touched file matches a `Watch globs` entry in a `.rota/qa/<target>.md` strategy, `/rota-qa run` fires scoped to that target. Verdict is advisory at this stage (the cycle is already complete) but findings surface for the next session. |

Skip turning this on until you have stable strategies and want continuous coverage on every cycle. Otherwise the noise of running runners on every commit outweighs the value.

## debug.competingHypotheses

Controls whether [`/rota-debug`](debugging.md) Step 6 dispatches a single hypothesis agent or fans out three parallel agents from different angles (recent-changes, data-shape, concurrency-lifecycle). The orchestrator deduplicates the ranked outputs and picks the strongest hypothesis regardless of which agent surfaced it.

| Value | Behavior |
|-------|----------|
| `false` (default) | Single hypothesis agent. Cheaper and faster; fine for most bugs where one angle is obviously primary. |
| `true` | Three parallel hypothesis agents in one tool-call batch. Better diversity on hard bugs where the right framing isn't obvious upfront, at ~3× orchestrator cost on every `/rota-debug` run. Step 6 latency stays roughly the same since the agents run concurrently. |

Flip on when you have a class of bugs that consistently take multiple cycles to land. The diversity of framings makes the difference. Keep off when most bugs are single-cause and you're paying for cycles you don't need.

## autonomy.level

Controls whether skills nudge or invoke the next skill directly. Two levels: `"off"` (default), `"auto"`. `"loop"` was removed; `rota config check` flags it. See [autonomy levels](autonomy.md) for the full breakdown: when each level fires, what gates still apply, and how to pick.

## docs.path

- **Type:** string
- **Default:** `"docs"`

Relative path (from the project root) to the documentation folder that [`/rota-ship --docs`](../reference/slash-commands.md#rota-ship) reads and writes. Set this when your project keeps docs somewhere other than the default, for example `"documentation"`, `"site/content"`, or `"wiki"`.

```json
{ "docs": { "path": "documentation" } }
```

## docs.autoCreate

- **Type:** boolean
- **Default:** `false`

Controls whether `/rota-ship --docs` after-work mode automatically writes proposed doc updates without pausing for approval. When `false` (the default), the after-work flow proposes changes and waits for your confirmation before writing. That's the safe propose-mode path. When `true`, it writes changes and reports what it did. The `true` path will gain a Layer-3 LLM safety review before commit when M01-S03 ships; until then, `false` is the recommended default and `true` is opt-in.

| Value | Behavior |
|-------|----------|
| `false` (default) | After-work mode proposes updates and waits for approval before writing. Recommended until M01-S03 ships the auto-write safety review. |
| `true` | After-work mode writes doc updates automatically. Fast; assumes you trust the agent's judgment on doc prose. Best paired with a `git diff` review per cycle. |

## docs.afterWork

- **Type:** boolean
- **Default:** `false`

Gate for the after-work docs flow. When `true`, the skills [`/rota-work`](running-work.md), `/rota-ship`, and [`/rota-release`](../reference/slash-commands.md#rota-release) trigger the docs after-work flow after their primary action completes. `/rota-work` and `/rota-ship` only fire on cycles that resolve 2+ items or touch 5+ files (small fixes don't trigger); `/rota-release` fires on every successful release (release notes are inherently user-facing). Under `autonomy.level: off`, the trigger is a one-line nudge in the terminal report; under `auto`, the skill auto-dispatches `/rota-ship --docs` directly (or runs the after-work flow inline if called from `/rota-ship` itself).

```json
{ "docs": { "afterWork": true } }
```

Leave `false` while you're shaping docs by hand. Flip on once your docs structure is stable enough that `/rota-ship --docs`'s propose-mode adds value rather than noise.

## release.checklistPath

- **Type:** string
- **Default:** `.rota/RELEASE.md`

Path to the project's release checklist: a flat markdown file with `- [ ]` items that `/rota-release` walks as gates before bumping the version (Step 1.5). Each open checkbox becomes an `AskUserQuestion` interjection: *Yes, continue* / *Fix now and continue* / *Skip* / *Abort*. Items marked `- [x]` are ignored. Items whose text ends with `(manual)` always interject even under `autonomy.level: auto`. Use this for sensitive gates (staging migrations, infra rollouts) that need attention regardless of autonomy.

The file is per-project and tracked by default, so the checklist is shared with the team. When absent under `autonomy.level: off`, the skill offers to scaffold a starter template; under `auto`, the skill silently skips the gate rather than interrupt an unattended run. To keep the checklist per-contributor instead, add it to `.gitignore`.

The skill itself stays generic: no release step is hardcoded. Drift like a forgotten sibling-version-file bump (e.g. the marketplace.json that went stale by two majors) gets caught by adding an item, not by patching the skill.

```json
{ "release": { "checklistPath": "docs/RELEASE-CHECKLIST.md" } }
```

Override the path if your project prefers a different location. By default the checklist is tracked at `.rota/RELEASE.md` and shared with the team.

## release.confirmLargePushCommits

- **Type:** integer
- **Default:** `10`

Threshold for the number of unpushed commits above which `/rota-release` will interject one confirmation prompt before pushing, even under `autonomy.level: auto`. Below the threshold, auto autonomy silently pushes the unpushed range as part of the release (the existing speed-contract behavior). Above it, the skill always asks. Releases that push 10+ commits are not the common case and the user usually wants a beat to confirm.

```json
{ "release": { "confirmLargePushCommits": 25 } }
```

Set higher to suppress the prompt for typical project velocities; set lower (e.g., `5`) for projects where every push is consequential.

## release.nudgeAfterCommits

- **Type:** integer
- **Default:** `10`

Number of commits since the last release tag at which [`/rota-work` (no argument)](picking-work.md) (terminal paths only) and `/rota-ship` (post-ship report) start surfacing a one-line nudge: *"<N> commits since <tag>; consider `/rota-release`."* Informational only; no skill is auto-invoked.

## release.nudgeAfterDays

- **Type:** integer
- **Default:** `14`

Companion to `release.nudgeAfterCommits`. The release nudge fires when EITHER threshold is reached: high-velocity projects hit the commits threshold first, slow-burn projects hit the days threshold first. Set one or both higher to suppress more aggressively, or lower to release more often.

## git.baseBranch

- **Type:** string
- **Default:** `""` (auto-detect)

Override the base branch that `rota git base` resolves to. When empty (the default), `rota` auto-detects by probing `main`, `master`, `trunk`, then `origin/HEAD` in that order. Set this explicitly when your project uses a non-default base branch such as `develop` (gitflow), `release`, or any other name that won't be found by auto-detection.

```json
{ "git": { "baseBranch": "develop" } }
```

Skills that use the base branch (including `/rota-ship`, `/rota-review` and `/rota-work`) all call `rota git base` and will pick up this override automatically.

## Issues backend keys

- **Keys:** `backlog.backend`, `issues.provider`, `issues.retryWaitSeconds`, `issues.bulkPaceMs`, `issues.homeRepo`, `issues.labels.*`
- **Required:** no. Existing configs stay valid without them.

| Key | Default | Values |
|-----|---------|--------|
| `backlog.backend` | `"file"` | `"file"` or `"issues"`. `"issues"` puts the backlog on the tracker; see [issue backend](issue-backend.md). |
| `issues.provider` | `"auto"` | `"auto"`, `"github"` or `"gitlab"`. |
| `issues.retryWaitSeconds` | `60` | Seconds to wait before retrying a failed tracker call. |
| `issues.bulkPaceMs` | `1000` | Milliseconds `rota migrate issues` waits between tracker writes. `0` disables the pause. |
| `issues.homeRepo` | `""` | Umbrella mode only: sub-repo holding milestone tracking issues. Empty means the first registered sub-repo. |
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

`issues.label` is the legacy alias of `issues.labels.inProgress`. It is used when the new key is unset. `rota config show` lists all of these keys with their effective value and source.

## rota.version (auto-managed)

- **Type:** string
- **Default:** `""` (unstamped until `rota init` first runs)

Records the rota release (binary version) that `rota init` last ran with. Auto-managed: `rota init` re-stamps this on every run, including STALE migrations. Don't edit by hand.

`rota version --drift` compares the stamped value with the installed `rota` binary, and [`rota init check`](../reference/preflight.md) surfaces the same drift as a warning. `--json` returns `stamped`, `installed` and `status` (`match`, `drift` or `unknown`).

Re-running `rota init` re-stamps `rota.version`; there are no project files to refresh. A stamp written before the rename to rota is read as a fallback and moved to `rota.version` by `rota init` / `rota config fill`. Distinct from `rota update` (which compares installed vs latest GitHub release): this is *project drift*, visible when `rota` was upgraded under you and the project hasn't been re-stamped yet.

When `rota version --drift` reports drift, re-run `rota init` after an upgrade to clear it.
