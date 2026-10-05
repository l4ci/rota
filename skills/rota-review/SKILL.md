---
name: rota-review
description: Staff-engineer review of a feature branch before merge or PR — reads commits, diff, referenced item IDs, and matching KNOWLEDGE.md topics; dispatches two parallel reviewers, Spec (intent match) and Standards (conventions, quality, tests), and reports them separately. Returns PASS / CONCERNS / FAIL, the worse of the two. Use on "review this", "check before I ship", "look over the branch", or implicitly from /rota-ship.
---

# rota-review — Pre-Merge Review

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model for the Spec reviewer (default `opus`)
- `round.tiers.claude.standard` — model for the Standards reviewer (the `standard` tier; silent default `models.worker`). No review-specific key.

## When to Use

- Before merging or opening a PR — typically invoked from `/rota-ship`
- *"Review this branch"*, *"Second-opinion this"*, *"Look over what I've got"*
- After manual commits to a branch you want validated before integrating
- `/rota-review --since <sha>` re-reviews a bounced branch: only the fix, against the findings of the last review (see Re-review)
- `/rota-review --queue` (issue backend) reviews and merges the PRs waiting on `needs-review` items (see Queue mode)

## When NOT to Use

- Code is still in flight → finish implementing via `/rota-work`
- You want to change code based on the review → `/rota-refactor` or a fresh `/rota-work` run
- Nothing committed yet → there's nothing to review
- You want product-level evidence (does it actually work? perf budgets met? a11y clean? smoke tests green?) → `/rota-qa run`. `/rota-review` reasons from commits and diff; it does not run the product.

## Step 1 — Task List

Track these phases with the host's task tool if it has one.

1. *Read commits and items* — branch range walked, referenced item IDs collected (Step 2)
2. *Resolve the spec* — what each referenced item promised (Step 3)
3. *Capture context* — KNOWLEDGE / DECISIONS topics, diff, scaffolding pre-scan (Steps 4, 5, 6)
4. *Review* — Spec and Standards reviewers in parallel (Step 7)
5. *Verdict* — spec then quality recorded, two sections reported (Step 8)
6. *Knowledge lifecycle* — register hits on consumed bullets via `rota knowledge hit` (Step 4)

## Step 2 — Scope the Review

```bash
rota review scope --json <branch>
```

