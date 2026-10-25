# Review and ship

`/rota-ship` integrates completed work into main (or opens a PR), gated by `/rota-review` by default.

## 🔍 /rota-review

`/rota-review` is a staff-engineer-level read of a feature branch before it leaves your machine. It is **read-only**: no commits, no mutations. The skill scopes the branch (commits, touched files, referenced item IDs), pulls relevant topics from [`KNOWLEDGE.md`](learning.md) and `DECISIONS.md`, resolves what each item promised, and dispatches two reviewers in parallel: Spec (does the diff do what the items promised, nothing more) and Standards (conventions, code smells, test quality, silent failures). Their reports are shown as separate sections; the overall verdict is the worse of the two. Standards runs on the `standard` model tier (`round.tiers.claude.standard`). The diff is not pasted: `rota review package` writes the commits, `--stat` and full diff (with 10 lines of context) to a gitignored file under `.rota/review/`, and both reviewers read that file, so a large branch needs no file cap. After fixes, `--since <sha>` packages only what changed since the last review.

### What counts as the spec

In issue mode the issue body is the spec, plus any `decision` comments on it. A plan note, when one exists, adds detail. In file mode it is the item's intent line plus its plan, when it has one. An item with nothing but a title is reviewed against the title, and the reviewer says when a spec is too thin to check.

### What the reviewer checks

- **Intent match:** does the diff deliver every outcome in the spec? A partly met outcome is `CONCERNS`; a missing outcome or edits the spec doesn't imply is `FAIL`. Locally sensible steps nobody asked for surface as `CONCERNS` naming the drift; drift alone never fails the review.
- **Convention compliance** against captured `KNOWLEDGE.md` topics.
- **Decision violations:** any forbidden pattern from `DECISIONS.md` present in the diff = `FAIL`.
- **Obvious quality:** dead code, swallowed errors, untested branches, security smells, API breaks, performance cliffs.
- **Stale scaffolding:** leftover *Task N* / *placeholder* / *in-flight* annotations that should have been removed once the corresponding work landed.
- **Silent-failure hunter:** for every verification claim in the diff (new test, smoke section, assertion, helper-output check), apply a four-question rubric: *(a)* what does this verify concretely? *(b)* is the asserted-on shape the same shape the real consumer reads? *(c)* was the new code path actually exercised? *(d)* if you deleted the new code, would the assertion still pass? If any answer is *no* or *unclear*, the claim is flagged `SILENT-FAIL` with file:line. Flags surface as CONCERNS; they don't break the build alone, but you see them before merging.

### Verdict

The report ends with one verdict (`PASS` / `CONCERNS` / `FAIL`) with file:line evidence where applicable. It is recorded with `rota verdict add` in the gitignored `.rota/verdicts.json`, and `/rota-ship` routes on the recorded verdict with `rota verdict route`, not on the report text.

| Verdict | Meaning |
|---------|---------|
| `PASS` | Clean. |
| `CONCERNS` | Issues found, but not blocking. You can proceed or fix first. |
| `FAIL` | Integration is blocked. |

Each finding is blocking or not. By default `blocker` and `major` findings block and `minor` and `info` do not. Only blocking findings are relayed to a round worker and count toward a bounce. Non-blocking findings above `info` are deferred: `rota verdict add --file-deferred --pr <ref>` collects them in one follow-up task item per PR, related to it, instead of relaying them. A later verdict on the same PR adds to that item. `review.fileDeferred` sets the floor: `major` (default; minor findings stay in the verdict record and the output lists them as kept), `minor` or `off`. A PR whose issue is itself a deferred-findings item files nothing.

You can run `/rota-review` at any time on a branch, not only before shipping. On the [issue backend](issue-backend.md), `/rota-review --queue` reviews the PRs waiting on `needs-review` items and merges the ones that pass.

## 🚢 /rota-ship

`/rota-ship` bundles a completed feature branch into main. Typical usage after finishing work:

```
/rota-ship
```

**Default flow:** branch check → `/rota-review` (unless `ship.review` is `none`) → second-opinion gate (if `ship.secondOpinion` is `true`) → `/rota-qa` gate (if `ship.qa` is `true`) → UAT gate (if `ship.uat` is `true`) → build PR body → open PR or merge → close resolved items (on the issue backend the PR merge closes them).

The review gate behaves as follows:

- `FAIL` blocks integration; fix the branch and rerun `/rota-ship`.
- `CONCERNS` surfaces to you; you can proceed or address it first.
- `PASS` lets integration run automatically.

### Second-opinion gate (opt-in)

When `ship.secondOpinion: true` and `/rota-review` returned `PASS`, `/rota-ship` dispatches a fresh subagent with **no prior conversation context** and gives it only the diff plus the stated goal. `/rota-review` shares the project's context (conventions, `KNOWLEDGE.md`, plan) with the work it produced, and a reviewer with that context normalizes blind spots. A reviewer without it must reason from the diff alone, catching what the contextualized reviewer let pass.

