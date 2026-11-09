---
subsystem: gate-landing
summary: manual-gate registry, landing adapters and freeze, post-gate bookkeeping, footprint overlap, ship and release domain logic
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose
Everything between "verified commit" and "on base": the manual gates no autonomy level passes alone, the two ways a commit lands (local git, forge), the repo-wide landing freeze, the bounce/park bookkeeping after a gate or train, footprint matching, the ship rules (strategy, PR body, merge, undo) and the release verbs' logic. The CLI owns flags, envelopes and exit codes; these packages decide.
Depends on: review-verdicts (proof), tracker-issues (tracker, marker, bugreport), backlog-items (backlog, status, blockedby), plus config, fsio, git, jsonx, pathglob, stamp, rotatree, rotastate, worker, repos, exitcode, exitmap, pystr, strutil.

## Entry points
- internal/gate/gate.go:46 — `Registry`, every manual gate (tag-push, release-publish, merge-approval, ...)
- internal/gate/gate.go:104 — `Clear`, checks `--confirm`/`--confirm-note` and writes the audit line
- internal/gate/gate.go:226 — `NewMergePolicy` (`ship.mergeApproval` none/all/paths)
- internal/land/land.go:76 — `MergeLocal`, local merge with one abort attempt on failure
- internal/land/land.go:146 — `MergeForge`, forge merge pinned to the verified head
- internal/landfreeze/freeze.go:26 — freeze `Record`; `Allows` at :147
- internal/postgate/postgate.go:63 — `Gate` (per-item bounce accounting); `Train` at :80
- internal/ship/merge.go:62 — `MergeBranch`; `MergePR` at :183; undo: `PlanUndo` ship/undo.go:51, `ApplyUndo` :161
- internal/release/flow.go:157 — `PlanPush`; `Detect` release/version.go:238, `Bump` :327

## Key files / dirs
- internal/gate/ — gate.go: registry, Confirm, Refused, audit log (`.rota/gate-audit.jsonl`, ReadAudit/ReadAuditStrict), MergePolicy
- internal/land/ — land.go: local and forge landing adapters, ConflictError/MergeError/CleanupError; subject.go (merge/train/sync subjects, IsCycleMerge)
- internal/landfreeze/ — freeze.go: freeze record shared by all worktrees, repair issue/PR clearance
- internal/postgate/ — postgate.go (Book, landed/bounce/park/notify), run.go (Env.Run), report.go (RenderReport)
- internal/overlap/ — overlap.go: Clean, MatchPath, Shared, Filter, Both, Paths, Dirs; `Clean` delegates to internal/pathglob
- internal/ship/ — body.go (PR body), closes.go, merge.go, pr.go, refusal.go, restore.go, strategy.go, undo.go, worktree.go (ClearWorktree), git.go
- internal/release/ — changelog.go, flow.go (push/publish plans), host.go, notes.go, pending.go (release nudge), version.go (version-file registry, semver bump)
- CLI: internal/cli/gate.go, ship.go, ship_finish.go, ship_strategy.go, release.go, release_gate.go, round_freeze.go

## Stack
Go, stdlib only. Git and forge access through injected ports (`land.Git`, `ship.Git`, `release.PushPorts`, `tracker.Adapter`), so logic runs against fakes.

## Conventions specific here
- Invariants: a merge is pinned to a commit the caller verified (`land.ErrUnpinned`); gates (verdict, approval, proof, provenance, freshness) stay with the callers, run before landing.
- Invariants: a gate with `Verbs` refuses without `--confirm` plus a non-empty `--confirm-note`; one without is skill judgment, listed for inventory.
- Invariants: landfreeze writers hold the worker landing lock; the state is never source-controlled and survives a failed gate.
- Invariants: postgate is the single bounce rule for gate and train; a stale merge and a provenance failure count nothing and never park; no gate or train verdict counts a bounce (review bounces are counted by the round review verbs).
- Invariants: overlap works on path lists only (callers own the git diff); `Clean` runs first everywhere; `MatchPath` takes config entries and is a path.Match glob where `**` is one level only (shared-path exemptions must not widen); `Paths`/`Dirs` take footprints (only trailing `/` marks a directory). Recursive `**` and entry matching live in internal/pathglob (`Match`, `MatchEntry`, `MatchUnder`).
- Invariants: `ship.Strategy` owns the landing rule (tracker backlog: pr; local forge: direct); `ship` resets only in `ApplyUndo`.

## Test commands
- go test ./internal/gate/... ./internal/land/... ./internal/landfreeze/... ./internal/postgate/... ./internal/overlap/... ./internal/ship/... ./internal/release/...
- SECTION_LIST=test/sections/77_manual_gates.sh bash test/runner.sh (also 08_ship, 35_undo, 14_release, 96_release_order, 104_gate_stale_merge, 107_gate_lock, 131_gate_closes, 172_round_freeze)

## Concerns
- ship (about 950 non-test lines) mixes unrelated rules (PR body, merge, undo, worktree clearing) in one package.
- postgate imports worker, marker and bugreport, so it depends on the round layer.
- overlap.MatchPath and pathglob.MatchEntry both answer "entry vs file" with different `**` semantics.
