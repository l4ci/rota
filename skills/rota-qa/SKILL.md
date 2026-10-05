---
name: rota-qa
description: Use on "/rota-qa", "run QA", "test the feature", "validate the build", before ship as a gate, or on the first cycle to scaffold a per-repo QA strategy. QA of the built product, not the diff.
---

# rota-qa — Product Quality Assurance

`/rota-qa` and `/rota-review` are deliberately separate:

- **`/rota-review`** answers *"does this diff make sense"* — reads commits + diff, no execution.
- **`/rota-qa`** answers *"does the product work"* — runs tests, probes, scans against the built artifact.

They never call each other. `/rota-ship` may call both, each behind its own config flag.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model dispatching the runners (default `opus`).
- `qa.gate` — `"advisory"` (default) emits verdict, never blocks ship; `"blocking"` causes `ship.qa: true` invocations to halt on `FAIL`.
- `qa.afterWork` — `false` (default). When `true`, `/rota-work` invokes `/rota-qa run` post-cycle if touched files match a QA target's `Watch globs`.
- `ship.qa` — `false` (default). When `true`, `/rota-ship` calls `/rota-qa run` between `/rota-review` and the merge/PR step.

## When to Use

- *"QA this"*, *"kick the tires"*, *"does this actually work?"* — manual exploratory run.
- After a feature lands and before opening a PR, when you want product-level evidence (not just diff sanity).
- First-time setup on a new repo or new umbrella sub-repo — bootstrap the strategy file.

## When NOT to Use

- You want diff-level review → `/rota-review`. QA does not read commits.
- Nothing built yet → finish via `/rota-work` first. QA needs an artifact to probe.
- You want to change code based on findings → consume the report, then `/rota-work` or `/rota-debug`.

## Modes

`/rota-qa` shares the three-mode skeleton with `/rota-ship`'s Docs Mode (scaffold / after-work / audit) — see `references/three-mode-skill-shape.md`. Divergences:

| Aspect | `/rota-qa` |
|---|---|
| Artifact root | `.rota/qa/<target>.md` — per-target strategy files. In umbrella mode, `<target>` is a registered repo name; in single-repo mode, `<target>` is a user-named surface (`web`, `api`, `cli`, ...) |
| Audience | AI runners + contributors triaging findings |
| Mode-3 name | `restructure` (re-probe surfaces, retire dead strategies, fix broken commands) |
| After-work approval gate | opt-in via `qa.afterWork: true`; default off — QA runs are slow and may need infra |
| After-work trigger gate | `qa.afterWork: true` AND touched files match a target's `Watch globs` |
| Commit ownership | `run` does not commit (read-only verdict, recorded with `rota verdict add`); `first-run` / `restructure` own a `chore(qa):` commit |

### Mode: first-run

Run when `.rota/qa/` is empty for the active scope (umbrella: per-repo; single-repo: when no `.rota/qa/*.md` files exist).

1. **Detect surfaces.** Inspect what kind of product this is — never assume browser:
   - `package.json` with `next` / `vite` / `react` / `vue` / `svelte` → web-UI surface
   - `App.swift` / `*.xcodeproj` / `Package.swift` with `@main` → macOS/iOS app surface
   - `openapi.yaml` / `swagger.json` / Express/FastAPI/Hono routes → HTTP-API surface
   - `bin/*` shell entry / `main.py` CLI / `cobra` / `clap` Rust → CLI surface
   - `lib/` or `src/` with no entry point + tests dir → library surface (unit + contract only)
   - Mixed → multiple targets, one strategy file each
2. **Detect existing test infra.** `find` for: `playwright.config.*`, `cypress.config.*`, `pytest.ini`, `jest.config.*`, `vitest.config.*`, `*XCTest*`, `*.smoke.sh`, `tests/`, `test/`, `__tests__/`, CI workflow files. Note what's already wired; missing tooling stays a note, not a proposal.
3. **Probe quality tooling.** Check for: Lighthouse / Pagespeed, axe-core / pa11y, ZAP / semgrep configs, dependency audit (`npm audit`, `pip-audit`, `cargo-audit`), perf budgets, contract tests. Presence only — never propose installing anything in this mode.
4. **Propose strategy.** For each target, draft a one-screen strategy with these sections — show the user before writing:
   - **Surface** — what kind of thing this is (web, API, CLI, mobile, lib)
   - **Watch globs** — paths whose changes should trigger after-work QA
   - **Executable checks** — runners with concrete commands, grouped by pillar (performance / security / functional). Each entry: `name` · `command` · `pass criterion`. Examples: `lighthouse --budget-path=.budget.json` · `pa11y http://localhost:3000` · `npm audit --audit-level=high` · `bash test/smoke.sh` · `playwright test --grep @smoke`.
   - **Audit checks** — usability dimensions to inspect by hand or LLM (empty states, error recovery, copy clarity, first-run flow). Rubric, no commands.
   - **Infra requirements** — what must be running for `run` mode (e.g. `npm run dev` on `:3000`, deployed staging URL, sandbox creds). Skill refuses to run if these aren't met.
   - **Out of scope** — explicit non-goals (e.g. "no load testing", "no real-payment flows").
