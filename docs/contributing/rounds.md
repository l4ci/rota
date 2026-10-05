# Running rounds on rota

This is the brief for rounds on this repo: the gate, the repo rules and the roster. It is not the
user guide. How a round works for any project is in [Parallel rounds](../usage/parallel-rounds.md).

The orchestrator reads this file and `rota-orchestrate` before it runs or joins a round. A worker reads
[`skills/references/worker-contract.md`](../../skills/references/worker-contract.md); `rota round assign` points it there.
Set `round.brief` to this file's path to make the assignment pointer name it as well.

## The gate

The full gate runs once, at merge: `rota worker gate` runs `refactor.verifyCommands` on the merged
tree through `bash test/gate.sh`: validate-skills, the doc lints (`bash test/doclint.sh`: prose pins, the
`.worktrees/` decoy check, and that every `rota` verb the docs name exists), `go vet ./...`, `go test -race -timeout 30m ./...`
and the smoke suite in `gate.smokeShards` (default 4) shards, all at once. The sharded gate takes about 2–3 minutes;
running the smoke suite in series is several times slower. It takes a machine-wide lock, so two gates never overlap, and
keeps one log per check. Every check makes its temp files under one gate-owned root, and the gate fails if
any entry is left in it afterwards, so a run that leaks shows up as a red gate, not as a full `/tmp`.
Workers do not run it.

GitHub CI (`.github/workflows/ci.yml`: gofmt, `go vet`, `go test`, validate-skills, doclint) is a backstop, not
the merge gate. It runs on pushes to `main`, on PRs once they are ready for review (opened non-draft,
reopened, or marked ready; drafts are skipped) and on manual dispatch, not on every push to an open PR. A
newer run on the same ref cancels the older one. The local gate above decides whether a PR merges.

Before a PR, a worker runs targeted checks only, as the [worker contract](../../skills/references/worker-contract.md)
says: `python3 test/validate-skills.py` (under a second), `bash test/doclint.sh` when it touched a skill or doc, `go vet` and `go test` for the packages it
touched, and only the smoke sections its change adds or touches, sourced through `test/runner.sh`
in a sandbox (sections are never executable alone). Several workers running full suites at once
starve the CPU and turn time-budgeted tests into false reds. A stale branch does not need a re-run
either: the merge gate verifies the merged tree.

