# Running rounds on rota

This is the brief for rounds on this repo: the gate, the repo rules and the roster. It is not the
user guide. How a round works for any project is in [Parallel rounds](../usage/parallel-rounds.md).

The orchestrator reads this file and `rota-orchestrate` before it runs or joins a round. A worker reads
[`skills/references/worker-contract.md`](../../skills/references/worker-contract.md); `rota round assign` points it there.
Set `round.brief` to this file's path to make the assignment pointer name it as well.

## The gate

The full gate runs once, at merge: `rota worker gate` runs `test.full` on the merged
tree through `bash test/gate.sh`: validate-skills, the doc lints (`bash test/doclint.sh`: prose pins, the
`.worktrees/` decoy check, that every `rota` verb the docs name exists, and the contract-doc stamps below), the grep gate (`bash test/grep-gate.sh`), the gofmt check (`bash test/gofmt.sh`, the same `gofmt -l` CI runs), `go vet ./...`, `go test -race -timeout 30m ./...`
and the smoke suite in `gate.smokeShards` (default 4) shards, all at once. The sharded gate takes about 2–3 minutes;
running the smoke suite in series is several times slower. It takes a machine-wide lock, so two gates never overlap, and
keeps one log per check. Every check makes its temp files under one gate-owned root, and the gate fails if
any entry is left in it afterwards, so a run that leaks shows up as a red gate, not as a full `/tmp`.
The gate builds with `-trimpath` (it adds it to `GOFLAGS`), so each fresh scratch worktree reuses the Go build cache instead of adding a copy per path (#709).
Workers do not run it. The same goes for `test.e2e`: the gate and the merge train run it on the merged tree after `test.full`, and round PRs skip per-branch ship-time QA. Here `test.e2e` is the scripted stub-worker scenarios; see [E2E scenarios](e2e-scenarios.md).

An item may carry a `## Try it` section: `- <command>` bullets, each optionally followed by `expect: <substring>` lines. After
`test.full` and `test.e2e` pass, the gate runs the commands on the merged scratch tree, in order. Each runs with a scrubbed
environment: `PATH`, an empty temp `HOME` (with the XDG and `GH_CONFIG_DIR` dirs inside it, removed when the run ends),
the gate host's `GOCACHE`, `GOMODCACHE` and `GOPATH`, the slot's `ROTA_PORT_BASE`, `ROTA_PORT_BLOCK` and `ROTA_DB_SUFFIX`,
and git hardening: `GIT_SSH_COMMAND` set to an ssh that offers no identity, `GIT_CONFIG_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`,
and no `SSH_AUTH_SOCK`. No variable of the gate's own environment is passed on. This is not a sandbox, and the collaborator trust check below is the boundary. Two known gaps: ssh resolves `~` through passwd, not `$HOME`, so the gate user's `~/.ssh` stays readable; and `/proc/$PPID/environ` exposes the gate's own environment, tokens included. A step still runs as the gate's OS user, so any file that user can read is readable by its absolute path. Output is capped at 4 MiB;
a step that times out keeps the output it wrote, ending in a `[timed out: ...]` line. The commands come from an issue
body, so on a forge tracker they run only when the issue's author and its last editor are collaborators with write access
or higher, judged on the same body that is then run; otherwise the gate says why in a `BEHAVIOUR-SKIP` note and goes on (a skip is not a failure). GitHub is the only
tracker that can answer that; GitLab, Jira and Linear issues skip. A gate with no PR reads no origin refs and so no forge: a
forge item skips there too. A file-mode item body is read from the base checkout, so a
commit someone with push access landed put it there, and it runs unchecked. `round assign` makes the same decision for the worker's
brief: trusted steps are quoted in it, and when they would be skipped the brief says they are withheld and why, and not to run them. A non-zero exit or a missing substring bounces the slot; the transcript is
posted on the PR and saved under `.rota/qa-runs/behaviour/`. Commands run as written, so a repo that tests its own binary
writes `go run ./cmd/<x> ...`. No section, no run.

GitHub CI (`.github/workflows/ci.yml`: gofmt, `go vet`, `go test`, validate-skills, doclint) is a backstop, not
the merge gate. It runs on pushes to `main`, on PRs once they are ready for review (opened non-draft,
reopened, or marked ready; drafts are skipped) and on manual dispatch, not on every push to an open PR. A
newer run on the same ref cancels the older one. The local gate above decides whether a PR merges.

To reproduce CI's `go test` locally, run it with no developer state reachable. A test that only passes
with a real `HOME` or `CLAUDE_CONFIG_DIR` (for example by finding the installed rota skills) fails on CI:

```sh
env -i PATH="$(dirname "$(command -v go)"):/usr/bin:/bin" HOME=/nonexistent \
  GOCACHE="$(go env GOCACHE)" GOPATH="$(go env GOPATH)" go test ./... -count=1
```

A fixture that assigns a worker must carry its own `skills/references/worker-contract.md`; see `newScopeFx`.

Before a PR, a worker runs targeted checks only, as the [worker contract](../../skills/references/worker-contract.md)
says: `rota test run fast`. In this repo that runs `test/fast.sh`, which maps the changed files to
`python3 test/validate-skills.py`, `bash test/doclint.sh` (skill, doc or markdown changes), `go vet` and `go test` for the
packages it touched, and only the smoke sections its change adds or touches, sourced through `test/runner.sh`
in a sandbox (sections are never executable alone). Several workers running full suites at once
starve the CPU and turn time-budgeted tests into false reds. A stale branch does not need a re-run
either: the merge gate verifies the merged tree.

If the merge gate fails, the orchestrator first runs it on `origin/main` in a throwaway worktree
(`.rota/KNOWLEDGE.md`, "Build & Tooling: Smoke testing: Gate & frozen records", entry "Pre-existing smoke failures: check main before
triaging your branch"). A failure that main shares pre-exists the PR and is captured as its own bug; otherwise the
orchestrator bounces the PR with the failing check.
New smoke sections take the number your dispatch assigns; do not pick one yourself, siblings are
numbering theirs at the same time. Start from `test/section-template.sh`.
Sections run under `set -euo pipefail`, so a bare `OUT=$(rota ...)` aborts the section silently when the
verb exits non-zero. Follow every capture of a rota call with `|| vfail` (defined in `test/lib.sh`), which
names the verb and its exit code. `python3 test/lint-sections.py test/sections` lists the bare captures,
quoted and multi-line ones included; `--fix` appends the handler.

Most of `cmd/rota`'s test time is the `TestFrozen*` scenario suites: each scenario runs the Go
binary and compares what it did with its record in `cmd/rota/testdata/frozen/`. A deliberate
behaviour change updates the record with
`go test ./cmd/rota -run '^TestFrozen<Suite>$' -update-frozen`; say why in the PR, since the jsonl
diff is the review. The 30m timeout is for a loaded box (#120).

The package goldens in `internal/*/testdata/golden/` (`internal/golden`) work the same way for
tests that call `golden.Check`: `go test ./internal/<pkg> -run '^TestX$' -update-golden`
rewrites the changed outputs if the rest of the test passes, and the JSON diff is the review.

There are no servers and no ports in this repo.

## Contract doc stamps

Every `docs/contributing/contract/*.md` opens with frontmatter naming the repo paths it describes:

```
---
refs:
  - internal/gate
  - internal/cli/gate.go
---
```

The commit each doc was last verified against lives in one sidecar, `docs/contributing/contract/STAMPS`, one
`<doc>.md <sha>` line per doc. It is not in the doc, so two PRs editing one doc never conflict on a stamp line.
**A PR never edits `STAMPS`** (a new contract doc is the one exception: add its line).

`test/doclint.sh` runs `test/check-doc-stamps.py`. It fails a doc whose stamp is missing or malformed, whose sha is
not a known commit, or whose `refs:` path does not exist: those are mistakes in the stamp itself. Drift (a ref changed
between the stamped sha and `HEAD`, `git diff <sha> HEAD -- <ref>`) only prints a `WARN` naming the doc and the changed
paths and exits 0, so an unrelated PR is never blocked by it. `ROTA_DOC_STAMPS=strict bash test/doclint.sh` (or
`python3 test/check-doc-stamps.py --strict`) fails on drift too; run it before `/rota-docs` and the re-stamp
below. Other docs under `docs/` need no stamp.

To re-stamp (on `main`, as bookkeeping after merges; the strict run lists what to fix): `git diff <sha> HEAD -- <ref>`
for each flagged path, read the doc against that diff and fix what drifted, then
`python3 test/check-doc-stamps.py --restamp <doc>.md…` sets those stamps to `git rev-parse HEAD`. Add a path to
`refs:` when the doc starts describing new code.

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
  `rota knowledge query "<heading text>"`; a partial name returns every heading containing it, an exact name returns that topic alone (`rota knowledge topics` lists them). The topics that bite most: *Architecture: Helper
  conventions & invariants*, *Architecture: Module extraction & migration safety*, *Build &
  Tooling: Smoke testing*.
- A new verb needs a contract entry and a smoke section. The entry goes in the group file under `docs/contributing/contract/`
  that holds its siblings (`docs/contributing/contract/README.md` maps groups to files); add the verb to the README index too.
- Config keys are documented in three places at once: the schema row in `internal/config/keys.go`
  (name, default, `Type`, `Desc`, `Choices`), the generated `docs/reference/config-options.md`
  (run `go generate ./internal/config`; `bash test/doclint.sh` fails on drift) and the prose in
  `docs/usage/configuration.md`. Touch only the lines about your key; a sibling may be adding
  another key in the same files.
- Stage explicit paths. Commit messages: imperative subject under 72 chars, body says why,
  no `Co-Authored-By` trailer.
- The PR body carries an `## Approvals` section citing the channel of every decision you
  acted on (`None` if you acted only on your brief, which is not a relay). A `## Rulings`
  section lists your own calls as `<what> — <why> — <cost if wrong>`. An `## Assumptions` section lists each question the
  issue left open that you answered from the code, as `<question> — <answer> — <where in the code>`
  (`None` if there were none); it is not a Ruling. Reference the issue so it closes on
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

Each slot's account (`CLAUDE_CONFIG_DIR`) comes from the `claude` entries of `round.accounts` (`{kind, name, dir}`). Those are paths on one
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
neither is affected while `round.herdrGroupSlots` is off: with it on (the default) dispatch
opens each slot as a linked workspace itself (see below). The manual recipe matters for a standing agent you
run in its own herdr workspace.

`--path` must be **absolute**. A relative path fails with `worktree_not_found` (checked on
herdr 0.9.5), probably because herdr resolves it against the server's directory.

Provision such a slot from the primary workspace:

```sh
herdr worktree create --workspace "$HERDR_WORKSPACE_ID" --path "$PWD/.worktrees/<agent>" \
  --branch park/<agent> --base main --label <agent> --no-focus
```

To link an existing unlinked slot in place, leaving the agent running:

```sh
herdr worktree open --workspace <primary id> --path "$PWD/.worktrees/<agent>"
herdr workspace rename <id> <agent>
```

With `round.herdrGroupSlots` on (the default), `rota worker dispatch` does this for every
slot, and `rota layout tabs` returns a split's panes to their workspaces (`rota layout split`
moves them into the orchestrator's tab, so they leave until then):
[`herdr-dispatch.md`](../../skills/rota-orchestrate/references/herdr-dispatch.md#linked-workspaces-per-slot)
has the call sequence and what changes for the orchestrator.

The maintainer checked both in the herdr sidebar (round 4, #79). Workspace ids are re-derived from
`herdr workspace list` at the start of each round; the label is the handle.

Tools that walk the tree without reading `.gitignore` see a second copy of every file
under `.worktrees/`; none of this repo's verbs or tests do (smoke section 70 pins it).

## Support role

A second machine can build `site:support` issues beside main with no connection between the
two: GitHub is the only shared state. `round.support` (tracked) names the machine and its
roster prefix; `round.role` (local only, `.rota/config.local.json`) turns a machine into the
support side. A support machine never merges and never writes tracked `.rota/` content; those
verbs exit 4 `blockedBy: role` (the tables live in `internal/cli/support_fence.go`).

Setting one up takes two steps and no hand edits:

1. On main, `rota round support enable <name>` writes `round.support` to the tracked
   `.rota/config.json`. Commit and push it. `rota round support disable` removes it.
2. On the second machine, clone or pull the repo and run `rota setup`. Answer `support` to the
   first question (or pass `--set round.role=support`). Setup checks that the tracked config names
   a support machine and a tracker, asks only the machine-local questions (dispatch, autonomy,
   worker and orchestrator harness), writes them with `round.role: support` to
   `.rota/config.local.json` and ends with the doctor's first failing check, or `doctor: pass`.
   Run it again to change an answer; it starts from the values already there.

Knowledge travels through the tracker. A support worker posts each learning as a `learning`
comment on the issue it came from (`rota item comment add <ID> --kind learning --body-file <f>`),
never into `KNOWLEDGE.md`, and does not restamp the contract docs. A learning with no issue is
filed upstream with `/rota-bugreport` or `/rota-capture`. Main's `/rota-learn` runs
`rota knowledge harvest` (learning comments on issues merged in the round, not yet marked),
folds each in with `rota knowledge add` and marks it with `rota knowledge harvest --mark
<commentId>`. `--mark` is refused on a support machine; listing is allowed. Only comments by
authors with write access count, so a stranger's `learning` comment never reaches
`KNOWLEDGE.md`; a forge that cannot say who may write (GitLab) is refused with `blockedBy: forge`,
and so is `round start` with `round.support` set. Knowledge hits from
support machines are not harvested.

Set `round.support` between rounds: flipping it mid-round switches the bounce store and bounce
counts are not carried over. At the bounce cap the item parks `needs-human` and no review-bounce
is posted.

### The round trip between the machines

- **Hand back by default.** When an offered issue cannot be worked (not ready, wrong premise,
  collides with main's work, the maintainer holds it), the support machine runs
  `rota round support return <ID> --reason-file <f>` at once and never idles on it. The reason
  goes on the issue as a signed comment and the support label comes off, so main's next
  `round candidates` lists the issue again. It is refused on main (`blockedBy: role`) and while a
  support slot holds the issue (`blockedBy: claimed`).
- **The issue thread is the index.** Every note one orchestrator posts for the other is an issue
  comment: `--kind feedback` for a plain note, `--kind relay` for a relayed instruction
  (`rota item comment add <ID> --kind feedback|relay`; there is no `note` kind). Bounce, pick and the review relay
  also leave a one-line `rota:pr-pointer` comment on the issue linking the PR post. Comments, relays included, carry
  a `rota:from <role>` stamp; only comments by authors with write access count.
- **What each watch wakes on.** Main's `round watch` exits on a changed PR head for an offered or
  adopted issue, a PR opened for a `site:support` issue, and a new comment from the support
  machine on an offered or held issue. The support machine's watch exits on a new comment from
  main on the issues its slots hold. Anything not yet reported wakes the watch once, so a PR that
  was already open when the watch started is not swallowed. Only the offered-or-held set is read,
  once per forge poll.
- **How approvals travel.** A relay the support orchestrator sends is also posted on the issue
  as a `rota:relay round=<n>` comment (`worker dispatch --relay` does it on a support machine).
  Main's gate accepts a support PR's `## Approvals` citing that relay without a local relay log
  entry, and the `round support offer` comment counts as the assignment approval. A cited relay no
  trusted comment holds still fails PROVENANCE-FAIL. A worker cannot post `--kind relay`.

## Maintainer answers typed into a pane

Prefix a direct answer with `m:` to make it citable without a confirmation round-trip. The
prefix is imitable, so a prefixed line that contradicts the last signed orchestrator
message still gets one confirmation.
