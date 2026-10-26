---
skill: rota-qa
lane: ship
order: 120
situation: Bigger picture
summary: Run product-level QA against the built artifact and score it.
not_when:
  - use: rota-review
    when: you want a read of the diff and commits, not a run of the product
  - use: rota-work
    when: nothing is built yet
  - use: rota-debug
    when: QA found a failure and you need to fix it
before: [rota-work]
after: [rota-ship]
manual_only: false
flags:
  - flag: "first-run"
    does: probe surfaces and propose a strategy in .rota/qa/<target>.md
  - flag: "run [<target>]"
    does: execute the strategy and return PASS, CONCERNS, FAIL or INFRA-FAIL
  - flag: "restructure"
    does: audit strategy files, retire dead ones, fix broken commands
  - flag: "uat <ID>"
    does: walk one item's acceptance criteria with you
example:
  source: usage/qa.md#invocation
  reviewed: true
  steps:
    - you: /rota-qa run web
    - agent: |
        Infra preflight: dev server on :3000 reachable.
        Executable checks: 4 run, 4 met their pass criteria.
        Audit pass: no blockers.
        Verdict: PASS (recorded as a qa verdict; artifacts under .rota/qa-runs/<timestamp>/)
  changes:
    - artifacts under .rota/qa-runs/<timestamp>/
    - proof rows for the items on the branch
    - a qa verdict that /rota-ship routes on
---
# /rota-qa

Product-level QA: runs the per-target strategy declared in `.rota/qa/<target>.md` and emits a scored verdict. `/rota-qa` does NOT read commits or the diff (that's `/rota-review`'s job). It runs the built artifact: Playwright, smoke scripts, contract tests, Lighthouse, axe, ZAP, whatever the strategy declares, grouped into executable and audit pillars. Three modes: `first-run` (probe surfaces, propose strategy), `run` (execute, score, verdict), `restructure` (audit strategy files). Returns `PASS` / `CONCERNS` / `FAIL`, or `INFRA-FAIL` when required infra is missing. Invoked from `/rota-ship` when `ship.qa: true`; route on verdict gated by `qa.gate` (`"advisory"` reports only, `"blocking"` halts on `FAIL`). See [product QA](../usage/qa.md) for the full flow.