If the merge gate fails, the orchestrator first runs it on `origin/main` in a throwaway worktree
(`.rota/KNOWLEDGE.md`, "Build & Tooling: Smoke testing", entry "Pre-existing smoke failures: check main before
triaging your branch"). A failure that main shares pre-exists the PR and is captured as its own bug; otherwise the
orchestrator bounces the PR with the failing check.
New smoke sections take the number your dispatch assigns; do not pick one yourself, siblings are
numbering theirs at the same time.

Most of `cmd/rota`'s test time is the `TestFrozen*` scenario suites: each scenario runs the Go
binary and compares what it did with its record in `cmd/rota/testdata/frozen/`. A deliberate
behaviour change updates the record with
`go test ./cmd/rota -run '^TestFrozen<Suite>$' -update-frozen`; say why in the PR, since the jsonl
diff is the review. The 30m timeout is for a loaded box (#120).

The package goldens in `internal/*/testdata/golden/` (`internal/golden`) work the same way for
tests that call `golden.Check`: `go test ./internal/<pkg> -run '^TestX$' -update-golden`
rewrites the changed outputs if the rest of the test passes, and the JSON diff is the review.

There are no servers and no ports in this repo.

## Review, merge and completion

- **One merge owner: the orchestrator.** Workers open PRs and stop. The orchestrator merges each
  through `rota worker gate`, which reviews the merged tree. `/rota-ship` never merges in issue mode
  and `/rota-review --queue` is for a session outside a round, not for round PRs.
- **Completion follows the merge.** On the issue backend the merge closes the issue. In file mode the
  orchestrator completes the PR's items at merge time; a worker skips `/rota-ship` Step 8.
- **Workers never review.** A worker runs no `/rota-review` and dispatches no reviewer subagent on
  its own branch; `/rota-ship` skips Step 3 for a round worker's PR. Review is the orchestrator's
  seat.
- **No second opinion on round PRs.** `ship.secondOpinion` is skipped for a round worker's PR even
  when set: the merge gate is the check, and a further model pass per PR costs more than it
  catches.
- **`work.mergeStrategy: direct` is ignored in issue mode.** `/rota-ship` always opens a PR there and
  says so; a round never direct-merges.

## Repo rules that bind workers

- Edit canonical sources only: `cmd/`, `internal/`, `skills/` (`rota-*/SKILL.md`, `references/`), `docs/`, `test/`.
- Never hand-edit tracked `.rota/` content. This repo uses the issue backend ([issue backend](../usage/issue-backend.md)), so the merge closes your issue and there is no backlog row to update.
- Before touching a verb, pull the matching `.rota/KNOWLEDGE.md` topics with
  `rota knowledge query "<exact ## heading>"`. The topics that bite most: *Architecture: Helper
  conventions & invariants*, *Architecture: Module extraction & migration safety*, *Build &
  Tooling: Smoke testing*.
- A new verb needs a contract entry and a smoke section. The entry goes in the group file under `docs/design/contract/`
  that holds its siblings (`docs/design/contract/README.md` maps groups to files); add the verb to the README index too.
- Config keys are documented in three places at once: `docs/reference/config-options.md`,
  `docs/usage/configuration.md` and `internal/config/schema.go`. Touch only the lines about your key; a sibling may be adding
  another key in the same files.
- Stage explicit paths. Commit messages: imperative subject under 72 chars, body says why,
  no `Co-Authored-By` trailer.
- The PR body carries an `## Approvals` section citing the channel of every decision you
  acted on (`None` if you acted only on your brief, which is not a relay). A `## Rulings`
  section lists your own calls as `<what> — <why> — <cost if wrong>`. Reference the issue so it closes on
  merge, unless the PR is a partial slice.

## Tracker CLI gotchas

On this repo `gh issue view <N> --comments` and `gh pr edit` fail with a Projects-classic
GraphQL deprecation error. Read an issue with `gh issue view <N> --json title,body,comments`
(the brief says `--comments`; use this instead), and edit a PR body through the REST API:

```sh
gh api -X PATCH repos/<owner>/<repo>/pulls/<N> -F body=@body.md
```

## Roster

Slots are provisioned once and reused: `.worktrees/<agent>`, parked on `park/<agent>`, working
on `<agent>/<issue>-<slug>`. `round.roster` sets the names; the default is `ben`, `dana`, `nia`,
`kit`. A longer round adds names with `rota config set round.roster '[...]'`.

Each slot's account (`CLAUDE_CONFIG_DIR`) comes from `work.accounts`. Those are paths on one
machine, so they live in the developer's gitignored `.rota/config.local.json`, not in tracked files.
`rota worker account list` shows what each slot would use.

Model per dispatch is the orchestrator's call, through `--tier` (`light`, `standard`, `heavy`; see
[tiers](../usage/configuration.md#round-keys)). Default `standard`; `heavy` for multi-helper features.

### Grouping a slot under the project in herdr

herdr groups a slot under the project only when its workspace is a **linked worktree
workspace** of the project's primary workspace. A workspace made with plain
`herdr workspace create`, or a worktree moved with `git worktree move`, is not linked and
shows up as a separate project. `rota round start` makes the worktree with git and calls no
herdr, and `rota worker dispatch` opens its tabs in the orchestrator's own workspace, so
neither is affected. This matters for a standing agent you run in its own herdr workspace.

Provision such a slot from the primary workspace:

```sh
herdr worktree create --workspace "$HERDR_WORKSPACE_ID" --path .worktrees/<agent> \
  --branch park/<agent> --base main --label <agent> --no-focus
```

To link an existing unlinked slot in place, leaving the agent running:

```sh
herdr worktree open --workspace <primary id> --path .worktrees/<agent>
herdr workspace rename <id> <agent>
```

The maintainer checked both in the herdr sidebar (round 4, #79). Workspace ids are re-derived from
`herdr workspace list` at the start of each round; the label is the handle.

Tools that walk the tree without reading `.gitignore` see a second copy of every file
under `.worktrees/`; none of this repo's verbs or tests do (smoke section 70 pins it).

## Maintainer answers typed into a pane

Prefix a direct answer with `m:` to make it citable without a confirmation round-trip. The
prefix is imitable, so a prefixed line that contradicts the last signed orchestrator
message still gets one confirmation.
