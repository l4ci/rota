---
subsystem: review-verdicts
summary: typed verdict store and routing, review-depth and change-tier policy, proof rows, acceptance-criteria grammar, test and flake ledgers, the test-audit report, failing-test-name parsing, golden records, Go test-path rules
touched: 2026-11-20
last_mapped_commit: 394b73c9c13b7ed81d37a069763e1b19c9a5482d
---

## Purpose
The evidence side of shipping. `verdict` records review, qa, ship and debug verdicts through a verb (no report-last-line parsing) and routes verdict to next step; `change` puts a branch's diff in a tier and `reviewpolicy` resolves the review depth and lens hits from `config.ReviewPolicy` and the text around the diff, so the gate and review do only the work a change needs; `proof` keeps verification rows on items; `acceptance` parses `## Acceptance` criteria (AC-n ids), marks and coverage; `testledger` is the sanctioned exclusion list of known-red tests; `flakeledger` logs retry recoveries; `suiteaudit` backs `rota test-audit` (recorded test costs, checkpoint, test placement inventory); `gotestout` reads failing test names out of `go test` output; `golden` pins Go output to approved records; `gopath` says which Go paths are test files.
Depends on: backlog-items (backlog, artifact, section, only `followup` imports backlog), tracker-issues (marker, tracker, itembody), gate-landing (gate, via suiteaudit), round-orchestration (ledger, via suiteaudit), plus acceptance, config, exitcode, fsio, git, gopath, gotestout, jsonx, pathglob, pystr, rotatree, shlex, stamp, strutil. Used by gate-landing (ship, gate) and cli, worker (changetier, reviewdepth, gate), round.

## Entry points
- internal/verdict/verdict.go:248 — `ParseBody`, parses a recorded verdict body
- internal/verdict/verdict.go:543 — `AddBranch`; `AddItem` at :554 (item verdicts, failed-fix counts)
- internal/verdict/route.go:74 — `Route`, verdict to next step; `ProducerNext` :38, `DebugNext` :47
- internal/verdict/missing.go:29 — `MissingReviews`, the missing-review-verdicts rule by review depth
- internal/change/classify.go:88 — `Classify`, a diff to a `Tier`; `Policy` at :51
- internal/reviewpolicy/reviewpolicy.go:71 — `Decide`, review depth; `MatchLenses` at :203
- internal/proof/proof.go:64 — `Add`; `Proves` at :190, `Mark` at :198
- internal/acceptance/acceptance.go:88 — `Parse`; `Coverage` at :203
- internal/suiteaudit/inventory.go:64 — `Inventory`: test placement report; `ReadTelemetry` report.go:98 (recorded test costs); CLI internal/cli/testaudit.go:20 `rota test-audit`
- internal/testledger/ledger.go:137 — `Ledger.Excuses`; `Load` at :63; it reads failing test names through `gotestout.FailingTests` (internal/gotestout/gotestout.go:31)

## Key files / dirs
- internal/verdict/ — verdict.go (kinds, verdicts, Finding, Body, Record, Store), route.go, missing.go, lookup.go, head.go (`BindHead`, `ResolveHead`), review_sets.go (reviews at a head), followup.go (the `FollowUpTitlePrefix` and floors), reask.go, check.go, item_identity.go, report.go
- internal/change/ — classify.go (`Tier`, `Higher`, `Policy`, `Input`, `Result`); a leaf over `pathglob`
- internal/reviewpolicy/ — reviewpolicy.go (`Input`, `Decision`, `Decide`, `MatchLenses`); config owns the settings, itembody the body grammar
- internal/proof/ — proof.go (Row, Store), files.go (sidecar), notes.go (issue-mode `proof` note), command.go (`{files}` command expansion), testdata/
- internal/acceptance/ — acceptance.go (pure grammar, no I/O)
- internal/testledger/ — ledger.go (`.rota/test-ledger.json`)
- internal/gotestout/ — gotestout.go (`FailingTests`: failing test names out of `go test` output; imported by testledger)
- internal/suiteaudit/ — report.go (checkpoint, telemetry), inventory.go (test placement, Playwright parsing)
- internal/flakeledger/ — ledger.go (`.rota/flakes.jsonl`, `Rank`; writes via `fsio.AppendLine`, reads via `fsio.ReadLines`), retry.go, infrastructure-markers.txt
- internal/followup/ — followup.go (`FileFollowUps`: files deferred review findings as one backlog item; imports acceptance, backlog, verdict, tracker; called by `rota verdict add --file-deferred` in internal/cli/verdict.go)
- internal/golden/ — golden.go, testdata/; records live in `testdata/golden/<test>.json` of each using package
- internal/gopath/ — gopath.go (`IsTestGo`, `IsProdGo`); used by config, stalebin, worker, reviewpolicy

## Stack
Go 1.22, stdlib only. Verdict store is a jsonx file (`rotatree.Verdicts`), gitignored; flake log is JSONL.

## Conventions specific here
- Invariants: verdict kinds and verdicts are fixed sets; verdicts order worst-of (`Worst`); the schema is `docs/contributing/contract/`, "B2: verdicts". `debug-reset` and `work-fix-reset` are written only by their reset verbs.
- Invariants: proof rows are facts, not acceptance; `Add` skips an identical row (date ignored).
- Invariants: acceptance is pure; a criterion is met by an `acceptance` item note mark, coverage read from body, marks and proof rows.
- Invariants: `change` and `reviewpolicy` do no I/O; callers read the diff, PR body and config and hand the facts in. Glob entries match through `pathglob`.
- Invariants: an expired testledger entry fails the gate; each entry carries owner, receipt and expiry.
- Invariants: flakeledger is kept separate from the tracked known-red ledger; one line is one command that passed its retry.
- Invariants: golden fails when the test's inputs differ from the recorded inputs; `-update-golden` rewrites outputs only after the test passes.

## Test commands
- go test ./internal/verdict/... ./internal/followup/... ./internal/change/... ./internal/reviewpolicy/... ./internal/proof/... ./internal/acceptance/... ./internal/testledger/... ./internal/gotestout/... ./internal/flakeledger/... ./internal/suiteaudit/... ./internal/golden/... ./internal/gopath/...
- go test ./internal/<pkg> -update-golden (accept changed golden records; read the diff)
- SECTION_LIST=test/sections/78_verdicts.sh bash test/runner.sh (also 52_proof, 119_proof_record, 122_acceptance_ids, 124_test_ledger, 86_merge_approval)

## Concerns
- verdict is ~1.9k non-test lines across 11 files; verdict.go still holds kinds, parsing, store and persistence together.
- golden is imported by about a dozen packages' tests, so a change to its record format touches all their testdata.
- Review depth is decided in two places: `reviewpolicy.Decide` (config-driven) and `verdict.MissingReviews` (what depth demands).
