---
name: rota-qa
description: Use on "/rota-qa", "run QA", "test the feature", "validate the build", before ship as a gate, or on the first cycle to scaffold a per-repo QA strategy. QA of the built product, not the diff.
---

# rota-qa — Product Quality Assurance

`/rota-review` asks "does this diff make sense": commits and diff, no execution. `/rota-qa` asks "does the product work": tests, probes and scans against the built artifact. They never call each other; `/rota-ship` may call both, each behind its own config flag.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model dispatching the runners (default `opus`).
- `qa.gate` — `"advisory"` (default) emits the verdict, never blocks ship; `"blocking"` halts `ship.qa: true` invocations on `FAIL`.
- `qa.afterWork` — `false` (default). When `true`, `/rota-work` invokes `/rota-qa run` post-cycle if touched files match a QA target's `Watch globs`.
- `ship.qa` — `false` (default). When `true`, `/rota-ship` calls `/rota-qa run` between `/rota-review` and the merge/PR step.

## When NOT to Use

- Diff-level review → `/rota-review`.
- Nothing built yet → `/rota-work` first.
- Changing code from findings → `/rota-work` or `/rota-debug`.

## Modes

`/rota-qa` shares the three-mode skeleton with `/rota-ship`'s Docs Mode (scaffold / after-work / audit); see `references/three-mode-skill-shape.md`. Divergences:

| Aspect | `/rota-qa` |
|---|---|
| Artifact root | `.rota/qa/<target>.md`, one strategy per target. Umbrella: `<target>` is a registered repo name; single-repo: a user-named surface (`web`, `api`, `cli`, ...) |
| Mode-3 name | `restructure` (re-probe surfaces, retire dead strategies, fix broken commands) |
| After-work trigger gate | `qa.afterWork: true` AND touched files match a target's `Watch globs` (default off) |
| Commit ownership | `run` does not commit (read-only; verdict recorded with `rota verdict add`); `first-run` / `restructure` own a `chore(qa):` commit |

### Mode: first-run

Read [`first-run.md`](first-run.md) and follow it when `.rota/qa/` is empty for the active scope (umbrella: per-repo; single-repo: no `.rota/qa/*.md`).

### Mode: run

#### Step 1 — Resolve Scope

Any `rota` verb exiting 3 means no `.rota/` project: surface that and stop.

A named target (`/rota-qa run web`) wins. Otherwise:

- **Umbrella** (`.rota/repos.json` non-empty): the repo of the current branch (`rota repo which`, field `name`). `--repo <name>` or `--all` override.
- **Single-repo**: all `.rota/qa/*.md` entries.

No strategy file for the scope: halt, tell the user to run `/rota-qa first-run`.

#### Step 2 — Load Strategies

`rota qa query <target>` per target. Parse the five body sections. A strategy missing `Executable checks` or `Infra requirements` is a config error: route to `restructure`.

#### Step 3 — Infra Preflight

Verify everything under `Infra requirements`:

- HTTP probes for dev/staging URLs (curl, 5s timeout)
- `command -v <binary>` for required tools
- Env-var presence for credentials (never print values)

