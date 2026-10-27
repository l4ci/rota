# Product QA

`/rota-qa` answers a question `/rota-review` cannot: *"does the product actually work?"* `/rota-review` reads commits and the diff. `/rota-qa` executes runners against the built artifact (Playwright, smoke scripts, contract tests, Lighthouse, axe, ZAP, whatever the target's strategy declares).

The two skills are separate and never call each other. `/rota-ship` may invoke both, each behind its own opt-in config flag.

## Per-target strategy files

QA strategy lives in `.rota/qa/<target>.md`, one file per testable surface. In single-repo mode, `<target>` is a user-named surface (`web`, `api`, `cli`, ...). In [umbrella mode](umbrella-mode.md), `<target>` is a registered sub-repo name.

Each strategy file has five body sections:

| Section | Purpose |
|---------|---------|
| **Surface** | What kind of thing this is: web UI, HTTP API, CLI, mobile app, library |
| **Watch globs** | Paths whose changes should trigger post-cycle QA when `qa.afterWork: true` |
| **Executable checks** | Runners with concrete commands, grouped by pillar (performance / security / functional). Each entry: name · command · pass criterion |
| **Audit checks** | Usability dimensions to inspect by hand or LLM (empty states, error recovery, copy clarity, first-run flow). Rubric, no commands |
| **Infra requirements** | What must be running for `run` mode (e.g. `npm run dev` on `:3000`, deployed staging URL, sandbox creds). QA refuses to run if these aren't met |

Strategies are written once via `/rota-qa first-run`, which probes the repo for testing infra, proposes a draft, and writes the file after explicit approval.

## Modes

| Mode | What it does |
|------|-------------|
| `first-run` | Probes surfaces, detects existing test infra, proposes per-target strategy, writes `.rota/qa/<target>.md` after approval. Never installs tooling. |
| `run` | Executes the strategy: infra preflight, parallel runner dispatch, audit pass, scored verdict. Read-only against the codebase; writes artifacts to `.rota/qa-runs/<timestamp>/`. |
| `restructure` | Re-probes surfaces, retires dead strategies, fixes broken commands. Audit equivalent for strategy files. |
| `uat` | Walks one item's acceptance criteria with you. Criteria a passing test proof row covers auto-pass; you judge the rest. Needs no strategy file. See [UAT](#uat-human-acceptance). |

## Verdicts

`/rota-qa run` ends with one of three verdicts and records it with `rota verdict add --kind qa`, which `/rota-ship` routes on:

| Verdict | Meaning |
|---------|---------|
| `PASS` | All executable checks met their pass criteria; no audit blockers found |
| `CONCERNS` | Findings worth surfacing but not blocking; usability or non-critical regressions |
| `FAIL` | One or more executable checks failed against their pass criteria |

A fourth shape, `INFRA-FAIL`, surfaces when required infra is missing (dev server down, binary not installed, creds absent). QA can't run; treat it as advisory, not as a quality signal.

The recording verb posts a marked report on the PR and each linked item, or under `## Reports` in file-mode item details. The report names the checks and existing artifact paths supplied in the verdict body. Recording the same kind and sha updates that report. If posting is pending (for example, no PR exists yet), the local verdict is still saved; `rota ship pr` retries when it opens the PR, or use `rota verdict post <branch>` once the tracker recovers.

## Invocation

Manual:

```
/rota-qa run             # all targets in active scope
/rota-qa run web         # specific target
/rota-qa run --repo api  # umbrella mode, scoped to one sub-repo
/rota-qa run --all       # umbrella mode, every registered sub-repo
```

Triggered by `/rota-ship` when `ship.qa: true`. Runs after `/rota-review` and the second-opinion gate, before merge or PR. Route is controlled by `qa.gate`:

| `qa.gate` | `PASS` | `CONCERNS` | `FAIL` |
|---|---|---|---|
| `"advisory"` (default) | continue silently | surface findings, continue | surface findings, continue |
| `"blocking"` | continue silently | surface, ask you how to proceed | stop; user fixes via `/rota-work` or `/rota-debug` and reruns `/rota-ship` |

## When to use

- *"QA this"*, *"kick the tires"*, *"does this actually work?"*: manual exploratory run after a feature lands.
- Before opening a PR when you want product-level evidence, not just diff sanity.
- First-time setup on a new repo or umbrella sub-repo: bootstrap the strategy file once via `first-run`.

## When NOT to use

- Diff-level review → `/rota-review`. `/rota-qa` does not read commits.
- Nothing built yet → finish via `/rota-work` first. QA needs an artifact to probe.
- Change code based on findings → consume the report, then `/rota-work` or `/rota-debug`.

## Configuration

`/rota-qa` reads these keys from `.rota/config.json`:

- `qa.gate`: `"advisory"` (default) or `"blocking"`. Controls whether `FAIL` halts `/rota-ship` invocations.
- `qa.afterWork`: `false` (default). When `true`, `/rota-work` invokes `/rota-qa run` post-cycle if touched files match a target's `Watch globs`.
- `ship.qa`: `false` (default). When `true`, `/rota-ship` calls `/rota-qa run` between review and the merge/PR step.
- `ship.uat`: `false` (default). When `true`, `/rota-ship` calls `/rota-qa uat` for each item on the branch, after review and before the merge/PR step.

See [configuration](configuration.md#shipqa) for the full block.

## UAT (human acceptance)

`/rota-qa uat <ID>` lists an item's `## Acceptance` criteria and asks you only about the ones no test proves. It always works when you call it; `ship.uat` only decides whether `/rota-ship` calls it for you.

- A criterion auto-passes when `rota proof show <ID>` has a PASS row at the current commit from a test command (`rota proof record`) that names the criterion, and its line does not carry `(human)`.
- A criterion whose line carries the token `(human)` always goes to you, whatever the proof says: `- [ ] AC-4: the error copy reads clearly (human)`. The token does not change the `AC-n` id; `rota plan check` reads the line as before.
- Everything else goes to you as well: no proof row, a row it cannot read, an unclear match. You answer `pass`, `fail` or `skip`, one criterion at a time.
- A failed criterion is captured through `/rota-capture` as a new item. Its body quotes the criterion and `Related:` names the original, which stays open.

The mode reads and asks; it changes no code and records no verdict. It does not diagnose failures into fix plans and keeps no UAT file across `/clear`. Round PRs stay unattended: `rota worker gate` and the round merge path never run it, with the key on or off.

## See also

- [Review and ship](review-and-ship.md): diff-level review and the `/rota-ship` flow that calls `/rota-qa`
- [Configuration](configuration.md): full key reference
