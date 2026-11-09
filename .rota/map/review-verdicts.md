---
subsystem: review-verdicts
summary: typed verdict store and routing, review-depth and change-tier policy, proof rows, acceptance-criteria grammar, test and flake ledgers, golden records, Go test-path rules
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose
The evidence side of shipping. `verdict` records review, qa, ship and debug verdicts through a verb (no report-last-line parsing) and routes verdict to next step; `change` puts a branch's diff in a tier and `reviewpolicy` resolves the review depth and lens hits from `config.ReviewPolicy` and the text around the diff, so the gate and review do only the work a change needs; `proof` keeps verification rows on items; `acceptance` parses `## Acceptance` criteria (AC-n ids), marks and coverage; `testledger` is the sanctioned exclusion list of known-red tests; `flakeledger` logs retry recoveries; `golden` pins Go output to approved records; `gopath` says which Go paths are test files.
Depends on: backlog-items (backlog, artifact, section), tracker-issues (marker, tracker, itembody), plus acceptance, config, exitcode, fsio, git, gopath, jsonx, pathglob, pystr, rotatree, shlex, stamp. Used by gate-landing (ship, gate) and cli, worker (changetier, reviewdepth, gate), round.

## Entry points
- internal/verdict/verdict.go:238 — `ParseBody`, parses a recorded verdict body
- internal/verdict/verdict.go:520 — `AddBranch`; `AddItem` at :531 (item verdicts, failed-fix counts)
- internal/verdict/route.go:74 — `Route`, verdict to next step; `ProducerNext` :38, `DebugNext` :47
- internal/verdict/missing.go:29 — `MissingReviews`, the missing-review-verdicts rule by review depth
- internal/change/classify.go:87 — `Classify`, a diff to a `Tier`; `Policy` at :51
- internal/reviewpolicy/reviewpolicy.go:64 — `Decide`, review depth; `MatchLenses` at :192
- internal/proof/proof.go:44 — `Add`; `Proves` at :171, `Mark` at :179
- internal/acceptance/acceptance.go:88 — `Parse`; `Coverage` at :183
- internal/testledger/ledger.go:172 — `Ledger.Excuses`; `Load` at :63, `FailingTests` :154

## Key files / dirs
- internal/verdict/ — verdict.go (kinds, verdicts, Finding, Body, Record, Store), route.go, missing.go, lookup.go, head.go (`BindHead`, `ResolveHead`), review_sets.go (reviews at a head), followup.go, check.go, item_identity.go, report.go
- internal/change/ — classify.go (`Tier`, `Higher`, `Policy`, `Input`, `Result`), glob.go (`ValidGlob`); a leaf over `pathglob`
- internal/reviewpolicy/ — reviewpolicy.go (`Input`, `Decision`, `Decide`, `MatchLenses`); config owns the settings, itembody the body grammar
- internal/proof/ — proof.go (Row, Store), files.go (sidecar), notes.go (issue-mode `proof` note), command.go (`{files}` command expansion), testdata/
- internal/acceptance/ — acceptance.go (pure grammar, no I/O)
- internal/testledger/ — ledger.go (`.rota/test-ledger.json`)
- internal/flakeledger/ — ledger.go (`.rota/flakes.jsonl`, `Rank`; writes via `fsio.AppendLine`, reads via `fsio.ReadLines`), retry.go, infrastructure-markers.txt
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
- go test ./internal/verdict/... ./internal/change/... ./internal/reviewpolicy/... ./internal/proof/... ./internal/acceptance/... ./internal/testledger/... ./internal/flakeledger/... ./internal/golden/... ./internal/gopath/...
- go test ./internal/<pkg> -update-golden (accept changed golden records; read the diff)
- SECTION_LIST=test/sections/78_verdicts.sh bash test/runner.sh (also 52_proof, 119_proof_record, 122_acceptance_ids, 124_test_ledger, 86_merge_approval)

## Concerns
- verdict is ~3.7k lines across 9 files; verdict.go still holds kinds, parsing, store and persistence together.
- golden is imported by about a dozen packages' tests, so a change to its record format touches all their testdata.
- Review depth is decided in two places: `reviewpolicy.Decide` (config-driven) and `verdict.MissingReviews` (what depth demands).
