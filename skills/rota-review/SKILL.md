---
name: rota-review
description: Use on "review this", "check before I ship", "look over the branch", before a merge or PR, or implicitly from /rota-ship.
---

# rota-review — Pre-Merge Review

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model for the Spec reviewer (default `opus`)
- `round.tiers.claude.standard` — model for the Standards reviewer (the `standard` tier; silent default `models.worker`). No review-specific key.

## When to Use

- Before merging or opening a PR, typically from `/rota-ship`
- "Review this branch", "second-opinion this"
- After manual commits you want validated before integrating
- `/rota-review --since <sha>` re-reviews a bounced branch: only the fix, against the last review's findings (see Re-review)
- `/rota-review --queue` (issue backend) reviews and merges the PRs waiting on `needs-review` items (see Queue mode)

## When NOT to Use

- Code still in flight → `/rota-work`
- You want code changed based on the review → `/rota-refactor` or a fresh `/rota-work` run
- Nothing committed yet
- You want product-level evidence (works? perf budgets? a11y? smoke green?) → `/rota-qa run`. `/rota-review` reasons from commits and diff; it does not run the product.

Copy this checklist and track your progress:

```
- [ ] Step 2 — Scope the Review
- [ ] Step 3 — Resolve the Spec
- [ ] Step 4 — Consult KNOWLEDGE & DECISIONS
- [ ] Step 5 — Capture the Diff
- [ ] Step 6 — Pre-flight Scaffolding Scan
- [ ] Step 7 — Dispatch the Reviewers
- [ ] Step 8 — Record and Relay the Verdict
- [ ] Step 9 — Route Based on Verdict
```

## Step 2 — Scope the Review

```bash
rota review scope --json <branch>
```

When the branch lives in a sub-repo (umbrella), read [`umbrella-mode.md`](umbrella-mode.md) for the `--repo` flag at Steps 2, 6 and 8.

Default to the current branch if none is named. `data` holds:

- `branch`, `base`, `commitCount`
- `commits` — array of `{hash, subject}`
- `touchedFiles` — paths changed vs base
- `referencedIds` — item IDs found in commit messages (`#N` or bracketed `[B07]`)
- `intents` — matched backlog item for each referenced ID

If `commitCount` is 0, stop and tell the user.

## Step 3 — Resolve the Spec

The spec is what each referenced item promised. Collect one entry per `referencedId`, in parallel:

- **Issue mode** (`backlog.backend: "issues"`, the default path): the issue body is the spec. `rota item field list <ID>` returns it; add `rota item comment list <ID> --kind decision`, because a decision recorded as a comment changes the body's promise. A plan note (`rota item note show <ID> --kind plan`, `exists: false` when absent) is extra detail, not a requirement, except its `## Review Focus` section (lift it out and carry it into the brief verbatim) and its `## Relies on` list (lift it out too; Step 4 uses it).

Take each criterion's coverage from `rota item show <ID> --json` → `data.acceptance` (`id`, `text`, `met`, `proof`, `flag`). A criterion is met only with a proof reference (`sha:check`); `flag` `changed` (text edited after the mark), `unproven` (proof row gone or not PASS) or `missing` counts as unmet. Pass the list to the Spec reviewer and report it per item.

When `backlog.backend` is `"file"`, read [`file-mode-spec.md`](file-mode-spec.md) instead.

An item with no body and no plan contributes only its title; the reviewer says so when a spec is too thin to check against.

**No referenced items.** `referencedIds` empty (manual commits, no `#N`) leaves nothing to collect. Do not dispatch the Spec reviewer with an empty items block. Ask once for an item ID or a spec file path. An ID or path becomes the spec (a path's text stands in for the issue body), and the review runs `full` as usual. No answer, or no way to ask (called from `/rota-ship` or a round worker's gate): run `light`, Standards only, and say in the report that the Spec axis was not run because the branch references no items. This overrides Step 7's depth for this review; the recorded verdict carries the note (Step 8).

## Step 4 — Consult KNOWLEDGE & DECISIONS

Apply the canonical K+D query pattern (`references/knowledge-consult.md`). Pick topics from the plan's `## Relies on` list when Step 3 lifted one with entries (query the topics it names, and read each listed entry in full). With no plan, or a list that says `none`, infer topics that plausibly touch the changed areas from `touchedFiles` and commit subjects, liberally (a file under `Networking/` → the `Networking` topic).

Carry KNOWLEDGE bullets into the reviewer brief. Pass DECISIONS entries under a `**Hard boundaries:**` section; the reviewer must **FAIL** if the diff violates any boundary, even if the change looks otherwise good. When the plan listed entries, also pass that list under `**Plan relies on:**` (Standards brief): the reviewer checks the diff against each entry and reports one the diff contradicts or ignores as a finding naming the entry.

> **Register hits after the verdicts, not after the brief.** A bullet landing in the brief is not use. Register in Step 8, once the reviewers have reported: for each KNOWLEDGE bullet the diff was checked against (every entry of `**Plan relies on:**`, or every bullet under `**Relevant project conventions (from KNOWLEDGE.md):**` when there was no list) that no finding names, call `rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"` once, issuing all calls as a single parallel batch. A bullet a finding names (contradicted or ignored) earns no hit; neither does a DECISIONS entry or a bullet that was pruned before the brief. Silent on success. Provisional bullets auto-promote to confirmed once `hits >= learn.promoteThreshold` (default 3).

## Step 5 — Capture the Diff

Write the diff to a file, not into your context:

```bash
rota review package <branch> --base <base> [--since <sha>] --json
```

`data.path` is the file (commits, `--stat` and the full `-U10` diff); `data.files` and `data.bytes` size it. No file cap: the reviewer reads the file, you don't. Exit 3 means an empty range or a `--since` not on the branch: report it and stop.

