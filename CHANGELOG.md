# Changelog

## Unreleased

### Milestone branches

- **Issues of an active milestone route to its branch.** `rota round assign` cuts the worker branch from `origin/milestone/<id>`, records it as the slot's base and names it in the brief; the worker's PR, `rota git base` and `rota ship` follow. Untagged issues, issues of a milestone that is not active, projects without `origin` and milestones whose branch is not on origin keep the round base. `rota round reconcile --apply` repairs a new `base-drift`: when an issue's milestone tag changes after assign, the PR is retargeted, the slot rerouted and held until the worker merges the new base. A milestone going active under an unchanged tag is not drift. Milestone PRs are not gated by the tick yet (train sync is a sibling issue).

### Upgrade notes

- **Visible claim labels.** After upgrading, run `rota round reconcile --apply` once to backfill `claimed:<agent>@<machine>` labels for in-flight comment claims and repair stale labels. Comment claims remain authoritative. Reverting this change leaves `claimed:*` labels already written to the tracker; remove those labels by hand.
- **Named machine pins.** `machine:<name>` replaces `site:support`: main is named `main`, support uses `round.support`. On main, preview with `rota migrate labels`, then run `rota migrate labels --apply` to relabel open issues and retire `issues.labels.support` (including a custom label value). `config fill` preserves that key until migration succeeds. Candidates show pins to other machines as blocked; unpinned issues remain available to main.
- **Fewer filed follow-ups.** `review.fileDeferred` now defaults to `major`: minor deferred findings stay in the verdict record and `verdict add` lists them as `kept`. A PR whose issue is itself a deferred-findings item files none. Set `minor` to get the old behavior.

## v0.25.0 — 2026-10-24

Config shrinks from 142 keys to 124, the round status screen is fast and grouped by issue, and worker slots can share dependency directories.

## Upgrade notes

