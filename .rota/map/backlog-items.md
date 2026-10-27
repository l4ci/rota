---
subsystem: backlog-items
summary: Backlog item model over file and issue backends, ID counters, plans, designs, spikes, milestones, status.json, debug counter and sessions, shared section/artifact helpers, blockedBy vocabulary
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose
The item and planning state rota keeps. `backlog` presents items from either backend (file: `.rota/BACKLOG.md` plus archive; issues: the tracker, or an umbrella over sub-repos) as one `Item`, plus claim/state/note workflow, capture, shipped audit and removal. `counter` mints file-mode IDs; `plan`, `design`, `spike`, `milestone` own the file-mode (and issue-note) documents; `status` owns `.rota/status.json` and the sub-repo registry; `section` scans `## Topic` sections and writes managed blocks; `artifact` is the glue they share; `debugctr` and `debugsession` keep /rota-debug state; `blockedby` is the refusal vocabulary.
Depends on: tracker-issues (tracker, labels, marker, notechunk, itembody, connections), review-verdicts (acceptance), gate-landing (land), plus config, exitcode, frontmatter, fsio, git, jsonx, pystr, repos, rotatree, roundcfg, stamp, strutil.

## Entry points
- internal/backlog/backend.go:14 — `Backend` interface; `Capabilities` at :73, `Open` at :157
- internal/backlog/workflow.go:14 — `Workflow` (Claim, Release, SetState, Status, Note*)
- internal/backlog/model.go:15 — `Item`; backends `File` file.go:22, `Issues` issue.go:64
- internal/counter/counter.go:26 — `Next`, mints B07/F12/M03 from `.rota/counters.json`
- internal/plan/plan.go:200 — `Add`; `Check` at internal/plan/check.go:78
- internal/milestone/milestone.go:117 — `Add`; `Check` at internal/milestone/check.go:67
- internal/status/status.go:147 — `Add`; `Remove` :186, `Find` :216
- internal/debugctr/debugctr.go:33 — `Open`, the fix-attempt counter; sessions: `debugsession.Put` internal/debugsession/session.go:31
- internal/blockedby/blockedby.go:149 — `All`, the closed set of `blockedBy` values

## Key files / dirs
- internal/backlog/ — model.go, backend.go, file.go/write.go/maintain.go (file side), issue.go/issue_write.go (issues side), umbrella*.go and route.go (sub-repo routing), milestones.go/milestone_store.go, capture.go, ready.go, proofgate.go, view.go, shipped.go, rm.go, bullet.go; trackertest/ (fake tracker), testdata/golden
- internal/counter/ — counter.go
- internal/plan/ — plan.go (Store), files.go, notes.go, check.go, size.go, uncertain.go, validatedocs.go, testdata/
- internal/design/ — design.go, files.go (`.rota/designs/<ID>.md`), notes.go, testdata/
- internal/spike/ — spike.go (`.rota/spikes/`, `spike/<name>` branches)
- internal/milestone/ — milestone.go, store.go, items.go, children.go, check.go, audit.go, capabilities.go, kind.go, decisions.go, shipped.go; testdata/fixture, testdata/golden
- internal/artifact/ — artifact.go (exit-coded Error, `Notes`, `ListDocs`), artifacttest/
- internal/section/ — section.go (`Find`, `Replace`, `Topics`, `Wrap`), testdata/
- internal/status/ — status.go, repos.go
- internal/debugctr/ — debugctr.go (`.rota/debug/<session>.json`)
- internal/debugsession/ — session.go (`.rota/debug/*.md`)
- internal/blockedby/ — blockedby.go

## Stack
Go, stdlib only. Markdown with front matter (`frontmatter`); status and counters as jsonx trees. Issue mode stores plan/design/proof as `NoteGet`/`NotePut` comment notes on the item's issue.

## Conventions specific here
- Invariants: `Backend.List` returns canonical IDs ("B07" file mode, "12" issue mode); callers enumerate through it and never parse `Backend.Markdown`. Callers branch on `Capabilities`, not backend name.
- Invariants: patterns port Python regexps with Unicode classes; use `pystr.SpaceClass` and `\p{Nd}`, with hand-written scanners where lookahead is needed.
- Invariants: `counter` sits below backlog and milestone so neither imports the other to mint an ID; `artifact` must not import internal/cli.
- Invariants: plan/design stores are file (`Files`) or notes (`NewNotes`) behind one `Store`, same verbs for both.
- Invariants: status.json is a jsonx tree, so unknown keys and key order survive a round trip; `section` stays byte-identical with the old helpers.
- Invariants: debugctr keeps the old snake_case keys; session name is the git branch with `/` replaced by `-`.
- Invariants: blockedby strings are declared once, spelling kept as consumers quote them; `TestBlockedByIsClosed` checks them against the contract docs and the source tree.

## Test commands
- go test ./internal/backlog/... ./internal/counter/... ./internal/plan/... ./internal/design/... ./internal/spike/... ./internal/milestone/... ./internal/artifact/... ./internal/section/... ./internal/status/... ./internal/debugctr/... ./internal/debugsession/... ./internal/blockedby/...
- SECTION_LIST=test/sections/06_backlog.sh bash test/runner.sh (also 55_backend, 61_milestones, 41_plan_validate_docs, 125_plan_check, 151_milestone_check, 36_debug_iron_law, 80_iron_law, 154_debug_session, 01_status)

## Concerns
- backlog is ~16.6k lines with 40 test files, the largest package in this map; file, issue and umbrella backends live side by side in it.
- backlog imports milestone and land; plan and design import backlog, milestone imports neither (it uses counter and artifact).
