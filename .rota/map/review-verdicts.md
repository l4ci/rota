---
subsystem: review-verdicts
summary: Typed verdict store and routing, proof rows, acceptance-criteria grammar, test and flake ledgers, golden records, Go test-path rules
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose
The evidence side of shipping. `verdict` records review, qa, ship and debug verdicts through a verb (no report-last-line parsing) and routes verdict to next step; `proof` keeps verification rows on items; `acceptance` parses `## Acceptance` criteria (AC-n ids), marks and coverage; `testledger` is the sanctioned exclusion list of known-red tests; `flakeledger` logs retry recoveries; `golden` pins Go output to approved records; `gopath` says which Go paths are test files.
Depends on: backlog-items (backlog, section, artifact), tracker-issues (marker, tracker), plus config, exitcode, fsio, git, jsonx, pystr, rotastate, rotatree, stamp. Used by gate-landing (ship, gate) and cli, worker, round.

## Entry points
- internal/verdict/verdict.go:228 — `ParseBody`, parses a recorded verdict body
- internal/verdict/verdict.go:456 — `AddBranch`; `AddItem` at :475 (item verdicts, failed-fix counts)
- internal/verdict/route.go:74 — `Route`, verdict to next step; `ProducerNext` :38, `DebugNext` :47
- internal/verdict/missing.go:29 — `MissingReviews`, the missing-review-verdicts rule by review depth
- internal/verdict/lookup.go:57 — `RecoverPR`, recovers a record from a PR report
- internal/proof/proof.go:45 — `Add`; `Proves` at :178, `Mark` at :186
- internal/acceptance/acceptance.go:89 — `Parse`; `Coverage` at :184
- internal/testledger/ledger.go:171 — `Ledger.Excuses`; `Load` at :62, `FailingTests` :153
- internal/golden/golden.go:74 — `Check`

## Key files / dirs
- internal/verdict/ — verdict.go (kinds, verdicts, Finding, Body, Record, Store), route.go, missing.go, lookup.go, followup.go, item_identity.go, report.go
- internal/proof/ — proof.go (Row, Store), files.go (sidecar), notes.go (issue-mode `proof` note), testdata/
- internal/acceptance/ — acceptance.go (pure grammar, no I/O)
- internal/testledger/ — ledger.go (`.rota/test-ledger.json`)
- internal/flakeledger/ — ledger.go (`.rota/flakes.jsonl`, `Rank`)
- internal/golden/ — golden.go, testdata/; records live in `testdata/golden/<test>.json` of each using package
- internal/gopath/ — gopath.go (`IsTestGo`, `IsProdGo`); used by config, stalebin, worker

## Stack
Go, stdlib only. Verdict store is a jsonx file (`rotatree.Verdicts`), gitignored; flake log is JSONL.

## Conventions specific here
- Invariants: verdict kinds and verdicts are fixed sets; verdicts order worst-of (`Worst`); the schema is `docs/contributing/contract/`, "B2: verdicts". `debug-reset` and `work-fix-reset` are written only by their reset verbs.
- Invariants: proof rows are facts, not acceptance; `Add` skips an identical row (date ignored).
- Invariants: acceptance is pure; a criterion is met by an `acceptance` item note mark, coverage read from body, marks and proof rows.
- Invariants: an expired testledger entry fails the gate; each entry carries owner, receipt and expiry.
- Invariants: flakeledger is kept separate from the tracked known-red ledger; one line is one command that passed its retry.
- Invariants: golden fails when the test's inputs differ from the recorded inputs; `-update-golden` rewrites outputs only after the test passes.

## Test commands
- go test ./internal/verdict/... ./internal/proof/... ./internal/acceptance/... ./internal/testledger/... ./internal/flakeledger/... ./internal/golden/... ./internal/gopath/...
- go test ./internal/<pkg> -update-golden (accept changed golden records; read the diff)
- SECTION_LIST=test/sections/78_verdicts.sh bash test/runner.sh (also 52_proof, 119_proof_record, 122_acceptance_ids, 124_test_ledger, 86_merge_approval)

## Concerns
- verdict is 2751 lines; verdict.go holds kinds, parsing, store and persistence together.
- golden is imported by about a dozen packages' tests, so a change to its record format touches all their testdata.