Returns `PASS` / `CONCERNS` / `FAIL` and routes through the same verdict logic as `/rota-review`. Skipped if the user already accepted CONCERNS in Step 3 (no value in re-litigating), and for a round worker's PR (the orchestrator's merge gate is the second check). Opt-in because it adds one fresh-context roundtrip per ship and most cycles don't need it. Enable when shipping release tooling, security paths, or data migrations. See [`ship.secondOpinion`](configuration.md#shipsecondopinion).

### QA gate (opt-in)

When `ship.qa: true`, `/rota-ship` invokes [`/rota-qa run`](qa.md) between review and the merge step. `/rota-review` and the second-opinion gate answer *"does the diff make sense"*. `/rota-qa` answers *"does the product actually work"* by executing the per-target strategy in `.rota/qa/<target>.md` (Playwright, smoke, lighthouse, axe, ZAP, contract tests).

Verdict routes per `qa.gate`:

| `qa.gate` | `PASS` | `CONCERNS` | `FAIL` |
|---|---|---|---|
| `"advisory"` (default) | continue silently | surface findings, continue | surface findings, continue (advisory means advisory) |
| `"blocking"` | continue silently | ask you how to proceed | stop the ship |

`INFRA-FAIL` (dev server / creds / binary missing) is always advisory regardless of `qa.gate`. See [product QA](qa.md) for the full strategy file format.

### UAT gate (opt-in)

When `ship.uat: true`, `/rota-ship` runs [`/rota-qa uat <ID>`](qa.md#uat-human-acceptance) for each item on the branch, after review and the QA gate and before the merge or PR. It lists the item's acceptance criteria, auto-passes the ones a PASS test proof row covers and asks you about the rest. A criterion marked `(human)` in the item body always comes to you: `- [ ] AC-4: the error copy reads clearly (human)`. A failed criterion becomes a new captured item that names the original under `Related:`; it does not block the ship. Round PRs never get this gate: `rota worker gate` and the round merge path ignore `ship.uat`.

Use `/rota-ship` to integrate finished work. Finish the implementation cycle in [`/rota-work`](running-work.md) first; don't call `/rota-ship` mid-implementation.

## Direct merge vs PR

`work.mergeStrategy` in `.rota/config.json` controls how the branch is integrated. On the [issue backend](issue-backend.md) this setting is ignored: `/rota-ship` always opens a PR / MR and never merges.

| Strategy | How it works | When to use |
|----------|-------------|-------------|
| `"direct"` | Merges to main and deletes the branch locally. | Solo work, fast iteration. |
| `"pr"` | Pushes the branch and opens a GitHub PR or GitLab MR with a generated body. | Team work, required code review. |

The PR body is built from commit subjects, the list of resolved item IDs, and a short test plan derived from the touched areas.

See [configuration](configuration.md) for the full `work` block.

## What `ship.review` controls

`ship.review` in `.rota/config.json` decides how deep a review `/rota-ship` runs before integrating:

| Value | Behavior |
|-------|---------|
| `full` (default) | `/rota-review` runs first. `FAIL` blocks, `CONCERNS` surface but you can proceed, `PASS` flows through. |
| `light` | Only the Spec reviewer runs, on the `standard` tier; the report says the Standards axis was not run. A branch that references no issue has no spec and gets the Standards reviewer instead. |
| `none` | Skips the review pass. Integration runs immediately. Use when you have already reviewed manually and want to skip the second pass. |

`true` and `false` still work as `full` and `none`. An object picks the depth by diff size, label and path (for example `risk:high` forces `full`), and lifts a `light` review to `full` on a `Door: one-way` PR, a diff outside the issue's declared files or a risky path. Support files do not count as outside: tests beside a declared file, docs (the generated option reference included), frozen and golden records and smoke sections (`ship.review.supportPaths` sets the list); see [configuration](configuration.md#shipreview). `/rota-ship` prints the chosen depth and why.

The review gate is independent of the autonomy level. A `FAIL` verdict still halts the chain until you fix the branch.

## Review lenses

Security, performance and UX get one line of attention in the Standards reviewer's baseline, and at `light` depth that reviewer does not run. A lens is a focused reviewer for one axis, chosen by what a branch touches. `review.lenses` maps a lens (`security`, `perf` or `ux`) to path globs and issue labels; the default is empty, so no lens runs:

```bash
rota config set review.lenses '{"security": {"paths": ["internal/git/**"], "labels": ["risk:security"]}, "ux": {"paths": ["internal/cli/**"]}}'
```

Globs allow `*` within a segment and a trailing `/**` for a directory. An unknown lens name or a malformed glob is a config error naming `review.lenses`. A lens triggers when a changed file (renames count both paths) or a label of the branch's issue, or of any issue in a bundled slot, matches. At depth `none` none triggers.

- **What shows.** `rota review depth --json` lists `lenses`: one `{name, why}` per triggered lens, with `why` as `path <file>` or `label <name>`. A branch that triggers none lists an empty array.
- **What runs.** `/rota-review` runs one read-only reviewer per triggered lens beside Spec and Standards, at `full` and at `light`: `security` on `orchestrator.model`, `perf` and `ux` on the standard tier. Each records `review-security`, `review-perf` or `review-ux` (PASS, CONCERNS or FAIL) at the reviewed commit. A `--since` re-review runs the triggered lenses again on the fix range, majors only.
- **What it blocks.** `rota worker gate` refuses the merge (`review-missing`) until each triggered lens has a record at the PR's head; the refusal names the lens and why, for example `review-security verdict (path internal/git/run.go)`. A lens FAIL blocks and bounces like any review FAIL, and its findings are relayed; `rota verdict add --file-deferred` files the deferred ones.

## Cross-harness review

The two reviewers run on the harness you work in, so code Claude wrote is reviewed by Claude. For the work where a miss costs most, a third review comes from a different model. Once `round.harnesses` lists more than one harness (for example `["claude", "codex"]`), an issue labelled `size:Major` or `risk:high` (`review.crossLabels`) needs one read-only review from a harness other than its author's. `rota review depth --json` shows which one as `cross`, and why as `crossWhy`.

- **Who reviews.** The first `claude` or `codex` entry of `round.harnesses` that is not the author's harness (the slot that built the PR, else the issue's `harness:<kind>` label). A `review:<harness>` label on the issue picks it instead, and `review:none` skips the cross review.
- **How it runs.** `/rota-review` runs the other harness once, headless, in a scratch extract of the reviewed commit (for Codex, `codex exec --sandbox read-only`). It takes no worker slot. It returns the same verdict block, recorded as `review-cross` with the reviewer's harness and model; `rota verdict show` prints both.
- **What it blocks.** `rota worker gate` refuses the merge (`review-missing`) until a `review-cross` from that harness is recorded at the PR's head. A FAIL blocks and bounces like any review. A malformed block is retried once; a run that cannot happen (missing binary, quota, a second bad block) records nothing and says `cross-harness review unavailable`, and the PR waits until the review runs or someone adds `review:none`.
- **Releases.** `/rota-release` runs one more over the range since the last tag, on contract and JSON changes, exit codes, migrations and config keys, and CHANGELOG claims. Its findings appear beside the notes for you to approve. They never stop the release.