When invoked with `--since <sha>` (re-review of a bounced branch), read [`re-review.md`](re-review.md) and follow it in place of the full Step 7 brief.

## Step 6 — Pre-flight Scaffolding Scan

Before dispatching, scan the diff for comments that reference task numbers ("added in Task 7"):

```bash
rota review scaffolding --base <base> <branch>
```

No `data.findings` → no candidates. Otherwise carry the matches into the brief as `**Possible stale scaffolding:**`. Do not auto-FAIL; the reviewer judges each match.

## Step 7 — Dispatch the Reviewers

**Gate.** If `rota verdict show <branch> --json` has a review record with `verdict` PASS and `stale: false`, skip this step and report that verdict.

**Depth.** Run `rota review depth <branch> --json`; it reads the labels of a round branch's issue itself, so `risk:high` and `best-of:2` reach it without a flag. `full` dispatches both reviewers; `light` (a small diff or a labelled attempt, per `ship.review`) dispatches the Standards reviewer only: skip the Spec brief, record only `review-quality` in Step 8, and say in the report that the Spec axis was not run. `none` is `/rota-ship`'s to skip; a direct `/rota-review` is an explicit request and runs `full`. What a full review checks does not change.

Dispatch two reviewers **in parallel** (one message, two dispatches), fresh context each, same diff file and same Steps 2-6 context. Two axes, judged apart, so one cannot mask the other:

- **Spec** — **orchestrator** model. Does the diff do what the items promised, and nothing more?
- **Standards** — **`standard`** tier. Does it meet the project's standards?

Fill the bracketed parts from Steps 2-6 and drop any empty section. Read [`reviewer-briefs.md`](reviewer-briefs.md) for the shared block, the Spec and Standards briefs, and the closing calibration and verdict-block text; build both briefs from it.

## Step 8 — Record and Relay the Verdict

Save each reviewer's JSON block to its own temp file. Record **spec first, then quality**, at the same sha (umbrella: add `--repo <name>`, see `umbrella-mode.md`):

```bash
rota verdict add <branch> --kind review-spec --verdict <PASS|CONCERNS|FAIL> --body-file "$SPEC" --json
rota verdict add <branch> --kind review-quality --verdict <PASS|CONCERNS|FAIL> --body-file "$STANDARDS" --json
```

With no spec (Step 3, no referenced items), record only `review-quality`, and start its `summary` with `Spec axis not run: no referenced items.` so `rota verdict show` carries the gap. The same applies to a `light` depth review.

Record both even when Spec is FAIL: the Standards findings are the author's to-do list too. Order decides the result: `review-quality` stores `combined`, the worse of the two at the same sha. `data.combined` on the second call is the branch's review verdict.

Then register the Step 4 hits (parallel batch) for the entries no finding names.

Exit 2 means the block is malformed or its `verdict` differs from `--verdict`: the message names the field. Ask that reviewer to resend; never guess a verdict.

Present both reports **verbatim** (trim only restatements). Spec first, Standards second, each under its own heading, neither merged into or reranked against the other. Show each reviewer's `Declined to judge` list under its section when non-empty; it does not change the verdict and `rota verdict route` ignores it. Structure:

```
Review: `rota/foo` → main (3 commits, 5 files)

## Spec — PASS
### [F03] Quick-switch projects — PASS
Acceptance: AC-1 met (abc1234:go test ./x) · AC-2 unmet
<evidence: each spec outcome ↔ diff line(s)>

### [B07] Timer badge — CONCERN
<evidence, drift>

## Standards — CONCERNS
### 1. Convention compliance — CONCERN
- src/Foo.swift:42 — uses raw URLSession; KNOWLEDGE says all network calls go through NetworkClient

### 3. Tests — PASS
...

Verdict: CONCERNS (worse of Spec PASS and Standards CONCERNS)
```

## Step 9 — Route Based on Verdict

The verdict is the entire product: return it and stop. Never ask a follow-up; the caller owns what happens next.

From `/rota-ship`, return the verdict; the parent routes on the recorded verdict with `rota verdict route --for ship-review` (`references/review-verdict-routing.md`). Standalone, relay it per *Producer-side relay* in that reference (concerns are already printed by then).

## Queue mode (`--queue`, issue mode)

Read [`queue-mode.md`](queue-mode.md) when invoked with `--queue` (issue backend) and follow it.

## Rules

- **Read-only.** Never edit, commit, or stage. Recording with `rota verdict add` (the gitignored `.rota/verdicts.json`) is the one write.
- **Evidence over opinion.** Every concern cites file:line or commit hash.
- **Scope is bounded.** Only the diff against the base; don't wander into unchanged code.
- **Call it honestly.** If conventions were violated for a good reason, the reviewer still reports CONCERN; the user decides.
- **Don't re-run on a passed branch.** Step 7's gate skips the reviewers when the recorded review is a fresh PASS.

## References

- [`reviewer-briefs.md`](reviewer-briefs.md) — Step 7 shared block, Spec and Standards briefs, calibration rules, verdict block.
- [`re-review.md`](re-review.md) — `--since <sha>` re-review of a bounced branch.
- [`umbrella-mode.md`](umbrella-mode.md) — `--repo <name>` at Steps 2, 6, 8 in a multi-repo umbrella.
- [`file-mode-spec.md`](file-mode-spec.md) — Step 3 spec collection on the file backend.
- [`queue-mode.md`](queue-mode.md) — `--queue` review-and-merge loop (issue backend).
- [`references/knowledge-consult.md`](references/knowledge-consult.md) — canonical K+D query pattern (`rota knowledge query` + `rota decisions query`).
- [`references/review-verdict-routing.md`](references/review-verdict-routing.md) — PASS / CONCERNS / FAIL routing for `/rota-review` consumers.
