---
subsystem: gate-landing
summary: manual-gate registry, landing adapters and freeze, post-gate bookkeeping, footprint overlap, ship and release domain logic
touched: 2026-11-22
last_mapped_commit: 9544e238cf1b73e8e121cbe5007c8993b37af7fd
---

## Purpose
Everything between "verified commit" and "on base": the manual gates no autonomy level passes alone, the two ways a commit lands (local git, forge), the repo-wide landing freeze, the bounce/park bookkeeping after a gate or train, footprint matching, the ship rules (strategy, PR body, merge, undo) and the release verbs' logic. The CLI owns flags, envelopes and exit codes; these packages decide.
Depends on: review-verdicts (proof, flakeledger, testledger via landing), tracker-issues (tracker, marker, bugreport via gatereport and landing), backlog-items (backlog, status, blockedby), plus config, fsio, git, jsonx, pathglob, semver, stamp, rotatree, rotastate, worker (ship only), land (ship), repos, exitcode, exitmap, pystr, strutil. postgate, gatereport and landing do not import worker; worker imports postgate and landing (the round layer sits above this subsystem).

## Entry points
- internal/gate/gate.go:46 — `Registry`, every manual gate (tag-push, release-publish, merge-approval, ...)
- internal/gate/gate.go:104 — `Clear`, checks `--confirm`/`--confirm-note` and writes the audit line
- internal/gate/gate.go:226 — `NewMergePolicy` (`ship.mergeApproval` none/all/paths)
- internal/land/land.go:76 — `MergeLocal`, local merge with one abort attempt on failure
- internal/land/land.go:146 — `MergeForge`, forge merge pinned to the verified head
- internal/landfreeze/freeze.go:24 — freeze `Record`; `Decide` at :174 (the one admission rule)
- internal/postgate/postgate.go:54 — `Gate` (per-item bounce accounting); `Train` at :82; `Book` port at :17
- internal/landing/verdict.go:77 — `ClassifyVerdict`, the single verdict-to-meaning table
- internal/ship/merge.go:39 — `MergeBranch`; `MergePR` at :110; undo: `PlanUndo` internal/ship/undo.go:51, `ApplyUndo` :171
- internal/release/flow.go:157 — `PlanPush`; `Detect` internal/release/version.go:239, `Bump` :314

## Key files / dirs
- internal/gate/ — gate.go: registry, Confirm, Refused, audit log (`.rota/gate-audit.jsonl`, ReadAudit/ReadAuditStrict), MergePolicy
- internal/land/ — land.go: local and forge landing adapters, ConflictError/MergeError/CleanupError; local.go, classify.go (`Kind`: what a `MergeLocal` error means to a caller); subject.go (merge/train/sync subjects, IsCycleMerge)
- internal/landfreeze/ — freeze.go: freeze record shared by all worktrees, repair issue/PR clearance (item-ID checks through `itemid`)
- internal/landing/ — shared gate/train result vocabulary: types.go (`GateResult`, `TrainResult`, `TrainPlan`, `Integration` and the train failure/stop/conflict/culprit types), verdict.go (`ClassifyVerdict`, `VerdictMeaning`, `Refused`), approval.go (`ApprovalThread`), round.go (`RoundMemo`); imported by postgate, gatereport, worker, round, roundtick and cli; imports only blockedby, exitcode, flakeledger, testledger and tracker
- internal/postgate/ — postgate.go (`Book` port, `Gate`, `Train`, landed/bounce/park/notify), run.go (`Env`, `GateRun`, `TrainRun`), options.go; imports only exitcode, gatereport, git and landing
- internal/gatereport/ — report.go (`RenderReport`: the gate report text and its bug-report marker; imports bugreport, landing, marker; used by postgate)
- internal/overlap/ — overlap.go: Clean, Shared, Filter, Both, Paths, Dirs; `Clean` delegates to internal/pathglob, `Shared` to `pathglob.MatchEntry` with the `SingleLevel` flavour
- internal/ship/ — body.go (PR body), closes.go, merge.go (merge failures worded through `land.Failure.Refusal` and `mergeWords`), pr.go, refusal.go, restore.go, strategy.go, undo.go, worktree.go (ClearWorktree; the verdict repo key comes from `repos.VerdictRepo`), git.go
- internal/release/ — changelog.go, flow.go (push/publish plans), host.go, notes.go, pending.go (release nudge), version.go (version-file registry, semver bump via `internal/semver`)
- CLI: internal/cli/gate.go, gate_report.go, ship.go, ship_finish.go, ship_strategy.go, release.go, release_gate.go, round_freeze.go

## Stack
Go, stdlib only. Git and forge access through injected ports (`land.Git`, `ship.Git`, `release.PushPorts`, `tracker.Adapter`), so logic runs against fakes.

## Conventions specific here
- Invariants: a merge is pinned to a commit the caller verified (`land.ErrUnpinned`); gates (verdict, approval, proof, provenance, freshness) stay with the callers, run before landing.
- Invariants: a gate with `Verbs` refuses without `--confirm` plus a non-empty `--confirm-note`; one without is skill judgment, listed for inventory.
- Invariants: landfreeze writers hold the worker landing lock; the state is never source-controlled and survives a failed gate.
- Invariants: postgate is the single bounce rule for gate and train; a stale merge and a provenance failure count nothing and never park; no gate or train verdict counts a bounce (review bounces are counted by the round review verbs).
- Invariants: overlap works on path lists only (callers own the git diff); `Clean` runs first everywhere; `Shared` takes config entries and reads `**` as one level only (`pathglob.SingleLevel`; shared-path exemptions must not widen); `Paths`/`Dirs` take footprints (only trailing `/` marks a directory). Recursive `**` and entry matching live in internal/pathglob (`Match`, `MatchEntry` with its `Flavour`, `MatchUnder`).
- Invariants: `ship.Strategy` owns the landing rule (tracker backlog: pr; local forge: direct); `ship` resets only in `ApplyUndo`.

## Test commands
- go test ./internal/gate/... ./internal/land/... ./internal/landfreeze/... ./internal/landing/... ./internal/postgate/... ./internal/gatereport/... ./internal/overlap/... ./internal/ship/... ./internal/release/...
- SECTION_LIST=test/sections/77_manual_gates.sh bash test/runner.sh (also 08_ship, 35_undo, 14_release, 96_release_order, 104_gate_stale_merge, 107_gate_lock, 131_gate_closes, 172_round_freeze)

## Concerns
- ship (about 860 non-test lines) mixes unrelated rules (PR body, merge, undo, worktree clearing) in one package.
- overlap.Shared and pathglob.Match both answer "entry vs file" with different `**` semantics, chosen by `pathglob.Flavour`.
