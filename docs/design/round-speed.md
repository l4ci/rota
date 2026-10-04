# Why rounds are slow

Research for #46. Nothing here changes behaviour; the ranked fixes in the last section become follow-up issues.

Measured 2026-10-04 on an 8-core, 15 GB box, Go 1.22.12, at `c9b5eef` (`main` before #47). Every timing carries the load average (`uptime`, 1/5/15 min) from the start and end of its run, because other workers shared the machine. All runs were in a scratch worktree with the default shared `GOCACHE`.

## Short answer

The merge gate is the cost, and it was paid many times per PR. One full gate takes about 10 minutes on an idle machine: smoke 458 s plus `go test -race` 129 s, run in series. Smoke is a single-threaded bash run, so it leaves 7 of 8 cores idle. Cut to one gate per PR (done in #47), the next win is running the gate in parallel: smoke split into four shards, run next to `go test`, took 159 s for the same checks.

Waiting dominates the rest of a PR's life, not work: worker PRs sat open 10 to 122 minutes before merging.

## 1. Smoke, per section

Two full runs of `bash test/smoke.sh` through a timing copy of `test/runner.sh` (it only wraps the `source "$f"` line; the repo file is untouched).

| Run | Load at start, end | Wall |
|---|---|---|
| 1 | 3.06 -> 2.27 (5-min avg 7.6 -> 5.2) | 510 s |
| 2 (idle) | 1.35 -> 0.66 | 458 s |

`rounds.md` said "~75 s". That number is stale by 6x on this suite (94 timed sections).

Top 10 sections, run 2 (idle) against run 1:

| # | Section | Idle s | Run 1 s |
|---|---|---|---|
| 1 | 92_limits | 46.3 | 46.5 |
| 2 | 55_backend | 39.5 | 42.7 |
| 3 | 61_milestones | 34.5 | 35.3 |
| 4 | 87_round_return | 34.3 | 35.7 |
| 5 | 97_install_sh | 30.6 | 30.7 |
| 6 | 58_item_notes | 24.9 | 24.6 |
| 7 | 59_item_claim | 22.2 | 22.5 |
| 8 | 65_migrate_issues | 22.0 | 40.9 |
| 9 | 64_umbrella_issues | 21.3 | 25.3 |
| 10 | 49_worker_dispatch | 18.6 | 18.9 |

The top 10 are 66% of the time (294 s of 447 s). 77 of 94 sections take under 5 s; the median is 1 s. Load barely moved most sections (+11% wall overall), except 65 (1.9x), so smoke is not CPU-starved by other workers the way Go is.

Where the time goes, from `strace -f -e trace=execve` on two slow sections:

| Section | Process execs | Biggest |
|---|---|---|
| 55_backend | 3033 | python3 748, dirname 528, bash 453, rota 319, fake gh 231, fake glab 210 |
| 87_round_return | 2156 | dirname 343, bash 340, python3 331, git 278, fake gh 262 |

- The `rota` binary starts in 8 ms. The Go side is not why smoke is slow.
- `python3` starts in 23 ms (`python3 -S`: 12 ms). It is the fakes (`gh`/`glab` are bash wrappers around `fake_tracker.py`) and the `jget`-style helpers. 748 execs x 23 ms is about 17 s of 55's 40 s; I measured two sections, not the whole suite.
- 92_limits is real waiting: 12 `sleep` calls plus `limit watch --timeout 1` runs.
- 97_install_sh: 30 s, no `rota` or git calls in the section; not profiled further.

### Does smoke need to be serial?

`runner.sh` says "sequential by design, state accumulates". I tested it. Greedy-balanced the 94 sections by run-2 timing into four lists (predicted 111.7 s each) and ran four `SECTION_LIST` runners at once:

| Setup | Load start -> end | Wall | Result |
|---|---|---|---|
| 1 runner, all sections | 1.35 -> 0.66 | 458 s | pass |
| 4 shards in parallel | 0.64 -> 3.27 | 116 s | all four pass |

3.95x. One partition passing does not prove every partition passes; it shows no section in this set needs another shard's state, and that the runner's per-run `mktemp` roots keep shards from colliding (no ports, no shared dirs). The "accumulates state" comment holds within a section range, not across the whole suite as it stands today.

## 2. Go: `go vet` and `go test -race ./...`

`-count=1` throughout, to defeat the result cache.

| Run | Load start -> end | Wall |
|---|---|---|
| `go vet ./...` (warm cache) | 2.08 -> 3.43 | 1 s |
| `go test -race -count=1 ./...` | 3.24 -> 11.99 | 129 s |
| `go test -count=1 ./...` (no race) | 10.44 -> 11.55 | 95 s |
| `go test -race` next to 4 smoke shards | 2.21 -> 9.60 | 128 s |

Per package, race run: `cmd/rota` 111.9 s, `internal/cli` 57.1, `internal/round` 43.2, `internal/worker` 42.3, `internal/backlog` 14.8, then everything else under 8 s. Packages already run in parallel, so wall time is the slowest package: `cmd/rota`.

Inside `cmd/rota` (race, summed test time 75.9 s over 14 top-level tests): `TestFrozenA4D` 42.7 s, `TestFrozenA4Umbrella` 10.8, `TestFrozenA4Issue` 7.8, `TestFrozenA4B` 6.2, `TestFrozenA4` 4.5. One suite is 56% of the package's test time.

Race vs no race: 129 s vs 95 s, a 1.36x gap, but the two runs had different load (3 -> 12 vs 10 -> 11), so treat it as an upper bound on race cost. I did not get a clean pair.

### GOCACHE and worktrees

Cache is already shared: the default `GOCACHE` (`~/.cache/go-build`) is content-addressed, so every worktree on the same checkout contents reuses it. Warm race compile of all packages: 9 s. Cold (empty `GOCACHE`, load 8 -> 5): build 14 s, vet 3 s, race compile 53 s, 297 MB. A worktree does not rebuild from scratch; a brand-new machine or cleaned cache pays about 70 s once. Nothing to fix here.

## 3. The gate as configured

`refactor.verifyCommands` is a serial list: validate-skills, `go vet`, `go test -race`, smoke. Idle-machine serial cost from the numbers above: about 1 + 1 + 129 + 458 = 589 s, matching the orchestrator's 9 to 13.5 min under load. Go test is multi-core; smoke is one core. Nothing runs them together.

Measured parallel version (race test + 4 smoke shards at once): 159 s total (go 128 s, shards 157 to 159 s), load 2.21 -> 9.60. That is 3.7x against the serial 589 s.

## 4. Round 1 timeline

From `gh pr list` on 2026-10-04 (25 PRs created, 18 merged, 7 open at 19:45 UTC). Only claim comments on the issue give a start time; PRs without an issue have none.

- 18 PRs merged in 6 h 23 m (13:22 to 19:45 UTC). 9 of them merged within 4 minutes of opening (doc, backlog and formula moves; 3 s to 4 min), so they never queued.
- Worker PRs, open to merge: #12 102 min, #13 122 min, #34 76 min, #36 56 min, #35 40 min, #30 19 min, #32 19 min, #28 10 min. Median about 19 min over all 18, but 40 to 122 minutes for the bigger ones. These are mostly queue time: the merge gate ran for 10 to 13 min per PR in series, one PR at a time.
- Claim to PR open (the implement phase), 7 PRs: 1 min to 76 min, median about 15 min.
- Stale-branch bounces (merge-main commits on a PR branch): #36 4, #42 6, #41 5, #45 2, #32/#37/#39/#44 1 each. The open PRs with 5 to 6 bounces carried 18 commits each.
- Gate and test mentions in PR bodies: 19 of 25 PRs mention a gate or smoke run; #36 mentions it 15 times, #44 11. Mentions are a rough proxy only: PR bodies do not say how many times the suite ran, and pane logs were not available to me. I could not reproduce the issue's "3 to 5 full gate runs per PR" from PR bodies; #36 is the clear example, the rest I can neither confirm nor rule out.

The split I can support with data: implement is short (median about 15 min), gate is about 10 min per run when idle, and wait-for-merge is the long tail. #47 already removed the worker-side full gate and the re-run after a stale bounce; those were the multiplied terms.

## 5. Not measured

- **Plain agent vs round worker baseline.** Not done. No open issue was small enough to run twice without touching the round's live branches, and a rigged task would not tell us what the contract skips. Round workers skip nothing the gate checks; what the contract adds is reading the thread, the PR body with `## Approvals`, and the sentinels. A fair baseline needs one real small issue assigned to both setups; propose it as a follow-up with the next doc-only issue.
- **Per-PR gate counts from panes.** Panes were not readable from a worker. Needs the orchestrator's logs.
- **Clean race-vs-no-race pair.** Load differed (see section 2).

## 6. Side findings

- `/tmp` held 267 `hv-gate-verify-*` directories, 6 `go-build*` and 7 `cli-tripwire*` leftovers (1.1 MB for the first set). The disk was 94 to 95% full (3.9 GB free) during my runs. A full disk turns into false reds; worth a cleanup step and a trap in whatever creates them. I deleted none of them; they are not mine.
- `rounds.md` quoted smoke at "~75 s". #47 rewrote the gate section on main; check no other doc still says it.

## 7. Ranked fixes

Expected time saved is per merge gate, idle machine, against the 589 s baseline.

| # | Fix | Expected saving | Quality risk |
|---|---|---|---|
| 1 | Run the gate's checks concurrently and shard smoke four ways: `refactor.verifyCommands` entries marked parallel, or one `test/gate.sh` that does it; `SECTION_LIST` sharding already exists | ~430 s (589 -> 159 s measured) | Low. Same checks, same pass line. Risk is hidden cross-section state in a future section; add a guard (below) |
| 2 | Merge train: verify a batch of N ready PRs once on an integration branch, then fast-forward `main` (`orch/train1` already exists in the log) | (N-1) x gate per batch; with N=4 about 3 gates saved | Medium. A red batch needs bisecting; mitigate by keeping the fast gate cheap and splitting on failure |
| 3 | Shard-safety guard: CI (or `rota worker gate`) runs smoke sharded with a random partition at least on `main`, so a section that starts depending on another's state fails loudly | Keeps #1 honest, costs 0 s extra | Removes #1's main risk |
| 4 | Parallelise the `cmd/rota` frozen suites (`TestFrozenA4D` is 42.7 s); go wall time is set by this one package | up to ~55 s of the 128 s Go wall (next package is 57 s) | Medium. Scenarios must not share dirs or the binary build; unverified |
| 5 | Speed up the smoke hot spots: `python3 -S`/one persistent fake instead of 748 python3 execs in 55; shorten the real sleeps in 92 | ~10 to 20 s from 55, ~20 to 30 s from 92 (estimates, two sections profiled) | Low for the fake change; 92 tests real timing, keep one real-time case |
| 6 | Concurrency limit on gates (`rota worker gate` takes a lock): two gates at once on 8 cores starve the Go tests and cause false reds | No time saved on a gate; avoids the 12-load reruns | Low |
| 7 | Cap the stale-bounce loop (#31 does this part) and let the gate rebase clean branches itself | Saves a manual merge-main round per stale PR (~7 in round 1) | Low |
| 8 | Fix docs: `rounds.md` smoke time, `runner.sh` "sequential by design" comment | 0 s; stops bad planning | None |
| 9 | Clean up `/tmp` leaks and guard disk space before the gate | 0 s; prevents false reds | None |

Considered and not proposed:

- **Race only on changed packages.** Race costs about 1.2 to 1.4x here, and `cmd/rota` and `internal/cli` import most of the tree, so a change anywhere re-races the slowest package anyway. Not worth the quality risk.
- **GOCACHE sharing.** Already shared (section 2).
- **Worker-side targeted gate vs full gate at merge.** Settled by #47: full gate once, at merge. Item 1 and 2 make that one gate cheap, which is what lets it stay the only one.

## 8. Results of #84 (smoke hot spots and the frozen suites)

Measured 2026-10-04, same box, `go 1.22`. The machine was shared with other workers and the load average swung from 5 to 29 during these runs, so wall times alone mislead. CPU seconds (user + system, children included) do not move with load, and are the column to trust. "Before" is the tree with #82 and #85 merged and nothing else (`dc53724`); "after" is this branch. Each row is one `SECTION_LIST` run of the section.

| Section | Before wall (load) | Before CPU | After wall (load) | After CPU |
|---|---|---|---|---|
| 92_limits | 52.3 s (16.9) | 15.6 s | 20.9 s (11.8) | 11.3 s |
| 55_backend | 74.6 s (22.5) | 46.1 s | 27.7 s (8.5) | 29.4 s |
| 61_milestones | 117.8 s (29.0) | 43.7 s | 20.1 s (7.0) | 22.8 s |
| 87_round_return | 47.6 s (22.1) | 22.5 s | 13.5 s (6.3) | 13.5 s |
| 97_install_sh | 32.0 s (15.3) | 2.5 s | 2.1 s (6.4) | 2.4 s |

What changed, by cause:

- **Fake `gh`/`glab`: 80 ms to 45 ms a call.** `fake_tracker.py` ran as a script, so Python recompiled its 788 lines on every call, and it imported `hashlib`, `subprocess` and `datetime` that most calls never use. The wrappers now import it as a module (bytecode cached in `test/fakes/__pycache__`, gitignored), run `python3 -S`, and load those three on first use. This is the CPU drop in 55, 61 and 87, and in every other section that calls a fake, and it also speeds the Go scenario suites, which shell out to the same fakes.
- **tmux settle pause.** After pasting a brief, rota waits 1 s, sends Enter, then waits 2 s, for Claude Code to take the paste. The fake tmux reacts at once, so every dispatch in smoke paid 3 s. `ROTA_HOST_SETTLE_PCT` scales those pauses (default 100, so rota itself is unchanged); the runner sets 5. The scaling is covered by `TestSettleScale`.
- **97_install_sh: 30 s to 2 s.** The signal test killed the installer while a fake `curl` slept 30 s. `sh` defers a trap until its foreground child exits, so the test waited out the sleep. The fake curl now gets the signal too, as a terminal's ^C would deliver it. The assertion (no temp files left after a TERM) is unchanged.
- **92_limits.** Twelve `limit watch --timeout 1` runs now use `--timeout 0.3`, and the fake herdr's emit delay and three herdr watch timeouts were cut. About 8 s of what remains is real waiting: one herdr event watch, the 1 s no-loop check, and the watches' own timeouts. No assertion was loosened.
- **Not changed:** `55_backend`'s `sleep 1` between two closes (the fake store stamps closes by the second), and 92's `sleep 1` that checks `--no-limits` starts no loop (shortening it would shorten the check).

Go: `go test -race -count=1 -run TestFrozen ./cmd/rota` went from 95.0 s wall (load 5.5 to 11.9, 462 CPU s) to 60.5 s (load 8.4 to 21.3, 338 CPU s). The record of the running suite was one package variable, so the seven suites ran one after another and each left cores idle at its tail. It is now keyed by suite and each `TestFrozen*` calls `t.Parallel()`. Per suite wall (before, run alone in sequence, then after, all seven sharing the pool): A4 4.5/22.8, A4B 6.6/31.8, A4C 0.8/2.0, A4D 47.1/57.3, A4Issue 9.9/23.2, A4Umbrella 13.5/33.5, A4UmbrellaFile 3.0/11.8. The "after" per-suite figures are longer because they run concurrently; the saving is the total. `TestFrozenA4D` is 305 scenarios of 1 to 5 s each, so it is bound by total CPU, not by one slow scenario; the fake speedup is what shortens it.

Not done: rota calls the tracker 22 times in one `round assign` (about 1 s of fake time). Cutting those calls changes rota, not the tests; it is a candidate follow-up.