5. **Approve & write.** Use `AskUserQuestion` with `Approve as drafted (Recommended)` / `Edit before writing` / `Cancel`. On approval, write `.rota/qa/<target>.md` with frontmatter (`target`, `surface`, `summary`, `created`, `touched`, `watch-globs`) and the five body sections.
6. **Index.** Run `rota qa index` to regenerate the `## Project QA` block in `CLAUDE.md`.
7. **Commit.** `chore(qa): scaffold QA strategy for <target> (.rota/qa/, ## Project QA block)`.

### Mode: run

Track these phases with the host's task tool if it has one.

Phases:

1. *Resolve scope* — which targets to QA (Step 2)
2. *Load strategies* — read `.rota/qa/<target>.md` for each (Step 3)
3. *Infra preflight* — verify `Infra requirements` are met (Step 4)
4. *Execute checks* — dispatch runner subagents (Step 5)
5. *Audit pass* — usability findings (Step 6)
6. *Score & verdict* — aggregate (Step 7)
7. *Report* — relay to user (Step 8)

#### Step 1 — Project Check

No separate check: every `rota` verb exits 3 when there is no `.rota/` project. Surface that and stop.

#### Step 2 — Resolve Scope

If user named a target (`/rota-qa run web`), use it. Otherwise:

- **Umbrella mode** (`.rota/repos.json` non-empty): default to the repo of the current branch (resolve via `rota repo which`, field `name`). User can pass `--repo <name>` or `--all`.
- **Single-repo mode**: default to all `.rota/qa/*.md` entries.

If no strategy file exists for the resolved scope, halt and tell the user to run `/rota-qa first-run`.

#### Step 3 — Load Strategies

For each target, read `.rota/qa/<target>.md` via `rota qa query <target>`. Parse the five body sections. Reject any strategy missing `Executable checks` or `Infra requirements` — surface as a config error and route to `restructure`.

#### Step 4 — Infra Preflight

For each target, verify everything under `Infra requirements`:

