---
subsystem: gate-landing
summary: Manual-gate registry, landing adapters and freeze, post-gate bookkeeping, footprint overlap, ship and release domain logic
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose
Everything between "verified commit" and "on base": the manual gates no autonomy level passes alone, the two ways a commit lands (local git, forge), the repo-wide landing freeze, the bounce/park bookkeeping after a gate or train, footprint matching, the ship rules (strategy, PR body, merge, undo) and the release verbs' logic. The CLI owns flags, envelopes and exit codes; these packages decide.
Depends on: review-verdicts (verdict, proof), tracker-issues (tracker, marker, bugreport), backlog-items (backlog, status, section, blockedby), plus config, fsio, git, jsonx, rotatree, roundcfg, worker, repos, exitcode, exitmap, pystr, strutil.

## Entry points
- internal/gate/gate.go:44 — `Registry`, every manual gate (tag-push, release-publish, merge-approval, ...)
- internal/gate/gate.go:99 — `Clear`, checks `--confirm`/`--confirm-note` and writes the audit line
- internal/gate/gate.go:203 — `LoadMergePolicy` (`ship.mergeApproval` none/all/paths)
- internal/land/land.go:76 — `MergeLocal`, local merge with one abort attempt on failure
- internal/land/land.go:134 — `MergeForge`, forge merge pinned to the verified head
- internal/landfreeze/freeze.go:26 — freeze `Record`; `Allows` at :121
- internal/postgate/postgate.go:48 — `Gate` (per-item bounce accounting); `Train` at :67
- internal/ship/merge.go:31 — `MergeBranch`; `MergePR` at :130; undo: `PlanUndo` ship/undo.go:77, `ApplyUndo` :187
- internal/release/flow.go:155 — `PlanPush`; `Detect` release/version.go:187, `Bump` :298

## Key files / dirs
- internal/gate/ — gate.go: registry, Confirm, Refused, audit log (`.rota/gate-audit.jsonl`), MergePolicy
- internal/land/ — land.go: local and forge landing adapters, ConflictError/MergeError/CleanupError
- internal/landfreeze/ — freeze.go: freeze record shared by all worktrees, repair issue/PR clearance
- internal/postgate/ — postgate.go (landed/bounce/park/notify), report.go (RenderReport)
- internal/overlap/ — overlap.go: Clean, MatchPath, Shared, Filter, Both, Paths, Dirs
- internal/ship/ — body.go (PR body), closes.go, merge.go, pr.go, restore.go, strategy.go, undo.go, verdict.go (VerdictCheck), worktree.go (ClearWorktree)
- internal/release/ — changelog.go, flow.go (push/publish plans), host.go, notes.go, pending.go (release nudge), version.go (version-file registry, semver bump)

## Stack
Go, stdlib only. Git and forge access through injected ports (`land.Git`, `ship.Git`, `release.Git`/`Forge`, `tracker.Adapter`), so logic runs against fakes.

## Conventions specific here
- Invariants: a merge is pinned to a commit the caller verified (`land.ErrUnpinned`); gates (verdict, approval, proof, provenance, freshness) stay with the callers, run before landing.
- Invariants: a gate with `Verbs` refuses without `--confirm` plus a non-empty `--confirm-note`; one without is skill judgment, listed for inventory.
- Invariants: landfreeze writers hold the worker landing lock; the state is never source-controlled and survives a failed gate.
- Invariants: postgate is the single bounce rule for gate and train; a stale merge and a provenance failure count nothing and never park; `--check-only` is never counted.
- Invariants: overlap works on path lists only (callers own the git diff); `Clean` runs first everywhere; `MatchPath` takes config entries (slashless also a directory), `Paths`/`Dirs` take footprints (only trailing `/` marks a directory).
- Invariants: `ship.Strategy` owns the landing rule (tracker backlog: pr; local forge: direct); `ship` resets only in `ApplyUndo`.

## Test commands
- go test ./internal/gate/... ./internal/land/... ./internal/landfreeze/... ./internal/postgate/... ./internal/overlap/... ./internal/ship/... ./internal/release/...
- SECTION_LIST=test/sections/77_manual_gates.sh bash test/runner.sh (also 08_ship, 35_undo, 14_release, 96_release_order, 104_gate_stale_merge, 107_gate_lock, 131_gate_closes, 172_round_freeze)

## Concerns
- ship (1637 lines) mixes unrelated rules (PR body, merge, undo, worktree clearing) in one package.
- postgate imports worker, roundcfg and bugreport, so it depends on the round layer.