On Linux, Codex's sandbox needs unprivileged user namespaces. Where the kernel forbids them (Ubuntu 24.04 and later), every sandboxed command fails, so the reviewer reruns without the sandbox inside the throwaway extract and says so in its report.

See [configuration](configuration.md) for the full `ship` block.

## Release nudges

Once you've accumulated commits since the last release tag, [`/rota-work` (no argument)](picking-work.md) (on terminal paths, when you stop without entering `/rota-work`) and `/rota-ship` (in its post-ship report) surface a one-line reminder:

```
5 commits since v1.16.0; consider /rota-release.
```

The nudge fires when EITHER `release.nudgeAfterCommits` (default 10) OR `release.nudgeAfterDays` (default 14) is reached; see [configuration](configuration.md#releasenudgeaftercommits). It's informational; no skill is auto-invoked. The first release is always your call (no nudge fires while no tag exists).

## Release checklist

`/rota-release` walks a per-project checklist (`.rota/RELEASE.md` by default) as a preflight gate before the version bump and before any writes. Each `- [ ]` line is one gate. Released-once-forgotten-forever drift like a sibling version file going stale, a missing CHANGELOG humanization pass, or an infra rollout step nobody owns ends up here. The skill itself stays generic.

```markdown
# Release Checklist

- [ ] `VERSION` matches the CHANGELOG heading for the release
- [ ] CI is green on the release branch
- [ ] Migration notes for users on the prior version are written
- [ ] Push staging migration (manual)
```

For each gate the skill asks: *Yes, continue* / *Fix now and continue* / *Skip this item* / *Abort release*. Skipped items show up in the post-release summary so the release record stays honest. Items ending in `(manual)` always interject even under `autonomy.level: roundtrip` or `auto`, useful for sensitive gates that should not auto-acknowledge.

When the file is absent, the skill offers to scaffold a starter under `autonomy.level: off`, or silently skips the gate under `roundtrip` and `auto` (don't interrupt unattended runs). See [`release.checklistPath`](configuration.md#releasechecklistpath) to override the path.

The file is tracked by default, so the release checklist is shared with the team like any other source file. To keep it per-contributor instead, add `.rota/RELEASE.md` to `.gitignore`.
