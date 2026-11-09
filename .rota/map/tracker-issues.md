---
subsystem: tracker-issues
summary: forge and issue-tracker adapters (gh, glab, Jira, Linear, Itsaplan), upstream-issue verbs, label registry, comment markers and note chunking, item-body grammar, tracker connections, bug-report log
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose
The one place rota talks to a tracker or forge. `tracker` normalizes issues and PRs over the forge CLIs and REST APIs and is the only place the forge CLI runs; `issues` is the upstream-issue side (provider detection, label, close); `labels` names every label rota sets; `marker` writes and parses the hidden `<!-- rota:... -->` comment lines, splits long notes into numbered marker comments (`Parts`, note.go) and trims a body to what people see (`Visible`); `itembody` owns the headings an item body carries; `connections` is the machine-wide registry of tracker connections and their secrets; `bugreport` is the local log of rota's own mistakes.
Depends on: tracker -> config, connections, git, labels, marker, proc, projects, pystr, strutil; issues -> tracker, marker, config, git, pystr; connections -> fsio, jsonx, proc, projects; bugreport -> fsio, git, rotastate, rotatree, stamp, strutil. Leaves: labels, itembody (no rota imports), marker (pystr only). Used by backlog-items, review-verdicts.

## Entry points
- internal/tracker/tracker.go:285 — `IssueTracker` interface; `Forge` at :358, `Adapter` at :436
- internal/tracker/tracker.go:536 — `New`; `NewForge` :554, `NewIssueTracker` :569
- internal/tracker/cli.go:313 — `CLI.Run`, the single place the forge CLI executes; `ResolveProvider` at :164
- internal/issues/issues.go:54 — `Provider`; `Label` :115, `Close` :140, `StillOpen` :188
- internal/marker/marker.go:24 — `Line`; `ParseNote` :125, `ParseComment` :143; `Parts` internal/marker/note.go:47 (note splitting), `Visible` internal/marker/visible.go:20
- internal/labels/registry.go:121 — `Resolve`; `Diff` at internal/labels/diff.go:27
- internal/connections/connections.go:144 — `Add`; `Load` :109; secrets: `Secrets.Resolve` internal/connections/secrets.go:122
- internal/bugreport/bugreport.go:80 — `Add`; `Draft` at :460

## Key files / dirs
- internal/tracker/ — tracker.go (types, interfaces, factory), cli.go, github.go, gitlab.go, jira.go, linear.go, itsaplan*.go (itsaplan.go, itsaplan_round.go, itsaplan_write.go), rest.go, adf.go (Jira ADF), claims.go, closing.go, comment_state.go, connect.go, merge_close.go, key.go, labels.go, readcache.go, release.go, report.go, prurl.go, noissues.go, no_milestones.go, assumed.go; test fakes in itsaplantest/, jiratest/, lineartest/; fixtures in testdata/
- internal/issues/ — issues.go (provider, label, close, open-only filter)
- internal/labels/ — registry.go, diff.go, refpage.go; genref/ is a command rendering the label reference
- internal/marker/ — marker.go (all marker kinds, writers and parsers), note.go (note splitting), visible.go
- internal/itembody/ — itembody.go (section and entry grammar, `AppendDependsOn`, `AppendFiles`), template.go (per-kind), change.go, tryit.go
- internal/connections/ — connections.go (`connections.json`), secrets.go, keychain.go, machine.go, check.go
- internal/bugreport/ — bugreport.go (Record, redaction, Draft)

## Stack
Go 1.22, stdlib only. gh/glab through `tracker.CLI`; Jira, Linear and Itsaplan through HTTP (`rest.go`). Secrets: OS keychain, else a 0600 file in the config dir; `ROTA_<NAME>_TOKEN` overrides both.

## Conventions specific here
- Invariants: adapters cache nothing except the GitLab username `AssignSelf` resolves once (and the opt-in `ReadCache`); list calls page until a short page, so results are never silently truncated; each CLI attempt has its own timeout.
- Invariants: every forge call in `issues` goes through `tracker.Adapter`; with `issues.source` jira or linear, label and close go through that tracker.
- Invariants: `marker` is the only writer/parser of `<!-- rota:` lines and still accepts the legacy `<!-- hv:` prefix (#236). Note chunking moved here from the removed `notechunk` package; the per-comment limit is a parameter.
- Invariants: `Visible` caps at `VisibleMax` (32000 runes) so a comment stays under Jira's 32767-character limit.
- Invariants: `labels` has no dependency on config, trackers or harnesses.
- Invariants: the connections file holds no secret; a project only names a connection (`issues.connection`).
- Invariants: the bug log is per-developer and gitignored; nothing leaves the machine except `rota bugreport file`, a manual gate.
- Invariants: `itembody` reader and writer share one grammar, pinned by a round-trip test.
- Builds with `-tags rotatest` take the retry pause from `ROTA_ISSUES_RETRY_WAIT_SECONDS` (tracker/testseam_rotatest.go).

## Test commands
- go test ./internal/tracker/... ./internal/issues/... ./internal/labels/... ./internal/marker/... ./internal/itembody/... ./internal/connections/... ./internal/bugreport/...
- SECTION_LIST=test/sections/54_tracker_call.sh bash test/runner.sh (also 34_issues, 56_fake_tracker, 57_tracker_adapters, 58_item_notes, 59_item_claim, 145_fake_tracker_failure)

## Concerns
- tracker is ~8.5k non-test lines (15.4k with 42 test files); github.go, jira.go and linear.go each run about 1000 lines.
- Itsaplan (itsaplan*.go) and `assumed.go` are adapter variants beyond the gh/glab layer the package comment describes.
