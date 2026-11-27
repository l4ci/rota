---
subsystem: tracker-issues
summary: forge and issue-tracker adapters (gh, glab, Jira, Linear, Itsaplan), upstream-issue verbs, label registry, comment markers and note chunking, item-body grammar, tracker connections, bug-report log
touched: 2026-11-27
last_mapped_commit: f5d97a0faf3575c5fa31e8c2306bf31b0ca1562e
---

## Purpose
The one place rota talks to a tracker or forge. `tracker` normalizes issues and PRs over the forge CLIs and REST APIs and is the only place the forge CLI runs; `issues` is the upstream-issue side (provider detection, label, close); `labels` names every label rota sets; `marker` writes and parses the hidden `<!-- rota:... -->` comment lines, splits long notes into numbered marker comments (`Parts`, note.go) and trims a body to what people see (`Visible`); `itembody` owns the headings an item body carries; `connections` is the machine-wide registry of tracker connections and their secrets; `bugreport` is the local log of rota's own mistakes.
Depends on: tracker -> cfgdir, config, connections, git, labels, marker, proc, pystr, remoteurl, stamp, strutil; issues -> tracker, marker, git, pystr; connections -> cfgdir, fsio, jsonx, proc; bugreport -> fsio, git, remoteurl, rotastate, rotatree, stamp, strutil. Leaves: labels (no rota imports), itembody (section only), marker (pystr only). Used by backlog-items, review-verdicts.

## Entry points
- internal/tracker/tracker.go:357 — `IssueTracker` interface; `Forge` at :432, `Adapter` at :474
- internal/tracker/tracker.go:621 — `New`; `NewForge` :644, `NewIssueTracker` :659
- internal/tracker/cli.go:290 — `CLI.Run`, the single place the forge CLI executes; `ResolveProvider` at :161
- internal/issues/issues.go:66 — `Forge` (the port `issues` calls); `NewEnv` :52, `Label` :121, `Close` :146, `StillOpen` :194
- internal/marker/marker.go:23 — `Line`; `ParseNote` :124, `ParseComment` :142; `Parts` internal/marker/note.go:47 (note splitting), `Visible` internal/marker/visible.go:20
- internal/labels/registry.go:147 — `Resolve`; `Diff` at internal/labels/diff.go:27
- internal/connections/connections.go:144 — `Add`; `Load` :109; secrets: `Secrets.Resolve` internal/connections/secrets.go:121
- internal/bugreport/bugreport.go:79 — `Add`; `Draft` at :447

## Key files / dirs
- internal/tracker/ — tracker.go (types, interfaces, factory), cli.go, cli_base.go (the `base` both gh/glab adapters embed), github.go, gitlab.go, jira.go, linear.go, itsaplan*.go (itsaplan.go, itsaplan_round.go, itsaplan_write.go), rest.go, adf.go (Jira ADF), claims.go, claim_markers.go (`ClaimReader`), closing.go, comment_state.go, connect.go, merge_close.go, key.go, labels.go, issuesource.go (`issueUse`: how an `issues.source` is resolved per caller), shared.go (`firstN`, `uniq`), readcache.go, release.go, report.go, prurl.go, noissues.go, no_milestones.go, assumed.go, classify.go (failure cause and `Kind` rules table), kinds.go (own-tracker kinds and `connect add` flags), fieldsblock.go, trust.go (optional trust capabilities: authorship, push rights, current user), testseam_rotatest.go; test fakes in itsaplantest/, jiratest/, lineartest/; fixtures in testdata/
- internal/issues/ — issues.go (provider detection, label, close, open-only filter; `Env.tracker` returns the `tracker.IssueActor` beside the forge)
- internal/labels/ — registry.go, diff.go, refpage.go; genref/ is a command rendering the label reference
- internal/marker/ — marker.go (all marker kinds, writers and parsers), claims.go (`FoldClaims`: claim comments to a `ClaimState`), note.go (note splitting), visible.go
- internal/itembody/ — itembody.go (section and entry grammar, `AppendDependsOn`), template.go (per-kind), change.go, tryit.go
- internal/connections/ — connections.go (`connections.json`), secrets.go, keychain.go, machine.go, check.go
- internal/bugreport/ — bugreport.go (Record, redaction, Draft)