- HTTP probes for dev/staging URLs (curl with 5s timeout)
- Process checks for required binaries (`command -v playwright`, etc.)
- Env-var presence for credentials (don't print values)

If any infra is missing, record an `INFRA-FAIL` verdict (Step 7's `rota verdict add`, with the missing items as `info` findings) and halt — partial QA produces false confidence. Tell the user exactly what to start / install.

#### Step 5 — Execute Checks

Dispatch one subagent per check group (per pillar per target) in parallel via the Agent tool — see `references/subagent-dispatch.md`. Each subagent:

- Runs the commands from its assigned `Executable checks` entries.
- Captures stdout, exit code, and artifact paths (screenshots, HAR files, reports). Artifacts write under `.rota/qa-runs/<timestamp>/<target>/<check>/`.
- Returns a structured result: `{ name, command, exitCode, passCriterion, met, evidence }`.

The orchestrator does not run the checks itself — parallel dispatch is the point. Aggregate the results.

**Reuse proof; the merge gate is the only full run.** The merge gate already ran the full suite (`refactor.verifyCommands`, e.g. smoke plus `go test`) on the merged tree. Before dispatching, `rota proof show <ID> --json` for each item: a PASS row for the same check at the current `git rev-parse HEAD` (its `sha`) is reused, not re-run. Where a strategy's executable check is the gate's own command, treat the gate's PASS at that sha as the QA run. Dispatch runners only for checks with no PASS at this sha (browser, lighthouse, audit and other surface checks the gate does not cover). Record reused rows as such in the report.

**Record proof.** For every item on the branch (`rota review scope --json` `data.referencedIds`), write each executable-check result as a proof row: `rota proof add <ID> --check "<check name>" --result PASS|FAIL --evidence "<artifact path under .rota/qa-runs/ or one-line output>"`. Rows are facts; the QA verdict (Step 7) is still the judgement.

**Re-run a failed check alone before recording it.** Parallel runners contend for one box, and every check with a fixed time budget starts failing on elapsed time rather than on truth once the machine is loaded. Before writing `met: false` for any check that timed out, blew a duration budget, or failed on a connection error, re-run that one check with nothing else in flight and record `uptime` alongside both runs. Three consequences worth stating separately:

- **A timeout is not a failure of the thing under test.** It says the assertion never ran — read the runner's actual output before forming a theory about the code.
- **Never resolve one of these by raising the budget.** A bigger fixed number fails at some higher load and makes the genuine regression slower to report. If the check is genuinely too slow, the fix belongs in `.rota/qa/<target>.md` as less work, not more time.
- **A red with no load figure beside it is not evidence in either direction.** Quote both `uptime` readings in the check's `evidence` field so the verdict is auditable.

Connection-refused and address-in-use errors across *many* checks at once are infrastructure, not findings — re-run serially before reporting anything.

#### Step 6 — Audit Pass

Dispatch one subagent (Opus, no prior context) per target with the `Audit checks` rubric, screenshots from Step 5 if available, and read-only access to the running surface. Return: array of `{ dimension, severity (P0|P1|P2|P3), observation, evidence, suggested_fix }`.

Audit findings never produce automated pass/fail. They surface as a separate report section, severity-ranked.

#### Step 7 — Score & Verdict

Aggregate per target:

- **PASS** — all executable checks `met: true`, audit findings have no P0.
- **CONCERNS** — executable checks all met, but audit has P0/P1 findings, OR ≥1 executable check passed only with a warning. Ship is allowed; user owns the call.
- **FAIL** — any executable check `met: false`, OR audit has a P0 with `severity: blocker`.

In umbrella `--all` mode, the rollup verdict is the worst-of across targets. Per-target verdicts still report individually.

**Record the verdict** for the current branch, once per repo (umbrella: `--repo <name>` with that repo's verdict). The body carries the failed executable checks and the P0/P1 audit findings, mapping P0 `blocker` to `blocker`, other P0 and P1 to `major`, the rest to `minor`:

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

Exit 2 names the field that failed validation; fix the body and re-run. `/rota-ship` routes on this record, not on the printed report.

#### Step 8 — Report

Print a structured report:

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

Same PASS/CONCERNS/FAIL contract as `references/review-verdict-routing.md`; use carrier label `QA concerns:` when invoked from `/rota-ship` (per the routing reference's "Carrier-label override").

#### Step 9 — Routing

- Standalone: relay verdict to user per `Producer-side relay` in the verdict-routing reference.
- From `/rota-ship`: return the verdict only. `/rota-ship` routes on the recorded verdict with `rota verdict route --for ship-qa`, which applies `qa.gate` (`"advisory"` never halts; `"blocking"` halts on `FAIL` and prompts on `CONCERNS`).

### Mode: restructure

Run on demand when strategy files have drifted from the project (new surfaces, retired tools, dead targets).

1. Re-run the `Detect surfaces` and `Detect existing test infra` probes from `first-run`.
2. Diff against current `.rota/qa/*.md` — flag: targets with no matching surface (dead), surfaces with no target (uncovered), commands referencing tools not installed (broken), `Watch globs` matching no files (stale).
3. Propose changes — archive dead, draft new, fix broken, update globs — show to user before writing.
4. On approval, write the changes, run `rota qa index`, commit `chore(qa): restructure QA strategy (<summary>)`.

## Rules

- **Strategy is data; runners are dispatched.** The skill never hardcodes Playwright, smoke, axe, or anything else. Every command comes from `.rota/qa/<target>.md`.
- **Three pillars, three shapes.** Performance + security = executable, pass/fail. Usability = audit, severity-ranked. Don't pretend usability is testable.
- **Read-only on `run`.** The verdict is the entire product. Never edit code; never stage. Artifacts write under `.rota/qa-runs/<timestamp>/` — gitignored by default (bulky and regeneratable from `qa/<target>.md`).
- **Infra-fail fast.** Missing dev server, missing creds, missing binary → halt before running anything. Partial QA produces false confidence.
- **Evidence over opinion.** Every audit finding cites file:line, screenshot path, or a reproducer command. Vibes don't ship.
- **Stay separate from `/rota-review`.** Never read commits or diffs. If you find yourself wanting to, the request belongs to `/rota-review`.

## Failure Modes

- **No strategy file** — halt; tell user to run `/rota-qa first-run`. Don't auto-scaffold.
- **Infra unavailable** — record an `INFRA-FAIL` verdict; halt. User starts services, re-runs.
- **Runner subagent timeout** — re-run that check alone per Step 5 before recording it. If it passes solo, the original red was contention: record `met: true` with both `uptime` figures in `evidence`. If it times out solo too, record `met: false` with `evidence: "timeout after Ns at load <figure>, reproduced alone at load <figure>"`. QA continues either way; verdict reflects the confirmed result, never the contended one.
- **Strategy references retired tool** — that check is `met: false` with `evidence: "command not found"`. Surface in `restructure` mode. This includes a `codex-verify` runner from an older strategy: that runner is retired, so `restructure` drops the entry.

## References

- [`references/three-mode-skill-shape.md`](references/three-mode-skill-shape.md) — Shared skeleton with `/rota-ship` Docs Mode.
- [`references/subagent-dispatch.md`](references/subagent-dispatch.md) — Parallel runner pattern.
- [`references/review-verdict-routing.md`](references/review-verdict-routing.md) — PASS / CONCERNS / FAIL contract; QA reuses it.
- [`references/umbrella-mode.md`](references/umbrella-mode.md) — Per-repo resolution for `--repo` / `--all`.
- [`references/post-cycle-trigger-gate.md`](references/post-cycle-trigger-gate.md) — When `qa.afterWork: true` should fire.
