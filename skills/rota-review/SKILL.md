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

- **Issue mode** (`backlog.backend: "issues"`, the default path): the issue body is the spec. `rota item field list <ID>` returns it; add `rota item comment list <ID> --kind decision`, because a decision recorded as a comment changes the body's promise. A plan note (`rota item note show <ID> --kind plan`, `exists: false` when absent) is extra detail, not a requirement, except its `## Review Focus` section: lift it out and carry it into the brief verbatim.

When `backlog.backend` is `"file"`, read [`file-mode-spec.md`](file-mode-spec.md) instead.

An item with no body and no plan contributes only its title; the reviewer says so when a spec is too thin to check against.

## Step 4 — Consult KNOWLEDGE & DECISIONS

Apply the canonical K+D query pattern (`references/knowledge-consult.md`) with topics that plausibly touch the changed areas, from `touchedFiles` and commit subjects. Infer liberally (a file under `Networking/` → the `Networking` topic).

Carry KNOWLEDGE bullets into the reviewer brief. Pass DECISIONS entries under a `**Hard boundaries:**` section; the reviewer must **FAIL** if the diff violates any boundary, even if the change looks otherwise good.

> **REQUIRED — Register hits on consumed bullets.** After building the reviewer brief, apply the hit-register pattern from `references/knowledge-consult.md` *Hit-register after consumption*: for each bullet that landed in the brief's `**Relevant project conventions (from KNOWLEDGE.md):**` section, call `rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"` once, issuing all calls as a single parallel batch. Bullets pruned before the brief don't earn credit. Silent on success. Provisional bullets auto-promote to confirmed once `hits >= learn.promoteThreshold` (default 3).

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

Dispatch two reviewers **in parallel** (one message, two dispatches), fresh context each, same diff file and same Steps 2-6 context. Two axes, judged apart, so one cannot mask the other:

- **Spec** — **orchestrator** model. Does the diff do what the items promised, and nothing more?
- **Standards** — **`standard`** tier. Does it meet the project's standards?

Fill the bracketed parts from Steps 2-6 and drop any empty section. Read [`reviewer-briefs.md`](reviewer-briefs.md) for the shared block, the Spec and Standards briefs, and the closing calibration and verdict-block text; build both briefs from it.

Never put a finding from one reviewer into the other's block or rerank them.

## Step 8 — Record and Relay the Verdict

Save each reviewer's JSON block to its own temp file. Record **spec first, then quality**, at the same sha (umbrella: add `--repo <name>`, see `umbrella-mode.md`):

```bash
rota verdict add <branch> --kind review-spec --verdict <PASS|CONCERNS|FAIL> --body-file "$SPEC" --json
rota verdict add <branch> --kind review-quality --verdict <PASS|CONCERNS|FAIL> --body-file "$STANDARDS" --json
```

Record both even when Spec is FAIL: the Standards findings are the author's to-do list too. Order decides the result: `review-quality` stores `combined`, the worse of the two at the same sha. `data.combined` on the second call is the branch's review verdict.

Exit 2 means the block is malformed or its `verdict` differs from `--verdict`: the message names the field. Ask that reviewer to resend; never guess a verdict.

Present both reports **verbatim** (trim only restatements). Spec first, Standards second, each under its own heading, neither merged into or reranked against the other. Show each reviewer's `Declined to judge` list under its section when non-empty; it does not change the verdict and `rota verdict route` ignores it. Structure:

```
Review: `rota/foo` → main (3 commits, 5 files)

## Spec — PASS
### [F03] Quick-switch projects — PASS
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

From `/rota-ship`, return the verdict; the parent routes on the recorded verdict with `rota verdict route --for ship-review` (`references/review-verdict-routing.md`). Standalone, relay it using the *Producer-side relay* table in that reference:

- **PASS** — *"Ready to ship. Run `/rota-ship`."*
- **CONCERNS** — concerns are already printed; suggest *"Address via `/rota-work` and rerun `/rota-review`, or accept and ship via `/rota-ship`."*
- **FAIL** — the merge would regress. Suggest `/rota-work` or `/rota-debug`. Don't route to `/rota-ship`.

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
