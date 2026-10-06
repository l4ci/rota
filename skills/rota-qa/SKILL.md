---
name: rota-qa
description: Use on "/rota-qa", "run QA", "test the feature", "validate the build", before ship as a gate, or on the first cycle to scaffold a per-repo QA strategy. QA of the built product, not the diff.
---

# rota-qa — Product Quality Assurance

`/rota-review` asks "does this diff make sense": commits and diff, no execution. `/rota-qa` asks "does the product work": tests, probes and scans against the built artifact. They never call each other; `/rota-ship` may call both, each behind its own config flag.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model dispatching the runners (default `opus`).
- `qa.gate`, `qa.afterWork`, `ship.qa` — gate and hook flags, all off or advisory by default. When a caller or flag decides whether QA runs, read [`gates-and-modes.md`](gates-and-modes.md).

## When NOT to Use

- Diff-level review → `/rota-review`.
- Nothing built yet → `/rota-work` first.
- Changing code from findings → `/rota-work` or `/rota-debug`.

## Modes

Three modes (first-run, run, restructure) share a skeleton with `/rota-ship`'s Docs Mode. When picking a mode or checking commit ownership, read [`gates-and-modes.md`](gates-and-modes.md).

### Mode: first-run

Read [`first-run.md`](first-run.md) and follow it when `.rota/qa/` is empty for the active scope (umbrella: per-repo; single-repo: no `.rota/qa/*.md`).

### Mode: run

Copy this checklist and track your progress (run mode):
```
- [ ] Step 1 — Resolve Scope
- [ ] Step 2 — Load Strategies
- [ ] Step 3 — Infra Preflight
- [ ] Step 4 — Execute Checks
- [ ] Step 5 — Audit Pass
- [ ] Step 6 — Score & Verdict
- [ ] Step 7 — Report
- [ ] Step 8 — Routing
```

#### Step 1 — Resolve Scope

Any `rota` verb exiting 3 means no `.rota/` project: surface that and stop.

A named target (`/rota-qa run web`) wins. Otherwise:

When `.rota/repos.json` is non-empty (umbrella), read [`umbrella-scope.md`](umbrella-scope.md). Single-repo: all `.rota/qa/*.md` entries.

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

Before dispatching, read [`proof-reuse.md`](proof-reuse.md): checks already PASS at the current sha are reused, not re-run.

**Record proof.** For every item on the branch (`rota review scope --json` `data.referencedIds`), write each check result: `rota proof add <ID> --check "<check name>" --result PASS|FAIL --evidence "<artifact path under .rota/qa-runs/ or one-line output>"`. Rows are facts; the Step 6 verdict is the judgement.

When a check timed out, blew a duration budget or hit a connection error, read [`rerun-failed-check.md`](rerun-failed-check.md) before recording `met: false`.

#### Step 5 — Audit Pass

Dispatch one subagent (Opus, no prior context) per target with the `Audit checks` rubric, Step 4 screenshots if any, and read-only access to the running surface. Return an array of `{ dimension, severity (P0|P1|P2|P3), observation, evidence, suggested_fix }`.

Audit findings never produce automated pass/fail. They get a separate severity-ranked report section.

#### Step 6 — Score & Verdict

Per target:

- **PASS** — all executable checks `met: true`, no audit P0.
- **CONCERNS** — all executable checks met, but audit has P0/P1, OR ≥1 check passed only with a warning. Ship allowed; user owns the call.
- **FAIL** — any executable check `met: false`, OR an audit P0 with `severity: blocker`.

**Record the verdict** for the current branch, once per repo (umbrella: read [`umbrella-scope.md`](umbrella-scope.md)). The body carries the failed executable checks and P0/P1 audit findings, mapping P0 `blocker` to `blocker`, other P0 and P1 to `major`, the rest to `minor`:

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

Exit 2 names the failing field: fix the body, re-run `rota verdict add`, and continue to Step 7 only on exit 0. `/rota-ship` routes on this record, not the printed report.

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

Same PASS/CONCERNS/FAIL contract as `references/review-verdict-routing.md`.

#### Step 8 — Routing

- Standalone: relay the verdict per `Producer-side relay` in the verdict-routing reference.
- From `/rota-ship`: return the verdict only. When invoked from `/rota-ship`, read [`gates-and-modes.md`](gates-and-modes.md) for routing and the `QA concerns:` label.

### Mode: restructure

When strategy files drifted, read [`restructure.md`](restructure.md) and follow it.

## Rules

- **Strategy is data.** Never hardcode a runner; every command comes from `.rota/qa/<target>.md`.
- **Performance + security = executable, pass/fail. Usability = audit, severity-ranked.** Don't pretend usability is testable.
- **Read-only on `run`.** Never edit code; never stage. Artifacts under `.rota/qa-runs/<timestamp>/` (gitignored).
- **Infra-fail fast.** Halt before running anything; partial QA gives false confidence.
- **Evidence over opinion.** Every audit finding cites file:line, a screenshot path or a reproducer command.
- **Never read commits or diffs.** That is `/rota-review`.

## Failure Modes

- **No strategy file** — halt; don't auto-scaffold.
- **Runner subagent timeout** — re-run that check alone; see [`rerun-failed-check.md`](rerun-failed-check.md).
- **Strategy references retired tool** — `met: false` with `evidence: "command not found"`. Surface in `restructure`.

## References

- [`references/three-mode-skill-shape.md`](references/three-mode-skill-shape.md) — Shared skeleton with `/rota-ship` Docs Mode.
- [`references/subagent-dispatch.md`](references/subagent-dispatch.md) — Parallel runner pattern.
- [`references/review-verdict-routing.md`](references/review-verdict-routing.md) — PASS / CONCERNS / FAIL contract; QA reuses it.
- [`references/umbrella-mode.md`](references/umbrella-mode.md) — Per-repo resolution for `--repo` / `--all`.
- [`references/post-cycle-trigger-gate.md`](references/post-cycle-trigger-gate.md) — When `qa.afterWork: true` should fire.
- [`first-run.md`](first-run.md) — `first-run` mode: probe surfaces, propose strategy.
- [`restructure.md`](restructure.md) — `restructure` mode.
- [`gates-and-modes.md`](gates-and-modes.md) — gate/hook config, mode skeleton, `/rota-ship` routing.
- [`umbrella-scope.md`](umbrella-scope.md) — Steps 1 and 6 in umbrella repos.
- [`proof-reuse.md`](proof-reuse.md) — Step 4 reuse of PASS proof at the current sha.
- [`rerun-failed-check.md`](rerun-failed-check.md) — Step 4 lone re-run before recording a failure.
