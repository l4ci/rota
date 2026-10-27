---
subsystem: tracker-issues
summary: Forge and issue-tracker adapters (gh, glab, Jira, Linear, Itsaplan), upstream-issue verbs, label registry, comment markers, note chunking, item-body grammar, tracker connections, bug-report log
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose
The one place rota talks to a tracker or forge. `tracker` normalizes issues and PRs over the forge CLIs and REST APIs and is the only place the forge CLI runs; `issues` is the upstream-issue side (provider detection, list, label, close); `labels` names every label rota sets; `marker` writes and parses the hidden `<!-- rota:... -->` comment lines; `notechunk` splits long notes into numbered marker comments; `itembody` owns the headings an item body carries; `connections` is the machine-wide registry of tracker connections and their secrets; `bugreport` is the local log of rota's own mistakes.
Depends on: tracker -> config, connections, git, labels, marker, proc, projects, strutil; issues -> backlog-items (backlog), tracker, marker, config, git, pystr; notechunk -> marker; connections -> fsio, jsonx, proc, projects; bugreport -> fsio, git, rotastate, rotatree. Leaves: labels, itembody (no rota imports), marker (pystr only).

## Entry points
- internal/tracker/tracker.go:283 — `IssueTracker` interface; `Forge` at :356, `Adapter` at :434
- internal/tracker/tracker.go:534 — `New`; `NewForge` :552, `NewIssueTracker` :567
- internal/tracker/cli.go:313 — `CLI.Run`, the single place the forge CLI executes; `ResolveProvider` at :164
- internal/issues/issues.go:54 — `Provider`; `Label` :119, `Close` :144, `StillOpen` :192
- internal/marker/marker.go:24 — `Line`; `ParseNote` :100, `ParseComment` :118
- internal/labels/registry.go:121 — `Resolve`; `Diff` at internal/labels/diff.go:27
- internal/notechunk/notechunk.go:48 — `Parts(kind, text, limit)`
- internal/connections/connections.go:144 — `Add`; `Load` :109; secrets: `Secrets.Resolve` internal/connections/secrets.go:115
- internal/bugreport/bugreport.go:77 — `Add`; `Draft` at :457

## Key files / dirs
- internal/tracker/ — tracker.go (types, interfaces, factory), cli.go, github.go, gitlab.go, jira.go, linear.go, itsaplan*.go, rest.go, adf.go (Jira ADF), claims.go, closing.go, key.go, labels.go, readcache.go, release.go, report.go, prurl.go, noissues.go, assumed.go; test fakes in itsaplantest/, jiratest/, lineartest/; fixtures in testdata/
- internal/issues/ — issues.go (provider, label, close, open-only filter)
- internal/labels/ — registry.go, diff.go, refpage.go; genref/ is a command rendering the label reference
- internal/marker/ — marker.go (all marker kinds, writers and parsers)
- internal/notechunk/ — notechunk.go
- internal/itembody/ — itembody.go, template.go (per-kind), change.go, tryit.go
- internal/connections/ — connections.go (`connections.json`), secrets.go, keychain.go, machine.go, check.go
- internal/bugreport/ — bugreport.go (Record, redaction, Draft)

## Stack
Go, stdlib only. gh/glab through `tracker.CLI`; Jira, Linear and Itsaplan through HTTP (`rest.go`). Secrets: OS keychain, else a 0600 file in the config dir; `ROTA_<NAME>_TOKEN` overrides both.

## Conventions specific here
- Invariants: adapters cache nothing except the GitLab username `AssignSelf` resolves once (and the opt-in `ReadCache`); list calls page until a short page, so results are never silently truncated; each CLI attempt has its own timeout.
- Invariants: every forge call in `issues` goes through `tracker.Adapter`; with `issues.source` jira or linear, label and close go through that tracker.
- Invariants: `marker` is the only writer/parser of `<!-- rota:` lines and still accepts the legacy `<!-- hv:` prefix (#236).
- Invariants: `labels` has no dependency on config, trackers or harnesses.
- Invariants: the connections file holds no secret; a project only names a connection (`issues.connection`).
- Invariants: the bug log is per-developer and gitignored; nothing leaves the machine except `rota bugreport file`, a manual gate.
- Invariants: `itembody` reader and writer share one grammar, pinned by a round-trip test.
- Builds with `-tags rotatest` take the retry pause from `ROTA_ISSUES_RETRY_WAIT_SECONDS` (tracker/testseam_rotatest.go).

## Test commands
- go test ./internal/tracker/... ./internal/issues/... ./internal/labels/... ./internal/marker/... ./internal/notechunk/... ./internal/itembody/... ./internal/connections/... ./internal/bugreport/...
- SECTION_LIST=test/sections/54_tracker_call.sh bash test/runner.sh (also 34_issues, 56_fake_tracker, 57_tracker_adapters, 58_item_notes, 59_item_claim, 145_fake_tracker_failure)

## Concerns
- tracker is ~14.6k lines across 37 test files; github.go, jira.go and linear.go each run 1000+ lines.
- Itsaplan (itsaplan*.go) and `assumed.go` are adapter variants beyond the gh/glab layer the package comment describes.