## Stack
Go 1.22, stdlib only. gh/glab through `tracker.CLI`; Jira, Linear and Itsaplan through HTTP (`rest.go`). Secrets: OS keychain, else a 0600 file in the config dir; `ROTA_<NAME>_TOKEN` overrides both.

## Conventions specific here
- Invariants: adapters hold only small per-adapter facts, each read once and retried on the next call after a failure, never issue lists: GitLab the username `AssignSelf` needs; Jira `myself` and the issue type; Linear the viewer, team id and workflow states, label ids and issue ids; Itsaplan the key's user id, the project metadata (columns, labels, custom fields, assignees, permissions; labels are reread once when a name or id is unknown) and issue ids. The opt-in `ReadCache` (never persisted, cleared by a write) is the only holder of read results; GitHub keeps its issue rows and open lists in a layer of that cache. List calls page until a short page, so results are never silently truncated; each CLI attempt has its own timeout.
- Invariants: every forge call in `issues` goes through `tracker.Adapter`; with `issues.source` jira or linear, label and close go through that tracker.
- Invariants: `marker` is the only writer/parser of `<!-- rota:` lines and still accepts the legacy `<!-- hv:` prefix (#236). Note chunking moved here from the removed `notechunk` package; the per-comment limit is a parameter.
- Invariants: `Visible` caps at `VisibleMax` (32000 runes) so a comment stays under Jira's 32767-character limit.
- Invariants: `BranchPRs`/`BranchPR` (tracker.go:225, :236) are the one answer to which open PR belongs to a branch: the first PR per head branch with `SameRepository` set, so a fork reusing a branch name never counts; every branch-keyed PR reader in cli, round and worker goes through them.
- Invariants: `labels` has no dependency on config, trackers or harnesses.
- Invariants: the connections file holds no secret; a project only names a connection (`issues.connection`).
- Invariants: the bug log is per-developer and gitignored; nothing leaves the machine except `rota bugreport file`, a manual gate.
- Invariants: `itembody` reader and writer share one grammar, pinned by a round-trip test.
- Builds with `-tags rotatest` take the retry pause from `ROTA_ISSUES_RETRY_WAIT_SECONDS` (tracker/testseam_rotatest.go).

## Test commands
- go test ./internal/tracker/... ./internal/issues/... ./internal/labels/... ./internal/marker/... ./internal/itembody/... ./internal/connections/... ./internal/bugreport/...
- SECTION_LIST=test/sections/54_tracker_call.sh bash test/runner.sh (also 34_issues, 56_fake_tracker, 57_tracker_adapters, 58_item_notes, 59_item_claim, 145_fake_tracker_failure)

## Concerns
- tracker is ~9.2k non-test lines (17.3k with 52 test files); github.go, jira.go and linear.go each run about 1000 to 1150 lines (gitlab.go 728).
- Itsaplan (itsaplan*.go) are adapter variants beyond the gh/glab layer the package comment describes.
- `assumedForge` (assumed.go:13, built by `NewIssueTracker` for a forge source whose origin names no forge, `AssumedNote` :31): reads pass through, every write refuses with `UnknownForgeError` (#3972).
- Label-name matching has one owner: `labelRule` in labels.go (case-folding for GitHub, Linear, Itsaplan; exact for GitLab, Jira) read through `sameLabelName` (:70), `labelKey` (:62) and `hasLabel` (:78), and outside the package through `LabelSource`/`LabelMatcher` (`LabelsFor`, `HasLabel`, `CutPrefix`; every adapter and `IssueActor` return their own, `NoIssues` and unknown sources match exactly; `sourceLabelRule` maps an `issues.source` to its rule; `issues.Label` and backlog items match through it); adapters never compare names themselves (#3971). Changing a forge's rule needs a recorded decision. `IssueEdit.NoCreateLabels` (tracker.go:274) is the inverse of `issues.autoCreateLabel`, so the zero value creates (#3973).