- **Removed, merged and renamed config keys.** `rota migrate config --apply` (and `rota config fill`) moves the values: `models.orchestrator` to `orchestrator.model`, `round.operatorCommand` to `orchestrator.command`, `models.worker` to `round.tiers.claude.standard` (default `sonnet`; the explicit tier key wins over `round.profile`), `round.workerCommand` / `round.codexCommand` / `round.opencodeCommand` to `round.commands.claude` / `.codex` / `.opencode`, and `round.workerKind` to the first entry of `round.harnesses`. `issues.label` moves to `issues.labels.inProgress` when that is unset. The preview lists exactly what `--apply` writes, and an old key and its new key that disagree in one file stop the migration with exit 4 before anything is written. See [Upgrading from the old keys](docs/usage/configuration.md#upgrading-from-the-old-keys).
- **One account list.** `round.accounts` now holds every worker login as `{kind, name, dir}` objects (`kind` is `claude`, `codex` or `opencode`). `round.codexAccounts` and `round.opencodeAccounts` are gone, and `rota migrate config` folds them in. A `round.accounts` in `.rota/config.local.json` replaces the global list rather than merging with it.
- **Dropped keys.** `ship.secondOpinionRunner` (the second-opinion gate always runs the subagent) and `gate.smokeShards` (use `ROTA_SMOKE_SHARDS`).
- **Fixed constants.** These keys are no longer read: `orchestrator.stateMaxAgeSeconds` (120), `handoffMaxAgeSeconds` (900), `handoffMaxBlocks` (2), `keepaliveMaxRestarts` (10), `keepaliveBreaker` (3), `keepaliveBackoffSeconds` (5), `limits.resumeMarginSeconds` (60), `fallbackSleepSeconds` (1800), `maxResumes` (3), `issues.retryWaitSeconds` (60), `issues.bulkPaceMs` (1000).
- **Invalid tracker settings refuse.** An invalid `forge.provider` or `issues.source` used to fall back to the default. `rota tracker call`, `rota issues provider`, `rota issues migrate` and the gate's bounce and provenance checks now exit 70 and name the bad value.

## New

- **Round status screen.** `rota round status --ui` opens at once on the slot registry and loads the host, tracker and quota in the background, so keys keep working while a read is slow. Issues are grouped into Active, PR review, Support queue and Upcoming, and link to their issue and PR.
- **Shared dependency directories.** `round.sharedDirs` lists directories such as `node_modules` or `.venv` that rota symlinks from the main checkout into each slot worktree, so workers skip the install.
- **Support hand-back.** A support machine returns an offered issue with `rota round support return <ID> --reason-file <path>`. Relays travel as issue comments stamped with their machine, PR events post a pointer on the issue, and the watches on both machines wake for them.
- **Workers update docs.** `/rota-docs` is no longer manual-only: round workers run its after-work step in their own PR. Scaffold and restructure still follow `autonomy.level` and go to the maintainer in a round. A lint fails when a skill tells the agent to run a manual-only skill.
- **Moved base and ready patches.** Workers merge a moved base branch into theirs before the final proof and on every fix push. A review finding can carry a ready patch, which the orchestrator applies as its own commit instead of relaying it, and a deferred finding's patch goes into the filed follow-up item.

## Fixed

- The merge gate fails an offer citation it cannot verify because the issue read failed, and refuses relay comments written by a worker.
- `rota round review-relay` loads round settings once, and a bad key exits 3.
- Credential expiry and the status screen use the injected clock, and the gate's issue trackers use the run's context, so a cancelled run stops them.
- Round settings have one owner (`round.Env`), and the round tick takes `round.Candidate` directly (refactors from the architecture review).

## Stats
74 commits, 327 files changed, +7771 −2267 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.24.0...v0.25.0

## v0.24.0 — 2026-10-22

Config keys gain scopes, rounds get a test lock and a support role, and one rule decides the forge.

## Upgrade notes

- **Config keys now carry a scope.** Every key is project, machine or taste scope. `rota config set` on a machine key writes `.rota/config.local.json`, not the tracked file; pass `--project` to pin a key in the repo. `rota config show` prints the scope. `rota config check` validates the merged layers, warns about machine keys and default-pinned taste keys in the tracked file, and now fails on unknown keys and on account directories that are not absolute or `~`-relative. `rota migrate config` previews the scope move and `--apply` writes exactly that preview: read the preview before applying, because deleting a tracked key pinned at its default lets a different global value through.
- **Empty account lists inherit the global accounts.** A `round.accounts`, `round.codexAccounts` or `round.opencodeAccounts` set to `[]` in the project or local file no longer forces the ambient account when the global config lists accounts.
- **One forge rule.** `forge.provider` wins, then the origin. An origin that names no forge makes writers (`worker gate`, `worker train`, `ship pr`, `release host|push|publish`, issue writes) refuse with exit 4 `blockedBy: forge`; readers (doctor, `issues provider`, issue listing) assume GitHub and say so. Set `forge.provider` for aliased or enterprise hosts. `release host` now exits 4 on an unrecognised origin instead of printing `none`.
- **Round config errors have one exit class.** An invalid `round.*` key exits 3 from `round candidates`, `tick`, `transfer`, `adopt`, `review-relay` and `reap` (candidates was 2, the others 70); `start`, `assign`, `watch`, `wind-down`, `status` and `reconcile` keep exit 2, `bounce` keeps 70 on a bad `round.maxBounces`. `test.fast` and `test.full` must be lists of strings; a malformed list stops the gate instead of being ignored.
- **Cross-harness review on big PRs.** With `round.harnesses` set, PRs labelled `size:Major` or `risk:high` need a `review-cross` verdict at HEAD before the gate merges. Refresh the installed skills together with the binary, or exempt an issue with `review:none`.

## New

- **Test lock.** `rota test run`, `rota proof record` and the gate take a slot of a per-repo lock (`test.slots`, default 1, machine scope); a busy lock waits and says who holds it. Proofs run niced. `rota test lock -- <cmd>` runs any command under a slot. The gate sizes its smoke shards to the machine.
- **Support machines.** `rota setup` asks the machine role first; `--set round.role=support` takes the support path, which writes only `.rota/config.local.json`, refuses a clone whose main never enabled support, and ends with the doctor. `rota round support enable|disable <name>` on main manages the tracked key. `rota round assign` and `worker dispatch` refuse a slot account at its usage limit (`blockedBy: limited`) and fall back to the installed worker contract when the checkout has none.
- **Codex workers.** `round assign` to a Codex slot verifies the session took the brief and exits 5 `blockedBy: dispatch` with the leftover steps when it did not; `worker dispatch --task` rebuilds the same brief, branch and model; the commits-not-on-main check compares against `origin/<base>`.
- **Harness quota fallback.** A worker whose harness runs out of quota falls back to the next configured harness for the next assignment.
- **Setup signals.** `rota setup` suggests `git.baseBranch`, `issues.source`, `test.fast`, `test.full`, `round.workerKind`, `work.isolation` and `round.dispatch` from the project, with the evidence shown; a saved global value beats the signal.

## Fixed

- The autopilot and the orchestrator no longer race on one slot, bundle members keep their claims, and `rota config check` reads the local layer.
- The worker wait test no longer hangs under load; the proof-record nice test passes when the suite itself runs niced.
- `rota setup` from a subdirectory of a support clone no longer writes tracked files.
- Review-kind, lease-check and round-config-exit rules each have one owner (three refactor findings from the architecture review).
- `rota test run` passes the lock mark to its tier commands, so a tier that re-enters the lock (`bash test/gate.sh`) no longer fails at one slot.

## Stats
146 commits, 323 files changed, +10855 −2645 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.23.0...v0.24.0

## v0.23.0 — 2026-10-22

A second machine can build issues for the same repo while one machine keeps every merge, file-mode milestone files list their items, and the gate now checks what GitHub will close.

### New

- **Support orchestrator (#1598).** A second machine builds issues for the same repo and tracker while one machine keeps every merge; the tracker is the only shared state. `round.support` names the support machine, `round.role: support` goes in its `config.local.json`, and both need an issue tracker. Main offers issues with `rota round support offer <ID>...`, each side assigns only its own, and support slots and branches carry the machine name as a prefix. Main's `round reconcile --apply` adopts a support PR once its issue is `needs-review`, bounce counts live on the issue so both machines read one cap, and main's bounces reach the support worker through the PR. Learnings travel the same way: a support machine posts `learning` comments, `rota knowledge harvest [--mark <commentId>]` lists and marks them on main, and `/rota-learn` folds them. Only comments by authors with write access count; `round start` refuses `round.support` on a forge that cannot say who may write.
- **Milestone files list their items (#1586).** On the file backend a milestone file carries a generated `## Items` section: open items first, then completed ones with their closure reason, each linked to its detail file. Every verb that writes an item's milestone regenerates it, as does `rota milestone check` (`itemsRegenerated`). On the tracker backend a milestone's tracking issue lists its child issues the same way (#1585).
- **The gate reads closing references (#1616).** `rota worker gate` and `worker train` ask GitHub what merging a PR closes and refuse, with exit 4 and `blockedBy: closes`, a PR that would close any `partial-slice` issue, its own or another. `--final-slice` (repeatable on `train`) exempts the last slice. A forge that cannot answer, or errors, skips the check with one `CLOSING-REFS-UNKNOWN` note per member.
- `rota item comment add|list --kind learning|harvested`; `rota knowledge harvest` is classified in the support fence (listing allowed, `--mark` refused).

### Changed

- `rota knowledge harvest` without `--mark` is read-only, so a refusal exits 1 with `blockedBy: backend|forge`; `--mark` keeps exit 4. `rota item comment add --kind harvested` is refused on a support machine (#1631).
- Invalid-value config errors show strings quoted with their printable runes intact and numbers, lists and objects as compact JSON; the Python-repr emulation is gone (#1622, #1648). An invalid `round.scope` reports the schema's choices; `config set round.scopeOverlap` refuses a non-choice at set time (#1621, #1703).
- `rota milestone check` is a write verb in file mode and leaves the support machine's read list; its contract documents `itemsRegenerated` (#1623). A milestone sync that fails after an item write is a warning, not a failed verb, and one bad milestone file no longer stops the others (#1624, #1625).
- The stale-binary warning ignores `.go` files under `testdata/` (#1699). The train prints each gate note once per member rather than once per stage (#1668).

### Fixed

- `rota doctor` names the effective Codex tier models when `round.tiers.codex` is unset (#1602). `config save-global` reports `changed` (#1603). The doc lint's stamp fixtures no longer inherit `ROTA_DOC_STAMPS` (#1604).
- A gate step that reads the forge no longer breaks every gate when the forge cannot answer (#1616).

### Internal

- The threshold architecture review ran twice and its refactors landed: one owner each for the partial-or-failed outcome rule, the slot-state sets (with a source guard), verdict follow-up filing, label and retry defaults, footprint overlap matching and the worker-done finish rules; `round.scopeOverlap` and `round.scope` are typed at load; test seams left the shipped binary (`ROTA_TEST_*` reads, `fsio.SetTempNameForTest`), and ps, clock, home and cwd reads go through seams. Behaviour-neutral by review; old-vs-new tables are in the PRs.
- Bundles (`round assign --bundle`) carried most of this round's follow-ups. Contract docs restamped; knowledge from rounds 33 and 34.

## Stats
107 commits, 218 files changed, +6899 −1116 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.22.0...v0.23.0

## v0.22.0 — 2026-10-22

Bundles of small issues for one worker, one follow-up item per PR for deferred review findings, and milestones seeded from a concept doc.

### New

- **Bundles.** `rota round assign <lead> --bundle <id>,<id>` hands one slot a lead issue plus up to `round.bundleMax` (default 3) related issues. They ship in one branch and one PR. `worker done`, `review depth` and the gate's closes check cover every issue in the bundle, and a bounce counts once on the lead. `round status` shows the bundle as `1559 (+#1563, +#1564)`. A member leaves the bundle by transfer to the human or by closing (#1592).
- **One follow-up per PR.** `rota verdict add --file-deferred` now collects a PR's deferred findings in one item, "Review follow-ups from PR #N". Later verdicts on the same PR add to it, under a per-PR lock. The new `review.fileDeferred` key (`minor` by default, or `major` or `off`) sets which findings get filed (#1599).
- **Concept docs seed milestones.** `/rota-vision` finds a concept or brief document in the repo and splits it into milestones. Each seeded milestone carries a `Source: <path>#<heading>` line, so later runs know which parts are covered (#1587).
- Deferred follow-up items wait until their source PR merges or is closed, so they never reach a worker ahead of the code they review.

### Changed

- File-mode proof rows move to a gitignored `.rota/<kind>/<ID>.proof` sidecar, so `rota proof record` no longer dirties the worktree. Run `rota init` again to add the ignore rule (#1547).
- `rota item create --milestone` and `item field set --name milestone` refuse an unknown milestone with exit 3 on the file backend too. This used to be a silent write (#1588).
- `rota worker done` refuses a dirty or unpushed branch and reports done only after the PR exists.
- The train nudge leaves out merged PRs, bounced PRs and PRs with blocking findings. `review depth` reports when the depth rose after the last review, and `verdict add` says when that leaves a verdict missing.
- `verdict add` JSON: `skipped` is gone, and `filed`, `appended` and `filedWhy` describe the follow-up item.

### Fixed

- A gate verdict's request receipt is settled, so a same-id retry replays the verdict instead of hanging pending. Retry hints no longer suggest a retry that would be refused.
- `config set` (and the TUI config screen) report an autonomy rewrite even when the set after it fails.
- Contradiction timestamps, design, spike, plan and knowledge-hit stamps all come from one clock.

### Internal

- Config is read through injected seams in round, escalation, gate, knowledge, agents and postgate. The pane classifier moved out of `worker/poll.go`. Contributor contract docs were re-verified against the code and restamped.
- A new smoke section covers bundles end to end.

## Stats
98 commits, 163 files changed, +6123 −2293 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.21.0...v0.22.0

## v0.21.0 — 2026-10-21

Three autonomy levels replace the old on/off switch, rota records its own mistakes and offers to file them upstream, and rounds stop switching worktrees under a running gate.

### Changed

- `autonomy.level` takes three values, `off`, `roundtrip` and `auto`, for solo skills and rounds alike (#1101). `roundtrip` runs one item from brainstorm to ship without process questions. `auto` continues with the next ready item and turns on the round autopilot. Manual gates, `ship.mergeApproval`, `/rota-decide` and the human-only calls (product behavior, public names, breaking changes, scope cuts) stop every level.
- `round.autopilot` is retired (#1101). `rota round tick` and `rota round watch --autopilot` exit 4 unless `autonomy.level` is `auto`. `rota config fill` deletes the key, and `rota config check` names it under REMOVED in `config.json` or `config.local.json` (#1520).
- One-time migration: `rota config fill` (also run by `rota init` and `rota update`) rewrites an `auto` in a config stamped older than 0.21.0 to `roundtrip`, because the old `auto` meant one hop. A global `auto` gets the same rewrite, guarded by a `config.version` stamp file beside the global config. Setting `autonomy.level` on any layer stamps that layer, so an `auto` set after upgrading stays. `config set`, `config save-global` and `rota setup` report or follow the rewrite too (#1511, #1512). To keep running until the queue is empty, run `rota config set autonomy.level auto` once after upgrading.
- `rota round assign` and `rota round transfer` refuse with `gate-running` while a gate or train holds the land lock (#1480).
- Round assign skips accounts with no usable login and names the login problem when none is left.
- `rota verdict add --sha` binds a verdict to the commit the review package diffed, not the local branch ref.

### New

- `/rota-bugreport` and `rota bugreport` (#1175): a verb that fails with exit 70 records the mistake in `.rota/bugreports.jsonl`, agents can add one by hand, and the skill drafts a redacted report, checks for a duplicate and files it on the rota repo only after you say yes.
- `rota knowledge rm` deletes a knowledge bullet and its tier entry.
- `rota item section set` replaces one section of an issue body.
- `rota migrate milestone-files` moves milestone files left from file mode into the archive, and doctor warns while they remain.

### Fixed

- Knowledge hits are buffered in a gitignored file and folded by `rota knowledge tier fold`, so recording a hit no longer dirties the tree during a gate.
- GitHub issue edits go over REST and are verified by reading them back.
- A receipt rerun points at the pending call it duplicates. The smoke runner refuses foreign sections and runs without `.git`.

Refactors with no intended behaviour change: worktree containment, the Claude settings layout, machine secrets and the projects config dir each have one owner, and the CLI's git and pane-host reads go through injected dependencies.

## Stats
281 commits, 347 files changed, +10129 −1479 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.20.0...v0.21.0

## v0.20.0 — 2026-10-21

It's a Plan becomes an issue source, the global config becomes a real runtime layer with its own XDG dirs, and pane detection stops misreading quoted text as a limit or logout.

### New

- It's a Plan as an issue source (#1100): connection and doctor check, read and write verbs, claims, assign and close on merge, and `rota migrate` from a file backlog.
- The global config is a runtime layer under the project config (#1179), no longer only a seed for new projects. `rota config show/set/edit --global` and an inherit toggle in the config screen (#1180). `rota setup` offers to save its answers as global defaults (#1181). `ROTA_CONFIG_DIR` overrides the global config dir (#1186).
- Decision items: research findings, grilling refusals and a decision log, taught to vision, brainstorm and spike (#967).
- `/rota-docs` takes over docs maintenance from `/rota-ship --docs`, which is now a deprecated alias (#1098). `rota round accept` completes an architecture review that opens no PR (#1091).
- Bare `rota` run in HOME is a launcher, and `init`/`setup` refuse to run there (#1183). Stub skill installs are the default (#1099).

### Changed

- XDG layout: the project registry moves to the state dir (#1184) and the secrets fallback file to the data dir, forced to 0600 (#1185, #1221). A symlinked legacy file is copied, and a non-file one is left alone (#1220, #1253, #1254). Doctor reports both dirs.
- Round-only config keys move from `work.*` to `round.*`; `rota migrate config --apply` moves them, and `round start` uses exactly the configured slot count (#1097).
- Worker slots with no `round.accounts` entry inherit the orchestrator's `CLAUDE_CONFIG_DIR`, and doctor warns on a mismatch (#1171). The account check reads macOS Keychain credentials when the credentials file is missing or expired (#1176).
- A corrupt global config refuses every write with exit 70 and skips the save offer (#1180, #1223).
- The overlap check compares an open PR's changed files and names directory pointers as the cause (#1094, #1170).
- Refactors with no intended behaviour change: one body-file reader for the CLI (#1106), typed gate refusals (#1108), and single owners for merge-approval policy and escalation (#1105, #1263), review kinds and depth (#1230), worker env and config reads (#1225, #1228, #1229), the PASS-at-HEAD proof rule (#1149), the keepalive marker (#1150) and the stale-binary state (#1109).

### Fixed

- Pane classification: limit and logged-out phrases count only inside the harness dialog region, so a phrase quoted in a reply, diff or tool listing no longer marks a slot limited (#1212, #1246). Only "Login expired" and the bare "Not logged in" status line count as logged out (#1209, #1255). A footer with running shells or monitors reads as busy (#1156), and a relay clears an answered ROTA-BLOCKED (#1096).
- Gate and round: the bounce cap holds under concurrent writes (#1226). A second gate on one PR is refused with the real cause (#1095), and a gate marker whose pid was reused is replaced (#1126). The gate waits for the forge PR head before failing on a SHA mismatch (#1168). A retried assign resumes under its existing claim id (#1172), assign adopts the open PR of a done slot (#1090), and transfer and reconcile free a blocked slot whose PR merged (#1093).
- Process ancestry falls back to `ps` where `/proc` is missing (#1167). Orphaned `.extract-*` dirs older than an hour are reaped (#1120). `Depends on` is read from the issue body only (#1139). `--file-deferred` no longer copies the finding title into AC-1 (#1092).
- Smoke: a shard that dies without a summary is reported with its exit code and last output, and section 148 no longer dies silently (#1242). Worker wait tests no longer depend on wall-clock timing (#1266).
- Knowledge: lessons from round 29.

## Stats
132 commits, 476 files changed, +14746 −3442 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.19.0...v0.20.0

## v0.19.0 — 2026-10-20

OpenCode joins Claude and Codex as a worker kind, the gate runs an issue's Try it steps, and the merge train becomes the default when several reviewed PRs wait.

### New

- OpenCode as a third worker kind (#584). Pane classification covers the boot footer, busy, limits and bad-model states; briefs are pasted raw; the slot's account env passes through; `rota doctor` checks the install. Default Codex and OpenCode tier models ship for `round.tiers.*` (#1009); the OpenCode defaults need the built-in Zen provider, otherwise override all three tiers.
- Try it steps run in the merge gate after verify (#913), and again for each train member before landing (#1012). They run only for collaborators' edits, with an empty HOME and a scrubbed environment; untrusted steps are withheld from the worker brief. New `behaviour` verdict kind and `rota verdict add --transcript`.
- Typed worker outcomes (#1007). `rota worker done --outcome done|partial|failed --summary-file <f>` records three summary lines; the gate refuses a `partial` or `failed` slot until a relay or transfer. Stale completions are fenced with an attempt id (#982), and workers get a durable inbox that `worker done` must drain (#1013).
- Round pacing and cost controls: `round.batch` caps open assignments (#1016); `round.profile` fills the tiers from a named model profile (#973); `round.freshAt` transfers to a fresh slot instead of relaying again (#981); `plan.criticHarness` runs the level-4 plan critic on another harness (#1001).
- Merge train as the default (#1048). `round wait` and `round watch` print a train nudge naming the PRs when two or more reviewed PRs wait, after every autopilot tick (#1064), and a nudge to assign a done slot before gating (#1075).
- Review gets sharper. A review verdict finding carries `blocking`, only blocking findings count as a bounce, and `rota verdict add --file-deferred` files the rest as items (#999, #1020). Review depth lifts on a one-way door, scope drift or a risky path (#909). `light` depth now runs the Spec reviewer (#916).
- New skills and modes: `/rota-answer` puts pending decisions to the maintainer one by one (#995); `/rota-capture --triage` handles issues filed by others (#994); `/rota-init` suggests config values from the project (#1034); `/rota-qa uat` and `ship.uat` add human acceptance (#991); `/rota-refactor --map` warns when the map is stale (#1008); `/rota-pause --report` writes a session summary (#970).
- Session guards: `work.contextGuard` warns or pauses a `/rota-work` session before its context fills (#988); `research.packageCheck` screens new dependencies against the registry (#990); `brainstorm.edgeProbe` adds an edge and prohibition probe (#985); `rota plan check` warns on oversized plans (#987) and checks that tasks honor the item's decision comments (#998).
- `rota receipt show` answers whether a lost write applied (#984). `rota milestone status --to shipped` exits 4 while the milestone has open items or unticked criteria, unless `--force` (#972).

### Changed

- Defaults that move: `work.herdrGroupSlots` is on (#920), the round tier keys for Codex and OpenCode have values (#1009), a bad `round.*` key exits 2 on status, reconcile, assign, start, architecture, wind-down and watch (#980), and `round escalate send` requires options and a recommendation (#1000).
- Gate and review rules tightened: a FAIL verdict blocks, a merged PR drops out of review whatever the verdict (#1042), needs-info issues are not ready in `round candidates` (#1043), and a PR carrying a superseded attempt's commits is gated without misfiring during a transfer (#997).
- Autopilot tick policy moved from `cli` into `roundtick`, and the tier walk, issue-branch name, recorded-review lookup, registry slot decode and round config load each have one owner (#1059, #1074, #1049, #1047, #1003, #980). No behaviour change intended.
- Knowledge: lessons from rounds 22 to 28; entries store a commit sha beside the date (#971).

### Fixed

- Watch and relay: `round watch` no longer reports stale or placeholder done lines (#1025); a ROTA-DONE that predates the last relay is ignored (#874); an unseen relay gets a fallback window when stall detection is off (#1035); herdr dispatch waits out `agent_pane_busy` before failing (#1033); the gate counts a done by event seq, not a stamp (#1056).
- Limit detection skips echoed orchestrator blocks (#887), reads phrases per slot kind (#882), and the limits feed no longer resubscribes after a stop or times out before the first pass (#996).
- `install.sh` installs its cleanup traps before `mktemp`, so no temp dir leaks (#1024); `KNOWLEDGE.md` edits are locked (#896); `round bounce --slot` refuses names outside the pool (#886).

### Documentation

- Round 28 train default, nudges and the idle table; the plain-orchestrator comparison re-judged and published in the user docs (#1021); OpenCode workers; triage, deferred findings and Try it runs; the `KNOWLEDGE.md` lock; the unknown-slot bounce exit 4; limit detection and the `release milestone-check` line.

## Stats

339 commits, 462 files changed, +19496 −2321 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.18.0...v0.19.0

## v0.18.0 — 2026-10-18

Milestone breakdowns get a shape and a check, workers report through typed events with an unverifiable state, and the round verbs carry request ids and receipts.

### New

- Milestone breakdown shape (#853). A milestone lists its `## Capabilities` and checkbox `## Acceptance` criteria; a child issue declares `## Change` (`ADDED|MODIFIED|REMOVED|RENAMED <capability>`, `Serves: M08 AC-2`, Reason and Migration on REMOVED) and copies the capability names into `## Touches`, so siblings on one capability clash in `round candidates`. `rota milestone check <MNN> [--strict]` names every gap; `rota migrate milestones` converts old bodies. `skills/references/breakdown.md` owns the shape for `/rota-plan`, `/rota-vision` and `/rota-capture`.
- Typed worker events (#819) and an unverifiable slot state (#820). Workers push done, blocked, question and heartbeat events to a durable record that the orchestrator reads before the pane; a newer event outranks a stale pane. Contact loss is `unverifiable`, distinct from `dead`, with a bounded herdr wait, a forced reclaim and a loud outage path.
- Request ids and receipts on the state-writing round verbs (#822). A retried `assign` or `transfer` after a flaky forge call replays its receipt instead of acting twice; inputs are fingerprinted by content, a corrupt receipts file is refused, and the store is capped.
- `rota skills get` and `list`, with an opt-in stub install (#821), so installed skill copies stop drifting from the binary after `rota update`.
- `rota item create --batch-file` files a JSON array in order and reports the ids already filed when a record fails part-way; placeholder titles are refused on every create path (#838).
- herdr slots open as linked worktree workspaces behind `work.herdrGroupSlots`, and a Claude Code dialog on the prompt line is read as a dialog, not a draft.

### Changed

- Kind switches moved behind the `Harness` interface (#584) and the autopilot tick mappings into `roundtick`; tracker close reasons, worker event kinds, the registry timestamp and the Linear read-versus-write split each have one owner.
- Knowledge: lessons from rounds 19 to 21 and from #853 (gate hygiene, stale verdicts, sidecar briefs); the orchestration and Smoke testing topics are split into facets.

### Documentation

- Worker events, `skills get`, the stub install, unverifiable slots, batch item create, dialog-aware dispatch, the breakdown shape and the two new milestone verbs; the `--force` caveat and the hedged orca read.

## Stats

87 commits, 209 files changed, +7807 −1056 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.17.0...v0.18.0

## v0.17.0 — 2026-10-15

Jira and Linear as issue sources, machine-wide tracker connections, and the issue source split from the forge

### New

- **`rota connect`**: tracker connections are set up once per machine, not per project. Secrets go to the OS keychain and never pass through argv, `.rota/`, logs or URLs. `rota connect add|list|test|remove`.
- **Jira as an issue source** (`issues.source: jira`), over the REST API, for both Cloud and Data Center. A connection has to say which one it is (`--deployment cloud|datacenter`); `*.atlassian.net` sites are assumed to be Cloud. Claims are `Claimed by <id>` comments, ordered by the time the server recorded them. Proof and handoff state live in issue properties. The gate closes the issue after the merge. If that close fails, the claim stays and the gate warns.
- **Linear as an issue source** (`issues.source: linear`), over the GraphQL API. Rota's hidden state is kept in issue attachments, which Linear shows as "rota state" links.
- **Tracker keys as issue IDs**: `PROJ-123` works anywhere a number did: `Depends on`, escalations, approvals, `rota issues` verbs and round assignment.
- **`rota ship strategy`** reports how a branch lands: `direct`, `pr` or unset. A local forge always lands direct.

### Changed

- **`issues.source` and `forge.provider` replace `backlog.backend` and `issues.provider`.** The issue source (`file|github|gitlab|jira|linear|forge`) is now separate from where PRs go (`auto|github|gitlab|local`), so issues can live in Jira while PRs go to GitHub. `issues.source: forge` covers umbrellas whose sub-repos are split across GitHub and GitLab. A project that hasn't migrated keeps working: the legacy keys still decide. `rota migrate source --apply` rewrites them.
- **Gate:** it can now merge the second PR of a split issue, following the slot's registered branch. An issue labelled `partial-slice` stays open when its first slice merges. Gate steps that read the forge are skipped when there is no `origin` remote.
- **Tracker REST layer:** a POST that gets a 5xx back is never sent again, because the mutation may already have landed. Read-only GraphQL goes through a path that retries but refuses to run mutations.
- **`rota doctor`** fails when a Jira source has no project or connection configured.

## Stats

41 commits, 272 files changed, +14952 −1253 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.16.1...v0.17.0

## v0.16.1 — 2026-10-14

One owner per rule across round, gate and doctor; tests own their clocks

### Changed

- **Internal refactors from the round-17 architecture review (#772–#783).** Each scattered rule now has a single owner: item-ID comparisons go through `worker.NormID`; git changed-paths and remote-base reads go through `git.ChangedPaths`; candidate scope and roster slot selection live in `round`; review-loop and limit-reading policy moved out of `cli`; worktree-ownership rules moved into `ship`. The unused `land.Policy` table is gone.
- **Gate targets:** a gate target's issue is resolved once and returned on the result. It stays correct when the gate rewrites an external PR's branch or adopts an existing PR.
- **Idle rule:** a busy slot or an adopted (external) slot is no longer idle. Implicit assign, best-of and the limit watcher all skip them, and quota counts an adopted slot as held.
- **`rota doctor` accounts check:** it uses the worker's credential rule, so a credentials file with no access token now fails the check.
- **Clocks:** ledger, audit and status clocks go through the caller's `Env`, so tests can pin timestamps end to end.
- **Forge CLI:** a missing forge CLI is reported as a tracker error kind, not just message text.
- **Status TUI:** the round status TUI builds its first frame from the report.

## Stats

22 commits, 113 files changed, +1722 −994 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.16.0...v0.16.1

## v0.16.0 — 2026-10-11

A git guardrail hook for workers, a Codex plugin built at release, and a sturdier merge gate.

### New

- **`rota hook guard`**: a PreToolUse hook that stops a round worker's agent from running git commands outside its own worktree and branch. Three bypasses are closed.
- **Codex plugin**: release now builds a symlink-free Codex plugin and marketplace and publishes it to the `codex` branch, but only after the binary release succeeds.
- **Skills**:
  - `/rota-decide --supersede` and `--retire`; readers skip retired decisions.
  - `/rota-learn --retro <session-id|path>` audits how a past session found information and was steered.
  - `/rota-capture` checks for duplicates, shipped work and decisions before filing.
  - `/rota-debug` and `/rota-pause` redact secrets in evidence and handoffs.
  - Manual-only skills are now enforced on both Claude Code and Codex.
- `SCOPE.md` and issue templates for bugs and ideas.

### Changed

- **Claude Code plugin**: hooks moved from `plugin/hooks/` to `hooks/` at the plugin root, and `plugin.json` drops its `hooks` field. This clears the plugin directory's checklist warning; the SessionStart hook still fires.
- **Corrupt worker registry fails closed.** If `.rota/workers.json` doesn't parse, verbs that write it refuse instead of treating it as an empty pool.
- **`round transfer` runs the same preflight as `round assign`.** It now honours `model:` labels and refuses on a missing tier model, exhausted quota or an out-of-scope item.
- **PR URLs** are parsed in one place. A self-hosted GitLab merge request URL without `/-/` now resolves to GitLab. Suffixes like `/files` and `?query` are accepted.

### Fixed

- `round reconcile --apply` parks a slot whose PR merged even when its tab is dead, instead of leaving it as drift that needed `round reclaim`.
- `round watch` and `worker poll` no longer report a freshly dispatched slot as dead.
- The merge train no longer reports a failed e2e tier as skipped.
- Gate runs no longer grow `GOCACHE` without bound; `rota doctor` warns when its disk is low (`go-cache-disk`).
- The gate pins the invoking worktree's HEAD across pass, fail and interrupt.

### Internal

Two architecture reviews' worth of refactors, with no behaviour change: one pre-merge sequence and one verify-tier runner shared by the gate and the train, `round.Env` built from one constructor with explicit account picking, one upward root walk, clock and exec calls behind seams, host kinds and capabilities owned by `internal/host`, shell quoting through `shlex`.

### Docs

README section on the Claude Code plugin; prep guides for the Anthropic plugin directory and awesome-claude-code listings.

## Stats
78 commits (129 with merges), 298 files changed, +7736 −1969 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.15.0...v0.16.0

## v0.15.0 — 2026-10-09

rota now installs as a Claude Code plugin, rounds keep a ledger you can summarise, and review depth and state writes are stricter.

### New

- **Claude Code plugin.** `/plugin marketplace add l4ci/rota`, then `/plugin install rota@rota`. The plugin serves this release's skills and adds `/rota:rota-install`, which installs the matching binary (Homebrew, else the install script) and runs `rota doctor`. A session-start line points at it when the binary is missing or the wrong version. `rota doctor` and `rota skills status` count the plugin cache as a skill root. (#602)
- **Round ledger.** Every assign, done, bounce, gate and merge goes to `.rota/ledger.jsonl`; `rota round summary` (also printed by `wind-down`) shows wall time, bounces, gate outcome and quota share per issue, slot and account. (#574, #613)
- **Review depth policy.** `ship.review` takes `full`, `light`, `none` or a policy by diff size and labels; `rota review depth` shows the result. `rota worker gate` refuses a branch without the verdicts its depth needs, recorded at the current head. (#581, #642, #662)
- **More round tooling:**
  - worker-side PR review loop: `rota round review-relay` (#577, #611)
  - `rota worker adopt` for worktrees another tool started (#580, #617)
  - scope overlap on declared `## Touches`, not only files (#575)
  - conflict-minimising merge order in trains (#583)
  - quota-aware slot cap and Codex limit detection (#578)
  - per-slot port ranges and a doctor `ports` check (#576)
  - the orchestrator's handoff note surfaces on `round start` (#621)
- Partial topic matching and topic listing for `rota knowledge` and `rota decisions`. (#582)

### Fixes

- **State writes fail loud.** Registry writes are re-read and verified. A corrupt `workers.json` is refused instead of reset. `rota reap` refuses when the registry cannot be read, and `reconcile --apply` skips repairs whose host or forge read failed. `assign`, `transfer` and `report` exit non-zero when the ledger row is not written. (#579)
- `install.sh` no longer stops when `minisign` is missing: it verifies the sha256 and warns; `ROTA_STRICT=1` keeps the old fail-closed behaviour, with tighter parsing. (#599, #612)
- `rota worker gate <PR>` gates the PR's own branch after the slot has moved on. (#648)
- Process liveness handles zombies, and the herdr pane id is resolved in one place. (#593, #594)
- The no-verify refusal (empty `test.full` and `test.e2e`) uses one rule in the gate, the train and doctor. (#590)

### Docs

- The README and install page lead with the single-agent path and Homebrew. (#600)
- rota skills are listed for Codex, skills.sh, the Hermes hub and `llms.txt`. (#605)
- Round 11 and 12 behaviour is documented; contract docs re-stamped. (#671)

### Internal

- Env and Deps seams for the gate, train, accounts and CLI env reads (#591, #652, #653, #654). One owner each for the park branch name and the first-line helpers (#651, #596). Terminal escape stripping was rewritten (#592).
- The merge gate now runs `gofmt`, and a test that only passed with a developer's Claude config was fixed. (#672)

## Stats
196 commits, 465 files changed, +16776 −1839 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.14.0...v0.15.0

## v0.14.0 — 2026-10-08

Interactive terminal screens behind `--ui`, a faster `round status`, and a merge gate that checks the merged code before it merges.

### New

- **Terminal screens (`--ui`).** A new global flag opens a command's screen instead of printing its result. Plain and `--json` output are unchanged, and `--ui` is refused off a terminal or with `--json`.
  - `rota config` (also `config edit`): keys grouped by section with a description, type and choices for each; `Enter` edits, `l` writes to `config.local.json`, `r` resets.
  - `rota projects`: status of every registered project; `c` cleans up, `d` removes, `n` runs `init` in a new directory.
  - `rota round status`: live slots, review queue and blocked candidates, refreshed every 5 seconds.
  - Read-only views for `doctor`, `worker account list`, `backlog list`, `knowledge stats` and `decisions stats`.
- **The palette is a hub.** Entries with a screen open it in place, and `q` returns to the list. Split and tab view are one View toggle, shown only in a herdr round.
- **`rota projects remove <dir>`** drops one registry entry and leaves the directory alone.
- **`rota decisions stats`** lists the decision topics, like `knowledge stats`.
- **Config key metadata.** `config show --json` gives every key its type, group, description and choices, and `docs/reference/config-options.md` is now generated from the schema.
- **herdr sidebar labels** show each worker's slot, task and state.

### Changed

- **The merge gate checks the merged code first.** It asks the forge whether a PR is mergeable, then runs your checks on a scratch merge, and merges only if both pass.
- **`round status` is about three times faster.** Identical forge reads are made once per command (45 calls down to about 16 on this repo). `round watch` and the autopilot start every poll from fresh data.
- **Rounds hold up better on herdr.** `round wait` survives a dropped event stream, reads a finished turn from herdr's completion counter, and explains states it does not know.
- `rota doctor` warns early when `test.full` is empty, and `release.versionFile` is now a config key.
- The `config edit` prompt loop is replaced by the config screen. On a dumb terminal, use `rota config set`.

### Fixed

- Assign and transfer no longer report false overlaps: closed issues are skipped, and an issue's comments are no longer read into its `## Files` list.
- `/dev/null` is no longer taken for a terminal, so `--ui`, the palette and `setup` refuse it.
- Removed the unread `issues.filterMineOnly` and `issues.providers.*` keys. `config check` lists them as removed and `config fill` deletes them.

### Docs

The README now leads with what rota is and shows a round in action, and the onboarding, usage and reference pages were updated for these changes. The verb contract moved to `docs/contributing/contract/`.

## Stats
65 commits, 287 files changed, +12080 −2800 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.13.0...v0.14.0

## v0.13.0 — 2026-10-07

Stricter merge gate, best-of-2 rounds, generated subagent definitions, and test tiers you can configure.

## Breaking

- `rota worker gate` and `rota worker train` refuse to merge when `test.full` and `test.e2e` are both empty. Pass `--no-verify` to merge without verification. (#474)
- The gate refuses a round PR whose body has no closing keyword for the slot's issue. Add `Closes #N`, or label a partial slice `partial-slice`. (#477)
- The gate and the train refuse a PR with a recorded FAIL verdict. (#449)

## New

- **Best-of-2 rounds.** An issue labelled `best-of:2` goes to two workers at once, by default one Claude and one Codex. The orchestrator picks the better PR with `rota round pick`, which closes the other. The gate refuses either attempt until the pick is made. (#489)
- **Generated subagent definitions.** `rota agents write` writes `.claude/agents/rota-{explorer,implementer,reasoner}.md` from new `roles.*` config, plus `.codex/agents/*.toml` when Codex is configured. `rota init` runs it, and `rota doctor` reports missing or out-of-date files. (#487)
- **Round setup.** `round.workerKind` sets the project's default worker harness. `rota round assign` reserves smoke section numbers. `work.itemTimeoutMinutes` caps how long one item may run. New slot worktrees run `work.envSetup`. (#472, #475, #439, #450)
- **Gate and train.**
  - `rota worker gate <PR>` gates any open PR, not only round PRs.
  - A stale branch merges when `git merge` is clean, even if both sides changed a file.
  - Gate and train share one land lock per repo.
  - With `test.fullWhere ci` they wait for CI checks.
  - `test.e2e` runs once on a train's merged tree.
  - The train caches passing verdicts.
  - An exclusion ledger lists tests known to be red. (#479, #480, #445, #411, #408, #451, #452)
- **Test tiers and proof.**
  - Test tiers are declared in config.
  - Every tier runs in an isolated environment.
  - `rota proof record` runs a check and records its result.
  - Acceptance criteria get AC ids that only proof can mark as met.
  - `rota worker done` refuses a finished slot that has no `test.fast` proof.
  - `work.tdd` can be turned off. (#379, #441, #420, #442, #440, #407)
- **Planning.** `rota plan check` checks that every criterion maps to a task and every task has a Verify step. Level-4 items get a critic step. A planning-dial rubric says when an item needs more planning, and plans cite the knowledge they rely on. (#458, #457, #453, #455)
- **Doctor.** Warns when the installed `rota` is older than its source checkout. (#473)

## Fixed

- `rota worker gate` names the real reason local main cannot fast-forward, and says when it skipped checking the merged tree. (#483)
- A no-break space after Claude Code's prompt marker no longer breaks parsing of the prompt line, and `rota` refuses to send into a pane that holds a human's draft. (#438, #429)
- `{files}` expansion includes untracked files and diffs against a remote base that is ahead. (#418, #422)
- `rota worker done` runs from a slot worktree. (#456)
- CI: git housekeeping no longer races fixture copies in the scenario tests, and the nightly smoke job installs minisign. (#486)

## Internal

- One mapping from domain errors to exit codes, and one owner for the Claude config folder and skill folders. (#482, #478)
- Assign and transfer share one worker-delivery module, and the item verbs moved into `backlog`. (#425, #430)
- Contract docs carry a verified sha, and doclint guards them against drift. (#447)

## Stats

192 commits, 295 files changed, +14,739 −1,349 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.12.0...v0.13.0

## v0.12.0 — 2026-10-06

Codex workers on your own account, per-issue harness and model labels, leaner skills, and a fix for slot worktrees vanishing mid-round.

### New

- **Pick a worker's harness and model per issue.** Label an issue `harness:claude` or `harness:codex`, and optionally `model:<id>` (file backend: `Harness:` / `Model:` fields). `rota round assign` picks them up; `--kind` and the new `--model` override them. `candidates` and `status` show the choice. `/rota-capture` adds the labels when you name a harness or model.
- **Codex workers use your default Codex account.** No per-slot login. Optional `work.codexAccounts` spreads slots across several Codex homes, like `work.accounts` does for Claude.
- **No Codex version pin.** Preflight checks that `codex --help` lists the flags the launch line uses. `--accept-codex-version` is now a no-op.
- **Config:** `rota config edit` (an interactive editor, also reachable from the bare `rota` palette), a global config that seeds new projects, and `rota projects cleanup` to prune stale project entries.
- **Rounds:**
  - Split layout names the tab `rota` and the pane `orchestrator`, keeps the CLI on top, and is remembered per round.
  - `rota round bounce` caps review bounces.
  - `transfer --tier`.
  - PR bodies gain `## Rulings`, proof evidence and a door/blast-radius line.
  - Briefs carry the item's Out of scope section.
- **Skills:**
  - `/rota-review` runs separate Spec and Standards reviewers.
  - `/rota-learn --retro` turns mistakes into guardrails.
  - `/rota-debug` builds a feedback loop before guessing, and ranks hypotheses.
  - `/rota-work` resumes from `Task:` trailers.
  - `item create --depends-on` orders items.
  - Behavioural evals cover skill triggers and the core skills.

### Changed

- **Skills are shorter and easier to follow.**
  - Each SKILL.md keeps the happy path inline and moves rare cases into per-skill files.
  - Each workflow opens with a copyable step checklist.
  - Duplicated rules across `skills/references/` now have one owner file (-10% words).
- `rota config show` and the editor show the value rota actually uses, including defaults behind a local `null`.
- The `blockedBy` for a Codex launch problem is now `codex flags` (was `codex version`).

### Fixed

- `rota ship pr` deleted the worktree it ran in, so a round slot could vanish mid-round. Ship verbs now never remove slots, the current directory, or a worktree with changes.
- Model ids from labels, fields and `--model` are validated before they reach the shell launch line.
- **Gate:**
  - The lock stays exclusive during stale-lock takeover.
  - The smoke leak guard no longer overwrites concurrent edits.
  - The gate and runner scrub HOME, XDG, SSH and host variables.
- **Rounds:**
  - `worker poll` lets the last sentinel decide a slot's state.
  - `reconcile --apply` parks slots whose PR merged, so `watch` no longer loops on them.
- **Merges:** merge-abort failures are reported with recovery steps instead of claiming a clean abort.
- **Items:**
  - Debug items get one canonical identity for the Iron Law count.
  - Item plans load without a milestone.
  - Removing the last backlog bullet works without a trailing newline.
  - `item rm` recognizes rota-written flat archives.
  - Archive, reopen and remove keep items across failed or concurrent transfers.
- **Other:**
  - GitLab changed-file lists paginate correctly.
  - Skill evals fail on failed model calls.

### Internal

Large refactors with no behaviour change:
- one merge module for branches and PRs;
- typed worker-registry rows;
- a single host-factory seam and clock on Deps;
- config typing centralised;
- one fake for round tests.

## Stats
107 commits, 440 files changed, +16005 −5435 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.11.0...v0.12.0

## v0.11.0 — 2026-10-05

Split and tab layouts for herdr rounds, an interactive palette on bare `rota`, and leaner CI.

### New

- **`rota layout split|tabs`.** Arranges a round's herdr panes: the orchestrator full height on the left, workers in columns two high to its right, or one tab per worker. It only moves live panes, so nothing restarts. Bare `rota layout` shows each project's current arrangement.
- **Palette.** Bare `rota` in a terminal opens a menu with the banner, version, project and round state. Orchestrate is preselected, so `rota` then Enter works as before. Pipes, `--json` and arguments skip it.
- **`rota repo add|rm`** edits the umbrella registry without re-running `rota init umbrella`. (#182)
- **Skills for every account.** `rota skills install|update|status` cover each `work.accounts` config dir as well as the current one; `--current-account` limits a run to the current dir. `rota doctor` flags an account whose skills lag the binary. (#179)
- `/rota-work` runs `/rota-qa run` after a cycle when `qa.afterWork` is on.

### Removed

- `debug.competingHypotheses`: no code ever read it.

### Fixed

- A test flake where git's background housekeeping wrote into a test repo during cleanup. (#184)
- The `/rota-ship --undo` preview no longer calls plan files gitignored.
- Two mermaid diagrams in the docs failed to render.

### Internal

- CI is one cached workflow that runs on main and on ready PRs, cancels superseded runs and times out after 15 minutes. (#183)
- The bracketed item-ID regexes come from the central ID grammar. (#176)
- An audit corrected the user docs against the code and skills, covering both backlog backends.

### Stats
18 commits, 96 files changed, +3925 −457 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.10.1...v0.11.0

## v0.10.1 — 2026-10-05

The orchestrator now starts under a configured account; skills moved under skills/.

### Fixed

- `rota orchestrate` (and bare `rota`) started Claude under the default `~/.claude` and ignored `work.accounts`. It now starts under your `CLAUDE_CONFIG_DIR` if set, else the `work.accounts` entry with the most headroom, else the first one listed. `rota keepalive run` takes `--config-dir`, and `rota orchestrate --dry-run` names the account. (#178)

### Docs

- The first-round guide covers tmux next to herdr, and its permission example allows `glab` as well as `gh`.

### Internal

- Skill folders and `references/` moved under `skills/`. The installed skill layout is unchanged. (#177)
- `.rota/gate-audit.jsonl` is gitignored in this repo.

### Stats
6 commits, 81 files changed, +255 −97 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.10.0...v0.10.1

## v0.10.0 — 2026-10-05

Continuous rounds with a sharded merge gate, an easier start, and a large internal cleanup.

### Breaking

- `autonomy.level: loop` is gone. Use `auto`.
- `rota issues list` and `/rota-capture --from-github`/`--from-gitlab` are removed. With `backlog.backend: issues`, the tracker already is the backlog.
- `rota-learn` no longer verifies by default. Pass `--strict` to opt in.

### New

- **Getting started.** Bare `rota` runs `rota setup` (an interactive config walkthrough) in a directory without `.rota/`. Otherwise it launches the orchestrator, in a herdr session when you are outside a multiplexer. `rota projects` lists every rota project on the machine.
- **Continuous rounds.** A slot frees as soon as its PR opens, so workers get the next issue without waiting for the merge. New verbs: `rota round watch` (it wakes the orchestrator on any slot, PR or escalation change), `rota round tick` and `--autopilot` for mechanical assign and gate, `rota worker train` (verify several PRs in one gate), and an `open` round scope.
- **Automatic architecture review.** After `round.architectureEvery` non-refactor issues (default 20), or when the queue runs dry with a slot idle, the round mints a review item and assigns it. Its findings become issues. A review finishes with `ROTA-DONE <slot> issues:#a,#b` instead of a PR. `/rota-refactor` is now this findings-first review.
- **Orchestrator harnesses.** Claude Code, Codex, Hermes and opencode can run the orchestrator.
- **Faster gate.** `test/gate.sh` runs validate, doclint, vet, `go test -race` and four smoke shards at once. It takes about 150 s instead of about 590 s, and only one gate runs per machine. Workers run targeted checks only. The full gate runs once, at merge.
- **Safer merges.** The gate merges the exact SHA it verified, through the tracker adapter (gh and glab). Releases require a `.minisig` for every asset, and `install.sh` verifies minisign signatures.
- **Doctor** warns on low disk. The gate fails when tests leak temp files.

### Fixed

- Dispatch submits a brief left unsent on the prompt line, and only when the agent is idle.
- `round candidates` and `round assign` skip issues that already have an open PR.
- The tmux host detects a booted Claude Code 2.1.289 pane.
- Slots stay usable after `rota migrate issues` runs mid-round.
- Flaky tests are fixed: the fsio concurrent-writers lock budget, the smoke 108 busy pane and the smoke 110 prompt marker.

### Internal

Three architecture reviews ran in this cycle, and every finding is merged. A domain module now owns each of these:
- the item ID grammar
- git and subprocess runners (`internal/git`, `internal/proc`)
- state paths (`rotastate`)
- backlog backend selection
- tracker construction
- the typed worker registry
- the round host resolver
- the ship flow (`internal/ship`)
- migration (`internal/migrate`)

Other changes:
- Verb files and frozen suites are named by domain instead of by port phase.
- Every golden can be regenerated with `-update-golden`.
- The CLI keeps its dependencies on `Ctx` instead of in package globals.
- Skills are leaner: no banners or task-list ceremony, issue-first IDs, a single review pass and one merge owner.

### Stats
155 commits, 634 files changed, +30286 −18950 lines

**Full changelog:** https://github.com/l4ci/rota/compare/v0.9.0...v0.10.0

## v0.9.0 — 2026-10-04

rota was hv-skills. The CLI moved to a new repo with a fresh history, and what would have been hv-skills 5.0 is rota 0.9.0. Older history stays in hv-skills.

rota moves the mechanics into a Go CLI and adds autonomous rounds: an orchestrator drives parallel workers, each in its own worktree, from GitHub issues.

### Upgrade
Everything is renamed: binary `rota`, skills `/rota-*`, state folder `.rota/`, env vars `ROTA_*`. Install with `curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh` or `brew install l4ci/tap/rota`, then run `rota skills install` to put the skills in place for Claude Code and Codex. `rota init` sets up a project. Projects that still have `.hv/` move over with `rota migrate`. There is no plugin and no `npx` install. Binaries are checked against `checksums.txt` (sha256). That is an integrity check, not proof of who built them; signatures are planned.

### New
- **Autonomous rounds.** `/rota-orchestrate` and `rota round` (start, candidates, assign, wait, status, reconcile, escalate, wind-down) run Claude or Codex workers under herdr or tmux, or as subagents in solo mode, with per-worker model tiers and merge policy.
- **Rules enforced in code.** Manual-gate registry, typed review and QA verdicts, the debug Iron Law, and a verified merge gate (`rota worker gate`).
- **Keepalive.** Orchestrator handoff on a full context, restart in the same pane, and usage-limit handling (sleep to reset or switch account). With `orchestrator.switchOnUsage` on (off by default), the orchestrator moves to another account before it hits the limit.
- **Codex.** Codex workers with a per-slot `CODEX_HOME` (each slot logs in separately), a version check in `rota doctor`, and no tier map required. Skills follow the Agent Skills spec so Codex discovers them.
- **Install.** `install.sh` downloads the release binary, checks it against `checksums.txt` and refuses a mismatch. A Homebrew formula is pushed to `l4ci/homebrew-tap` on each release. The skills ship inside the binary.
- **Release pipeline.** goreleaser builds `rota` for linux and macOS (amd64, arm64) from a version tag into one draft release; `rota release publish` finishes it, and the version bump reaches the branch last. The release workflow refuses a tag that does not match `VERSION`.
- **Helpers are verbs.** Every helper is a `rota` verb with `--json` output and stable exit codes.
- **Skills trimmed.** Capture hands off to work, `/rota-work` with no argument suggests the next item, and init, config, update and migrate are `rota` verbs. `/rota-ship`, `/rota-capture`, `/rota-release` and `/rota-pause` keep only the judgment calls.
- **`rota update` and `rota doctor`.** `update` reports how the binary was installed and prints the matching upgrade command; `doctor` and `rota reap` cover preflight and cleanup for rounds.