Anything missing: record an `INFRA-FAIL` verdict (Step 6's `rota verdict add`, missing items as `info` findings) and halt. Tell the user exactly what to start or install.

#### Step 4 — Execute Checks

Dispatch one subagent per check group (per pillar per target) in parallel via the Agent tool; see `references/subagent-dispatch.md`. The orchestrator does not run checks itself. Each subagent:

- Runs the commands from its assigned `Executable checks` entries.
- Captures stdout, exit code and artifact paths under `.rota/qa-runs/<timestamp>/<target>/<check>/`.
- Returns `{ name, command, exitCode, passCriterion, met, evidence }`.

**Reuse proof; the merge gate is the only full run.** The merge gate already ran the full suite (`refactor.verifyCommands`) on the merged tree. Before dispatching, run `rota proof show <ID> --json` per item: a PASS row for the same check at the current `git rev-parse HEAD` (its `sha`) is reused, not re-run. Where a strategy check is the gate's own command, the gate's PASS at that sha is the QA run. Dispatch runners only for checks with no PASS at this sha (browser, lighthouse, audit and other surface checks). Mark reused rows as such in the report.

**Record proof.** For every item on the branch (`rota review scope --json` `data.referencedIds`), write each check result: `rota proof add <ID> --check "<check name>" --result PASS|FAIL --evidence "<artifact path under .rota/qa-runs/ or one-line output>"`. Rows are facts; the Step 6 verdict is the judgement.

**Re-run a failed check alone before recording it.** Parallel runners contend for one box, so time budgets fail on load, not truth. Before writing `met: false` for a check that timed out, blew a duration budget or hit a connection error, re-run it with nothing else in flight and put both `uptime` readings in `evidence`. A timeout means the assertion never ran: read the runner's output before theorizing about the code. Never raise the budget; if the check is too slow, cut its work in `.rota/qa/<target>.md`.

Connection-refused and address-in-use errors across many checks at once are infrastructure, not findings: re-run serially before reporting.

#### Step 5 — Audit Pass

Dispatch one subagent (Opus, no prior context) per target with the `Audit checks` rubric, Step 4 screenshots if any, and read-only access to the running surface. Return an array of `{ dimension, severity (P0|P1|P2|P3), observation, evidence, suggested_fix }`.

Audit findings never produce automated pass/fail. They get a separate severity-ranked report section.

#### Step 6 — Score & Verdict

Per target:

- **PASS** — all executable checks `met: true`, no audit P0.
- **CONCERNS** — all executable checks met, but audit has P0/P1, OR ≥1 check passed only with a warning. Ship allowed; user owns the call.
- **FAIL** — any executable check `met: false`, OR an audit P0 with `severity: blocker`.

`--all` rollup is worst-of across targets; per-target verdicts still report individually.

**Record the verdict** for the current branch, once per repo (umbrella: `--repo <name>` with that repo's verdict). The body carries the failed executable checks and P0/P1 audit findings, mapping P0 `blocker` to `blocker`, other P0 and P1 to `major`, the rest to `minor`:

```bash
rota verdict add <branch> --kind qa --verdict <PASS|CONCERNS|FAIL|INFRA-FAIL> --body-file "$VERDICT" --json
```

`$VERDICT` is one JSON object (`internal/verdict`: strict, unknown keys rejected). `verdict`, when present, must equal `--verdict`:

```json
{
  "summary": "<one line: what ran and the result>",
  "findings": [
    {"severity": "blocker|major|minor|info", "title": "<failed check or audit finding>",
     "file": "<path, optional>", "line": 12, "detail": "<command and evidence, optional>"}
  ]
}
```

Exit 2 names the failing field; fix the body and re-run. `/rota-ship` routes on this record, not the printed report.

#### Step 7 — Report

```
QA verdict: <PASS|CONCERNS|FAIL|INFRA-FAIL>

Targets:
  <target-1>: <verdict>
    Executable checks: <n passed> / <n total>
    Audit findings: <n P0>, <n P1>, <n P2>, <n P3>
  ...

Failed executable checks:
  - <name> — <command> — <evidence>
  ...

Audit findings (P0/P1 inline; full list at <path>):
  - [P0] <dimension>: <observation>  — fix: <suggested_fix>
  ...

Evidence: .rota/qa-runs/<timestamp>/
```

Same PASS/CONCERNS/FAIL contract as `references/review-verdict-routing.md`; carrier label `QA concerns:` when invoked from `/rota-ship` (that reference's "Carrier-label override").

#### Step 8 — Routing

- Standalone: relay the verdict per `Producer-side relay` in the verdict-routing reference.
- From `/rota-ship`: return the verdict only. `/rota-ship` routes with `rota verdict route --for ship-qa`, which applies `qa.gate` (`"advisory"` never halts; `"blocking"` halts on `FAIL` and prompts on `CONCERNS`).

### Mode: restructure

On demand, when strategy files drifted (new surfaces, retired tools, dead targets).

1. Re-run the `Detect surfaces` and `Detect existing test infra` probes (steps 1 and 2 of `first-run.md`).
2. Diff against `.rota/qa/*.md`; flag targets with no surface (dead), surfaces with no target (uncovered), commands using tools not installed (broken), `Watch globs` matching no files (stale).
3. Propose changes (archive dead, draft new, fix broken, update globs) and show the user before writing.
4. On approval, write, run `rota qa index`, commit `chore(qa): restructure QA strategy (<summary>)`.

## Rules

- **Strategy is data.** Never hardcode a runner; every command comes from `.rota/qa/<target>.md`.
- **Performance + security = executable, pass/fail. Usability = audit, severity-ranked.** Don't pretend usability is testable.
- **Read-only on `run`.** Never edit code; never stage. Artifacts under `.rota/qa-runs/<timestamp>/` (gitignored).
- **Infra-fail fast.** Halt before running anything; partial QA gives false confidence.
- **Evidence over opinion.** Every audit finding cites file:line, a screenshot path or a reproducer command.
- **Never read commits or diffs.** That is `/rota-review`.

## Failure Modes

- **No strategy file** — halt; don't auto-scaffold.
- **Runner subagent timeout** — re-run that check alone per Step 4. Passes solo: the red was contention; record `met: true` with both `uptime` figures in `evidence`. Times out solo too: `met: false` with `evidence: "timeout after Ns at load <figure>, reproduced alone at load <figure>"`. QA continues either way; the verdict reflects the confirmed result.
- **Strategy references retired tool** — `met: false` with `evidence: "command not found"`. Surface in `restructure`.

## References

- [`references/three-mode-skill-shape.md`](references/three-mode-skill-shape.md) — Shared skeleton with `/rota-ship` Docs Mode.
- [`references/subagent-dispatch.md`](references/subagent-dispatch.md) — Parallel runner pattern.
- [`references/review-verdict-routing.md`](references/review-verdict-routing.md) — PASS / CONCERNS / FAIL contract; QA reuses it.
- [`references/umbrella-mode.md`](references/umbrella-mode.md) — Per-repo resolution for `--repo` / `--all`.
- [`references/post-cycle-trigger-gate.md`](references/post-cycle-trigger-gate.md) — When `qa.afterWork: true` should fire.