**Umbrella mode.** When the branch lives in a sub-repo, pass `--repo <name>` so git ops resolve there: `rota review scope --json --repo <name> <branch>`. Determine `<name>` from `data.repo` of `rota status show <branch> --json` (the active stream's repo), or from `rota repo which` if invoked from inside the sub-repo's worktree. Backlog lookups stay umbrella-flat: `rota review scope` reads them from the umbrella's `.rota/`, so no repo flag is needed for intent matching.

If the user didn't name a branch, default to the current one. `rota review scope` returns, under `data`:

- `branch`, `base`, `commitCount`
- `commits` — array of `{hash, subject}`
- `touchedFiles` — paths changed vs base
- `referencedIds` — item IDs found in commit messages (`#N` or bracketed `[B07]`)
- `intents` — matched backlog item for each referenced ID

If `commitCount` is 0, stop and tell the user.

## Step 3 — Resolve the Spec

The spec is what each referenced item promised. Collect one entry per `referencedId`, in parallel:

- **Issue mode** (`backlog.backend: "issues"`): the issue body is the spec. `rota item field list <ID>` returns it; add `rota item comment list <ID> --kind decision`, because a decision recorded as a comment changes the body's promise. A plan note (`rota item note show <ID> --kind plan`, `exists: false` when absent) is extra detail, not a requirement, except its `## Review Focus` section: lift it out and carry it into the brief verbatim.
- **File mode**: the item's `Intent` line from `intents`, plus its plan when it has a milestone (`rota item field get <ID> --name milestone`, then `rota plan show "<MNN>-<ID>"`; exit 3 means no plan file). Lift the plan's `## Review Focus` section out the same way.

An item with no body and no plan contributes only its title. That is fine: the reviewer judges intent match from what exists, and says so when a spec is too thin to check against.

## Step 4 — Consult KNOWLEDGE & DECISIONS

Apply the canonical K+D query pattern (`references/knowledge-consult.md`) with topics that plausibly touch the changed areas based on `touchedFiles` and commit subjects — infer liberally (e.g., a file under `Networking/` → the `Networking` topic).

Carry KNOWLEDGE bullets into the reviewer brief. Pass DECISIONS entries under a `**Hard boundaries:**` section — the reviewer must **FAIL** if the diff violates any boundary, even if the change looks otherwise good.

> **REQUIRED — Register hits on consumed bullets (F03 lifecycle).** After building the reviewer brief, apply the hit-register pattern from `references/knowledge-consult.md` *Hit-register after consumption*: for each bullet that landed in the brief's `**Relevant project conventions (from KNOWLEDGE.md):**` section, call `rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"` once, issuing all calls as a single parallel batch. Bullets returned but pruned before the brief don't earn credit. Silent on success. Provisional bullets auto-promote to confirmed once `hits >= learn.promoteThreshold` (default 3).

## Step 5 — Capture the Diff

The reviewer needs concrete diff content, not just file names. Write it to a file rather than into your own context:

```bash
rota review package <branch> --base <base> [--since <sha>] --json
```

`data.path` is the file (commits, `--stat` and the full `-U10` diff); `data.files` and `data.bytes` size it. There is no file cap: the reviewer reads the file, you don't. Pass `--since <sha>` on a re-review to package only the commits after the sha the last review covered. Exit 3 means an empty range or a `--since` that is not on the branch: report it and stop.

### Re-review (`--since <sha>`)

After a bounce the worker pushes a fix. Review that fix, not the whole branch again. `<sha>` is the `sha` of the branch's last recorded review (`rota verdict show <branch> --json`, the newest record of kind `review-quality`); without a recorded review there is nothing to re-review against, so run the full review. Do these in place of the full Step 7 brief:

- Step 5 packages only `--since <sha>`. Steps 2-4 and 6 still run, on the fix range.
- Both reviewers run, each on its own axis. `rota verdict show <branch> --json` holds the latest `review-spec` and `review-quality` records: put each record's `findings` into its reviewer's brief as `**Earlier findings:**`, numbered. `<sha>` is the `review-quality` record's sha.
- Each reviewer marks every earlier finding `ADDRESSED` or `NOT ADDRESSED`, with the diff line that settles it. A finding the fix does not touch is `NOT ADDRESSED`.
- Only the fix gets the rubric. Anything the reviewer notices outside the fix goes to a `**Deferred:**` list in the report: it is never a finding and never moves the verdict.
- The verdict block carries each `NOT ADDRESSED` finding again, plus any new finding the fix itself introduced. `ADDRESSED` ones appear only in `summary`, as a count. `PASS` needs every earlier finding `ADDRESSED` and no new finding in the fix.
- Relay the `**Deferred:**` list to the caller beside the verdict. File it with `/rota-capture` if the caller wants; never fold it into this branch.

## Step 6 — Pre-flight Scaffolding Scan

Multi-task feature branches sometimes ship comments that referenced earlier task numbers ("Umbrella behavior is added in Task 7 — for now --repo is parsed but ignored") even after the referenced task completed. Before dispatching the reviewer, run a deterministic diff scan:

```bash
rota review scaffolding [--repo <name>] --base <base> <branch>
```

Empty stdout (no `data.findings`) → no candidates. Otherwise carry the matches into the brief as `**Possible stale scaffolding:**` evidence. Do not auto-FAIL — the reviewer judges each match as real scaffolding or legitimate prose. The verb surfaces; the reviewer decides.

## Step 7 — Dispatch the Reviewers

Dispatch two reviewers **in parallel** (one message, two dispatches), fresh context each, same diff file and same Steps 2-6 context. Two axes, judged apart, so one cannot mask the other:

- **Spec** — **orchestrator** model. Does the diff do what the items promised, and nothing more?
- **Standards** — **`standard`** tier. Does it meet the project's standards?

Fill the bracketed parts from Steps 2-6 and drop any section that has nothing in it. Both briefs open with the shared block, then add their own rubric.

```
Review `<branch>` against base `<base>` on one axis (named below).

**Commits:**
<hash> <subject>
...

**Items being resolved, with their spec:**

### [F03] Quick-switch projects
<issue body, decision comments and plan from Step 3, or the intent line>

### [B07] Timer badge shows stale duration
<same>

**Diff:** read `<data.path from Step 5>` (commits, `--stat`, full diff with context). Do not ask for the diff to be pasted.
```

### Spec reviewer brief

Shared block, plus these sections (items and Review Focus go here; the Standards reviewer does not get them beyond the shared block):

```
**Axis: Spec.** Does the diff deliver what the items promised, nothing more? Do not judge style, conventions or test quality: another reviewer does.

**Review Focus (from the plan, verbatim):**
<the plan's `## Review Focus` lines per item, or drop this section>
Check each named edge is handled and pinned by a test; an unhandled or untested one is a CONCERN.

**Recorded proof:**
<rows from `rota proof show <ID>` per item, or "none recorded">
Rows are verification already run (check, result, sha, evidence). Do NOT re-run a check that has a PASS row at the current sha; spot-check one row. A FAIL row or a row at a stale sha is a gap to cite.

**Rubric. For each item, return PASS / CONCERN / FAIL with evidence.**

1. **Intent match** — does the diff deliver every outcome in the item's spec? PASS cites the diff line for each outcome. A partially met outcome (stub, missing test for a named outcome) is a CONCERN. A missing outcome, or edits to files the spec does not imply, is a FAIL. If an item's spec is too thin to check, say so rather than inventing one.
2. **Drift** — trace each change back to the item's intent. Sensible steps nobody asked for (extra options, generalised helpers, adjacent cleanups) are a CONCERN naming the drift path, never a FAIL on their own.
```

### Standards reviewer brief

Shared block, plus:

```
**Axis: Standards.** Does the diff meet the project's standards? Do not judge whether it matches the items' intent: another reviewer does.

**Relevant project conventions (from KNOWLEDGE.md):**
- <bullet>

**Hard boundaries (from DECISIONS.md):**
<entries from `rota decisions query`, full rule + forbids/permits>

**Possible stale scaffolding (deterministic pre-flight grep):**
<file:line>: <matched line text>

**Rubric. Return PASS / CONCERN / FAIL per heading, with evidence.**

1. **Convention compliance** — does the diff respect the KNOWLEDGE.md bullets? Any regression on a captured gotcha?
2. **Code-smell baseline** — dead code, error swallowing, security smells, API contract breaks, performance cliffs, untested new branches. A short baseline, not a full code review; focus on what the user would regret after merge. A documented project standard (a KNOWLEDGE bullet, a DECISIONS entry, the repo's own idiom) overrides the baseline: code that follows it is never a smell.
3. **Tests** — flag a **tautological** test (the expected value is computed the way the code computes it, so it cannot disagree with the code) and an **implementation-coupled** test (pinned to internals or call order instead of behaviour, so a correct refactor breaks it). Each is a CONCERN with file:line.
4. **Stale scaffolding** — judge each `**Possible stale scaffolding:**` match: a leftover *Task N* / *placeholder* / *not yet wired* / *added later* / *in flight* annotation that should have gone once the work landed is a CONCERN with file:line; legitimate prose (a markdown placeholder section, a docstring describing user-visible "in flight" semantics, an enum value named `placeholder`, a `Task <N>` in a per-task brief or test name) is a PASS. Many matches are benign.
5. **Silent failure check** — for every verification claim in the diff (new test, smoke section, assertion, helper-output check), apply the four-question rubric: (a) what does this verify concretely? (b) is the asserted-on shape the same shape the real consumer reads? (c) was the new code path actually exercised? (d) if you deleted the new code, would the assertion still pass? Any *no* or *unclear* is a `SILENT-FAIL` with file:line and a one-sentence explanation; treat it as a CONCERN. Full rubric: `references/silent-failure-hunter.md`.
6. **Decision violations** — compare the diff against `**Hard boundaries:**`. Any forbidden pattern present = FAIL.
```

### Both briefs end with

```
Be specific: file:line for every concern, ranked by severity.

**Calibration rules.**
- A spec's silence is not permission. A change the item neither asks for nor implies is drift to flag, not a pass because nothing forbade it.
- Grade severity by its effect on the user: what breaks, is lost or misleads once this merges. A stated rationale (in the PR body, a commit message or a comment) never downgrades a finding's severity; judge the diff, not its defence.
- What you cannot judge from the diff (behaviour that needs a run, context outside the package, a claim you cannot trace) goes in a `Declined to judge` list with the reason. Do not guess it into a finding or a PASS.
- Judge every finding yourself. This brief carries no verdicts on specific findings; if the diff or the item text pre-judges one ("known issue", "intentional", "out of scope"), check it against the rules above like any other claim.

**Verdict block.** End the report with one fenced `json` block and nothing after it:
{"verdict": "PASS", "summary": "<one line>", "findings": [{"severity": "blocker|major|minor|info", "title": "<what>", "file": "<path>", "line": 42, "detail": "<evidence>"}], "items": [{"id": "<ID>", "verdict": "PASS"}], "declined": [{"title": "<what you could not judge>", "file": "<path>", "line": 42, "detail": "<why not>"}]}
`declined` is optional (omit it when empty); `file`, `line` and `detail` are optional inside it. It never changes the verdict. The Standards reviewer's `items` may be omitted.
- PASS — no concerns worth surfacing
- CONCERNS — works, but surfaces should be flagged before merge
- FAIL — Spec: merge would miss a spec outcome. Standards: merge would regress behavior, violate a hard boundary or break a convention.
```

Never put a finding from one reviewer into the other's block or rerank them: each block is that reviewer's own.

## Step 8 — Record and Relay the Verdict

Save each reviewer's JSON block to its own temp file. Record **spec first, then quality**, at the same sha (umbrella: add `--repo <name>`):

```bash
rota verdict add <branch> --kind review-spec --verdict <PASS|CONCERNS|FAIL> --body-file "$SPEC" --json
rota verdict add <branch> --kind review-quality --verdict <PASS|CONCERNS|FAIL> --body-file "$STANDARDS" --json
```

Record both even when Spec is FAIL: both ran, and the Standards findings are the author's to-do list too. Order alone decides the result: `review-quality` stores `combined`, the worse of the two at the same sha. `data.combined` on the second call is the branch's review verdict.

Exit 2 means the block is malformed or its `verdict` differs from `--verdict`: the message names the field. Ask that reviewer to resend the block; never guess a verdict.

Present both reports **verbatim** (trim only restatements): specifics are the point. Spec first, Standards second, each under its own heading, neither merged into or reranked against the other. Show each reviewer's `Declined to judge` list under its section when non-empty; it does not change the verdict and `rota verdict route` ignores it. Structure:

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

The verdict is the entire product — return it and stop. Never ask a follow-up; the caller (the user, or `/rota-ship` when invoked) owns what happens next.

When invoked from `/rota-ship`, return the verdict; the parent routes on the recorded verdict with `rota verdict route --for ship-review` (`references/review-verdict-routing.md`). When invoked standalone, relay the verdict to the user using the *Producer-side relay* table in the reference — short summary:

- **PASS** — *"Ready to ship. Run `/rota-ship`."*
- **CONCERNS** — the concerns are already printed; suggest *"Address via `/rota-work` and rerun `/rota-review`, or accept and ship via `/rota-ship`."*
- **FAIL** — tell the user the merge would regress. Suggest fixing via `/rota-work` or `/rota-debug`. Don't route to `/rota-ship`.

## Queue mode (`--queue`, issue mode)

Works through every `needs-review` item's open PR / MR. Issue mode only (`backlog.backend: "issues"`; see `references/issue-mode.md` for the label lifecycle). In file mode say *"`--queue` needs the issue backend; use `/rota-ship` and `rota ship merge` here"* and stop.

**Who merges.** Whoever runs the queue owns the merge: you, outside a round. Inside a round the orchestrator merges through `rota worker gate` (rota-orchestrate section 6) and workers never run `--queue`, so don't run it against round PRs.

```bash
rota review queue --json
```

`data.items` is `[{"id","number","title","prs":[{"number","title","branch","url","body"}]}]`. Empty: report *"Review queue is empty"* and stop. Per entry, in order:

1. **PRs.** None: report *"<ID> is `needs-review` but has no PR with a closing keyword"* and skip. Several: review each.
2. **Checkout.** `git status --short` must be clean, else stop. Check the PR out through the adapter: `rota tracker call -- pr checkout <n>` (GitHub) or `-- mr checkout <n>` (GitLab).
3. **Review.** Skip this stage when `rota proof show <ID> --json` already holds a PASS at the PR's current head sha (`git rev-parse HEAD`): the merge gate that follows is the only full run, so don't repeat verification here, and go to Route as a PASS. Otherwise run Steps 2-8 on the checked-out branch, scoped to `<base>...HEAD` (`<base>` from `rota git base`). The reviewer is read-only (it runs no suite); so is the queue, apart from the verbs below.
4. **Route** on `rota verdict route <branch> --for queue --json`, field `data.next`:
   - **`ask`** (PASS) — `AskUserQuestion` (Header `"Merge"`, *"Merge PR <n> for <ID>?"*, options *Merge (Recommended)* / *Skip* / *Stop*). Merge with `rota ship pr-merge <n>` (in an umbrella `--repo <name>` is required; queue entries carry `repo` and qualified IDs): it merges and closes the linked items the host left open; `data.sha` and `data.closed` report the result. Pass the Merge answer along: `--confirm --confirm-note "<answer>"` (ignored unless `ship.mergeApproval` covers the PR). Exit 4 with `data.unproven` means nothing was merged because an item has no proof: it is now `changes-requested`; report it and move on. Exit 4 with `data.blockedBy: "verdict"` means the PR's branch has a recorded review or second-opinion FAIL: nothing changed; report it and move on. Exit 4 with `data.blockedBy: "manual gate"` is the `merge-approval` gate (`ship.mergeApproval` requires a human for this merge; `data.paths` names the files that put it there): nothing changed. Ask the user in an `AskUserQuestion`, then re-run with `--confirm --confirm-note "<their answer>"`. Then post the verdict on each linked item (`rota item comment add <ID> --kind feedback --body-file -`) and on the PR (`rota tracker call -- pr comment <n> --body-file -` on GitHub, `-- mr note <n> --message "<verdict>"` on GitLab).
   - **`request-changes`** (CONCERNS / FAIL) — post the findings as a `feedback` comment on each linked item and on the PR (same commands), then `rota item state <ID> --to changes-requested`. The author's next `/rota-work` claim reads the feedback. No merge, under any autonomy level.
5. **Return.** `git checkout <base>` before the next entry.

Exit 5 or 6 (tracker unavailable or rate-limited) from any verb stops the queue with a report of what was done and what is left. Never retry. Routing table: `references/review-verdict-routing.md` (Queue routing).

## Rules

- **Read-only.** Never edit, commit, or stage. The verdict is the entire product; recording it with `rota verdict add` (the gitignored `.rota/verdicts.json`) is the one write.
- **Evidence over opinion.** Every concern must cite file:line or commit hash.
- **Scope is bounded.** Only the diff against the base is reviewed — don't wander into unchanged code.
- **Call it honestly.** If conventions were violated but the user has a good reason, the reviewer still reports CONCERN — the user decides what to do.
- **Don't re-run on a passed branch.** If `rota verdict show <branch> --json` has a review record with `verdict` PASS and `stale: false`, skip Step 7 and report that verdict.

## References

- [`references/knowledge-consult.md`](references/knowledge-consult.md) — Canonical K+D query pattern (`rota knowledge query` + `rota decisions query`) used by every cycle-starting skill.
- [`references/review-verdict-routing.md`](references/review-verdict-routing.md) — PASS / CONCERNS / FAIL routing for `/rota-review` consumers.
