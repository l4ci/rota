---
subsystem: test-infra
summary: The merge gate, sharded smoke suite, doc lints, fakes, e2e scenarios, evals and the shared Go test helpers
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose

Everything that checks rota rather than ships it: the full merge gate (`test/gate.sh`), the fast per-change tier (`test/fast.sh`), the smoke suite of numbered sections, doc and skill lints, fake external tools, tmux/herdr e2e scenarios, behavioural skill evals, root-level Go tests for the embedded skills and plugins, and the Go helpers that isolate tests from the developer's machine.

Depends on (by map file): cli-dispatch (the built `rota` binary, `cmd/rota` frozen tests), skills-packaging (`validate-skills.py`, `skills-docs.py --check`, plugin manifests), foundations (`gittest` wraps git). Used by every Go package (`testiso`, `gittest`).

## Entry points
- test/gate.sh:1 — full gate: validate-skills, doclint, grep gate, gofmt, `go vet`, `go test -race`, sharded smoke, all concurrent
- test/fast.sh:1 — maps changed files to the targeted checks (`rota test run fast`)
- test/runner.sh:1 — smoke runner: builds a throwaway `.rota/` and sources `test/sections/*.sh` in order (`test/smoke.sh` is a wrapper)
- test/doclint.sh:1 — repository doc lints, no smoke state
- internal/testiso/testiso.go:16 — `init`: drops `ROTA_CONFIG_DIR`, points `XDG_CONFIG_HOME` at a missing path
- internal/gittest/gittest.go:118 — `NewRepo`; `Run` at :48, `Init` at :66, `Commit` at :112
- cmd/rota/frozen_test.go:169 — `runFrozen`: frozen-output suites against `cmd/rota/testdata/frozen`
- plugin_test.go:18 — `TestPluginManifest`; root tests also cover the Codex tree and skill alignment

## Key files / dirs
- `test/sections/` — numbered `NN_name.sh` smoke sections (about 150), template in `test/section-template.sh`, helpers in `test/lib.sh`
- `test/lib/` — `isolate.sh` (env and worktree isolation), `gate-lock.sh`, `checkout-guard.py`, `gate-lock-race.py`
- `test/gate-go.py`, `gate-smoke.py`, `gate-flake.py`, `shard-weights.txt` — gate parts and shard weights
- `test/fakes/` — `gh`, `glab`, `tmux`, `herdr`, `codex`, `opencode`, `stub_worker`, `fake_forge.py`, `fake_tracker.py`, `codex_skill_worker.py`
- `test/e2e/` — `run.sh`, `lib.sh`, `scenarios/*.sh` (watcher, dead worker, usage limit, train bisect)
- `test/evals/` — behavioural skill evals (`run.py`, `scenarios/`, `triggers.json`, `baseline/`)
- `test/validate-skills.py`, `lint-sections.py`, `lint-verbs.py`, `check-doc-stamps.py`, `gofmt.sh`, `grep-gate.sh`
- Root `*_test.go`: `plugin_test.go`, `plugin_hook_test.go`, `codex_plugin_test.go`, `skill_alignment_test.go`, `skill_autonomy_test.go`
- `internal/testiso/` — blank-import package isolating global config; `internal/gittest/` — shared git fixtures

## Stack
Bash and Python 3 standard library for the suite; Go 1.22 `testing`. Fakes stand in for `gh`, `glab`, `tmux`, `herdr`, codex and opencode.

## Conventions specific here
- Invariants: sections run under `set -euo pipefail`, so a verb capture is written `OUT=$(rota ...) || vfail`; `lint-sections.py` flags bare captures. A section may use state from earlier sections in its own shard but not from another shard (`--random` checks this).
- Only one gate runs per machine; a second waits. The gate also takes a slot of the repo test lock.
- Failed Go packages and smoke shards retry once after the first pass.
- The fake `herdr` only appends to a log and prints canned JSON; never put the real `herdr` on a test's PATH. e2e uses poison stand-ins for tmux, herdr, gh and glab.
- A Go test package that loads config imports `testiso` blank; `gittest` fixes identity, default branch and symlink resolution.
- `--smoke-only` skips the doc lints; they run in the Skill Format workflow.
- Doc contract files name the paths they describe in front matter, with last-verified commits in `docs/contributing/contract/STAMPS` (`check-doc-stamps.py`).

## Test commands
- Full gate: `bash test/gate.sh` (about 2-3 minutes; run at integration boundaries only)
- Fast tier: `bash test/fast.sh <files...>`
- One smoke section in a sandbox: `bash test/runner.sh` with `SECTION_LIST` set (see `test/runner.sh`)
- `bash test/doclint.sh`, `python3 test/validate-skills.py`, `bash test/e2e/run.sh`
- `go test ./internal/testiso/ ./internal/gittest/ .`

## Concerns
- The smoke suite is slow when run whole (about 19600 lines across the sections); only the sharded gate is fast.
- `test/grep-gate.sh` is documented as the final cutover gate for an earlier migration and is not part of smoke.
