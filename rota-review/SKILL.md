---
name: rota-review
description: Staff-engineer review of a feature branch before merge or PR — reads commits, diff, referenced item IDs, and matching KNOWLEDGE.md topics; dispatches an Opus reviewer that checks intent match, convention compliance, and quality. Returns PASS / CONCERNS / FAIL. Use on "review this", "check before I ship", "look over the branch", or implicitly from /rota-ship.
---

# rota-review — Pre-Merge Review

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model for the reviewer (default `opus`)

## When to Use

- Before merging or opening a PR — typically invoked from `/rota-ship`
- *"Review this branch"*, *"Second-opinion this"*, *"Look over what I've got"*
- After manual commits to a branch you want validated before integrating
- Issue mode: `/rota-review --queue` reviews and merges the PRs waiting on `needs-review` items (see Queue mode)

## When NOT to Use

- Code is still in flight → finish implementing via `/rota-work`
- You want to change code based on the review → `/rota-refactor` or a fresh `/rota-work` run
- Nothing committed yet → there's nothing to review
- You want product-level evidence (does it actually work? perf budgets met? a11y clean? smoke tests green?) → `/rota-qa run`. `/rota-review` reasons from commits and diff; it does not run the product.

## Step 1 — Task List

Track these phases with the host's task tool if it has one.

Phases:

1. *Read commits and items* — branch range walked, referenced item IDs collected (Step 2)
2. *Resolve plans* — milestone-keyed `.rota/plans/<milestone>-<ID>.md` located for each referenced item (Step 3)
3. *Capture context* — diff, KNOWLEDGE / DECISIONS topics, scaffolding pre-scan (Steps 4, 5, 6)
4. *Stage 1 — Spec compliance* — diff evaluated against `PLAN.md` outcomes; short-circuit on FAIL (Step 7)
5. *Stage 2 — Code quality* — staff-engineer review, gated on Stage 1 PASS or CONCERNS (Step 8)
6. *Verdict* — combined PASS / CONCERNS / FAIL with structured findings (Step 9)
7. *Knowledge lifecycle* — register hits on consumed bullets via `rota knowledge hit` (Step 4)

**Stage opt-out (power users).** When invoked with `--stage spec`, run only Stage 1 (Steps 2, 3, 5, 7) and skip Step 8. When invoked with `--stage quality`, skip Steps 3 and 7 and run only Stage 2 — the legacy single-pass behavior. No `--stage` arg = run both stages with short-circuit gating (the default).

## Step 2 — Scope the Review

```bash
rota review scope --json <branch>
```

**Umbrella mode.** When the branch lives in a sub-repo, pass `--repo <name>` so git ops resolve there: `rota review scope --json --repo <name> <branch>`. Determine `<name>` from `data.repo` of `rota status show <branch> --json` (the active stream's repo), or from `rota repo which` if invoked from inside the sub-repo's worktree. `BACKLOG.md` / `ARCHIVE.md` lookups stay umbrella-flat — `rota review scope` reads them from the umbrella's `.rota/`, so no repo flag is needed for intent matching.

If the user didn't name a branch, default to the current one. `rota review scope` returns, under `data`:

- `branch`, `base`, `commitCount`
- `commits` — array of `{hash, subject}`
- `touchedFiles` — paths changed vs base
- `referencedIds` — `[B##]`/`[F##]`/`[T##]` found in commit messages
- `intents` — matched TODO entries for each referenced ID

If `commitCount` is 0, stop and tell the user.

## Step 3 — Resolve Plans for Referenced Items

For each `intent` in the scope JSON's `intents` array, resolve its milestone-keyed plan if one exists. The plan content is Stage 1's input alongside the diff.

```bash
# For each <ID> in referencedIds:
rota item field get <ID> --name milestone     # data.value; empty = untagged
rota plan show "<MNN>-<ID>"                    # only when tagged; exit 3 = no plan file
```

Issue the `rota item field get` and `rota plan show` calls in parallel — one pair per `referencedId` — and collect a `plans` map: `{ID -> plan-content-or-empty}`. Untagged items (no `Milestone:` field) and items with no plan file produce empty entries — those items don't contribute to Stage 1.

**No-plan fallback.** If every entry in `plans` is empty (no referenced item has a plan file), Stage 1 cannot run as a meaningful spec check. Print one informational line — *"No plans found for referenced items; skipping Stage 1 (spec compliance). Running Stage 2 only."* — and proceed directly to Step 4 (effectively `--stage quality` behavior). The user may have skipped `/rota-plan` for this branch (e.g. a one-shot `/rota-capture` hand-off); that's legitimate, not an error.

When `--stage quality` is set, skip this step entirely — Stage 1 won't run.

## Step 4 — Consult KNOWLEDGE & DECISIONS

Apply the canonical K+D query pattern (`references/knowledge-consult.md`) with topics that plausibly touch the changed areas based on `touchedFiles` and commit subjects — infer liberally (e.g., a file under `Networking/` → the `Networking` topic).

Carry KNOWLEDGE bullets into the reviewer brief. Pass DECISIONS entries under a `**Hard boundaries:**` section — the reviewer must **FAIL** if the diff violates any boundary, even if the change looks otherwise good.

> **REQUIRED — Register hits on consumed bullets (F03 lifecycle).** After building the reviewer brief, apply the hit-register pattern from `references/knowledge-consult.md` *Hit-register after consumption*: for each bullet that landed in the brief's `**Relevant project conventions (from KNOWLEDGE.md):**` section, call `rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"` once, issuing all calls as a single parallel batch. Bullets returned but pruned before the brief don't earn credit. Silent on success. Provisional bullets auto-promote to confirmed once `hits >= learn.promoteThreshold` (default 3).

## Step 5 — Capture the Diff

The reviewer needs concrete diff content, not just file names. For each touched file (up to 8; with 9 or more, ask the user which to focus on):

```bash
git diff <base>...<branch> -- <file>
```

**Issue all the per-file `git diff` calls in parallel** — they're independent and serial calls add up fast on bigger branches. Keep a per-file diff map in memory for the reviewer brief.

## Step 6 — Pre-flight Scaffolding Scan

Multi-task feature branches sometimes ship comments that referenced earlier task numbers ("Umbrella behavior is added in Task 7 — for now --repo is parsed but ignored") even after the referenced task completed. Before dispatching the reviewer, run a deterministic diff scan:

```bash
rota review scaffolding [--repo <name>] --base <base> <branch>
```

Empty stdout (no `data.findings`) → no candidates, skip ahead to Step 7. Otherwise carry the matches forward as `**Possible stale scaffolding:**` evidence in the Stage 2 reviewer brief (Step 8). Do not auto-FAIL — the reviewer judges each match as real scaffolding or legitimate prose. The verb surfaces; the reviewer decides.

## Step 7 — Stage 1: Dispatch Spec-Compliance Reviewer

Skip this step entirely when `--stage quality` is set, OR when Step 3's `plans` map is empty (no-plan fallback already printed).

Dispatch a focused spec-compliance reviewer using the **orchestrator** model. Stage 1 has a narrow input: the diff + the resolved `PLAN.md` content per referenced item. KNOWLEDGE / DECISIONS / scaffolding stay out of this brief — Stage 1 answers *"does the diff fulfill what the plan promised?"* and nothing else. Shorter brief, smaller token budget, faster verdict.

Brief template:

```
Stage 1 / 2 — spec compliance review of `<branch>` against base `<base>`.

You evaluate ONE question: does the diff fulfill the outcomes promised by the plan(s)?

You do NOT evaluate code quality, style, conventions, security, or scaffolding —
that's Stage 2's job. Even if you notice issues there, do NOT flag them.

**Commits:**
<hash> <subject>
<hash> <subject>
...

**Items being resolved (with plans):**

### [F03] Quick-switch projects
**Intent (from BACKLOG.md):** "<full intent line>"

**Plan outcomes (from .rota/plans/M01-F03.md):**
<plan content>

### [B07] Timer badge shows stale duration
**Intent:** "<full intent line>"

**Plan outcomes (from .rota/plans/M01-B07.md):**
<plan content>

(Omit a plan section for items without plans — note them as "no plan; skipped from spec check.")

**Recorded proof:**
<rows from `rota proof show <ID>` per item, or "none recorded">
Rows are verification already run (check, result, sha, evidence). Do NOT re-run a check that has a PASS row at the current sha; spot-check one row. A FAIL row or a row at a stale sha is a gap to cite.

**Diff by file:**
<file>
```diff
<diff content>
```
...

**Evaluate per item:**
For each item with a plan, return PASS / CONCERN / FAIL with evidence:
- **PASS** — every outcome in the plan is fulfilled by the diff. Cite the diff line(s) for each.
- **CONCERN** — most outcomes fulfilled, but one or more partially met (stub, incomplete coverage, missing test for a named outcome). Cite the gap.
- **FAIL** — at least one plan outcome is missing entirely OR the diff went off-target (touched files not implied by the plan, scope creep into unrelated areas). Cite the missed outcome AND the off-target evidence.

**Refocus check (per item, after the PASS/CONCERN/FAIL call):**
1. Trace each change back up the chain: plan task → backlog item intent → milestone intent (only where the item carries a `Milestone:` tag; otherwise stop at the item). Use only the intent text already in this brief.
2. Flag scope inflation: steps that are sensible on their own but drift from the parent intent (extra options, generalised helpers, adjacent cleanups no level of the chain asks for). Report it as CONCERN, naming the drift path (e.g. `task 3 → [F03] → M01: adds a project-sync mode neither asks for`).
3. Drift alone is never a Stage 1 FAIL. Off-target edits to files the plan doesn't imply stay under FAIL above.

**Verdict block.** End the report with one fenced `json` block and nothing after it. Stage verdict is `PASS`, `CONCERNS` or `FAIL`:
{"verdict": "PASS", "summary": "<one line>", "findings": [{"severity": "blocker|major|minor|info", "title": "<what>", "file": "<path>", "line": 42, "detail": "<evidence>"}], "items": [{"id": "<ID>", "verdict": "PASS"}]}
- PASS — every plan-bearing item delivered what its plan promised
- CONCERNS — works, but plan-vs-diff gaps surfaced
- FAIL — at least one plan outcome went un-fulfilled or the diff went off-target
```

**Record the verdict.** Save the reviewer's JSON block to a temp file and record it (umbrella: add `--repo <name>`):

```bash
rota verdict add <branch> --kind review-spec --verdict <PASS|CONCERNS|FAIL> --body-file "$VERDICT" --json
```

Exit 2 means the block is malformed or its `verdict` differs from `--verdict`: the message names the field. Ask the reviewer to resend the block; never guess a verdict. Route on `data.next`:

| `data.next` | Route |
|---------|-------|
| `quality` | Continue to Step 8 (Stage 2). Carry the per-item evidence forward into the final report. Stage 1 concerns alone don't decide the merge. |
| `report` | **Short-circuit** (Stage 1 FAIL). Skip Step 8 and jump to Step 9 to emit a Stage-1-only verdict block. No point burning a deeper review on a diff that doesn't match intent. |

When `--stage spec` is set, always jump to Step 9 after Stage 1 (no Stage 2 regardless of verdict).

## Step 8 — Stage 2: Dispatch Code-Quality Reviewer

Skip this step entirely when `--stage spec` is set, OR when Stage 1 routed to `report` (FAIL short-circuit in Step 7).

Dispatch a single code-quality reviewer using the **orchestrator** model. Stage 2 owns code quality, conventions, stale scaffolding, and silent-failure detection. Intent / spec compliance lives in Stage 1 and is NOT re-evaluated here — the brief tells the reviewer to skip it.

**Use Variant A when Stage 1 ran (PASS or CONCERNS). Use Variant B when Stage 1 was skipped (no-plan fallback or `--stage quality`).**

### Variant A — Stage 2 with Stage 1 (Stage 1 ran with PASS or CONCERNS)

```
Stage 2 / 2 — code-quality review of `<branch>` against base `<base>`.

Stage 1 (spec compliance) already evaluated whether the diff fulfills the
promised plan outcomes — DO NOT re-evaluate intent here. Stage 1's verdict
is provided for context but is not your concern. Focus on quality:
conventions, edge cases, security smells, performance cliffs, stale
scaffolding, silent failures.

**Stage 1 verdict (context — do not re-evaluate):**
<PASS or CONCERNS — paste the Stage 1 evidence block verbatim>

**Commits:**
<hash> <subject>
<hash> <subject>
...

**Items being resolved:**
- [B07] Timer badge shows stale duration — "<full intent line from TODO>"
- [F03] Quick-switch projects — "<full intent line from TODO>"

**Relevant project conventions (from KNOWLEDGE.md):**
- <bullet 1>
- <bullet 2>

**Hard boundaries (from DECISIONS.md):**
<entries from `rota decisions query`, if any — full rule + forbids/permits>

**Possible stale scaffolding (deterministic pre-flight grep):**
<file:line>: <matched line text>
<file:line>: <matched line text>
...

(Omit this section entirely when Step 6 produced no matches.)

**Diff by file:**
<file>
```diff
<diff content>
```
...

**Evaluate on the rubric below. For each item, return PASS / CONCERN / FAIL with evidence.**

1. **Convention compliance** — does the diff respect the bullets from KNOWLEDGE.md? Any regressions on captured gotchas?
2. **Obvious quality** — dead code, error swallowing, untested new branches, security smells, API contract breaks, performance cliffs. Not a full code review; focus on things the user would regret after merge.
3. **Stale scaffolding** — for each entry in `**Possible stale scaffolding:**`, judge whether the matched line is a leftover *Task N* / *placeholder* / *not yet wired* / *added later* / *in flight* annotation that should have been removed once the corresponding work landed. Flag as CONCERN with the file:line if it reads like leftover scaffolding; PASS-and-skip if it's legitimate prose (e.g., a markdown placeholder section, a docstring describing user-visible "in flight" semantics, an enum value named `placeholder`, or a `Task <N>` mention in a per-task brief or test name). Many matches will be benign — the helper surfaces candidates, not verdicts.
4. **Silent failure check** — for every verification claim in the diff (new test, smoke section, assertion, helper-output check), apply the four-question rubric: (a) what does this verify concretely? (b) is the asserted-on shape the same shape the real consumer reads? (c) was the new code path actually exercised? (d) if you deleted the new code, would the assertion still pass? If any answer is *no* or *unclear*, flag the claim as `SILENT-FAIL` with file:line and a one-sentence explanation. Treat `SILENT-FAIL` flags as CONCERNS in the verdict — they don't break the build alone, but the user sees them before merging. Full rubric and patterns in `references/silent-failure-hunter.md`.
- **Decision violations.** Compare the diff against the `**Hard boundaries:**` block above. Any forbidden pattern present in the diff = FAIL.

Return verdict as labeled sections. Be specific: file:line for every concern. Rank concerns by severity.

**Verdict block.** End the report with one fenced `json` block and nothing after it. Stage verdict is `PASS`, `CONCERNS` or `FAIL`:
{"verdict": "PASS", "summary": "<one line>", "findings": [{"severity": "blocker|major|minor|info", "title": "<what>", "file": "<path>", "line": 42, "detail": "<evidence>"}]}
- PASS — no concerns worth surfacing
- CONCERNS — works, but surfaces should be flagged before merge
- FAIL — merge would regress behavior, violate a hard boundary, or break a convention
```

### Variant B — Stage 2 standalone (Stage 1 was skipped via no-plan fallback or `--stage quality`)

```
Stage 2 / 2 — code-quality review of `<branch>` against base `<base>`.

Stage 1 (spec compliance) was skipped for this review. Evaluate intent
match as the first rubric item, then assess quality: conventions, edge
cases, security smells, performance cliffs, stale scaffolding, silent
failures.

**Commits:**
<hash> <subject>
<hash> <subject>
...

**Items being resolved:**
- [B07] Timer badge shows stale duration — "<full intent line from TODO>"
- [F03] Quick-switch projects — "<full intent line from TODO>"

**Relevant project conventions (from KNOWLEDGE.md):**
- <bullet 1>
- <bullet 2>

**Hard boundaries (from DECISIONS.md):**
<entries from `rota decisions query`, if any — full rule + forbids/permits>

**Possible stale scaffolding (deterministic pre-flight grep):**
<file:line>: <matched line text>
<file:line>: <matched line text>
...

(Omit this section entirely when Step 6 produced no matches.)

**Diff by file:**
<file>
```diff
<diff content>
```
...

**Evaluate on the rubric below. For each item, return PASS / CONCERN / FAIL with evidence.**

1. **Intent match** — does the diff deliver what the TODO entries promise? Anything missing, anything scope-creeping?
2. **Convention compliance** — does the diff respect the bullets from KNOWLEDGE.md? Any regressions on captured gotchas?
3. **Obvious quality** — dead code, error swallowing, untested new branches, security smells, API contract breaks, performance cliffs. Not a full code review; focus on things the user would regret after merge.
4. **Stale scaffolding** — for each entry in `**Possible stale scaffolding:**`, judge whether the matched line is a leftover *Task N* / *placeholder* / *not yet wired* / *added later* / *in flight* annotation that should have been removed once the corresponding work landed. Flag as CONCERN with the file:line if it reads like leftover scaffolding; PASS-and-skip if it's legitimate prose (e.g., a markdown placeholder section, a docstring describing user-visible "in flight" semantics, an enum value named `placeholder`, or a `Task <N>` mention in a per-task brief or test name). Many matches will be benign — the helper surfaces candidates, not verdicts.
5. **Silent failure check** — for every verification claim in the diff (new test, smoke section, assertion, helper-output check), apply the four-question rubric: (a) what does this verify concretely? (b) is the asserted-on shape the same shape the real consumer reads? (c) was the new code path actually exercised? (d) if you deleted the new code, would the assertion still pass? If any answer is *no* or *unclear*, flag the claim as `SILENT-FAIL` with file:line and a one-sentence explanation. Treat `SILENT-FAIL` flags as CONCERNS in the verdict — they don't break the build alone, but the user sees them before merging. Full rubric and patterns in `references/silent-failure-hunter.md`.
- **Decision violations.** Compare the diff against the `**Hard boundaries:**` block above. Any forbidden pattern present in the diff = FAIL.

Return verdict as labeled sections. Be specific: file:line for every concern. Rank concerns by severity.

**Verdict block.** End the report with one fenced `json` block and nothing after it. Stage verdict is `PASS`, `CONCERNS` or `FAIL`:
{"verdict": "PASS", "summary": "<one line>", "findings": [{"severity": "blocker|major|minor|info", "title": "<what>", "file": "<path>", "line": 42, "detail": "<evidence>"}]}
- PASS — no concerns worth surfacing
- CONCERNS — works, but surfaces should be flagged before merge
- FAIL — merge would regress behavior, violate a hard boundary, or break a convention
```

## Step 9 — Combine Verdicts and Relay

When Stage 2 ran, record its block the same way as Step 7:

```bash
rota verdict add <branch> --kind review-quality --verdict <PASS|CONCERNS|FAIL> --body-file "$VERDICT" --json
```

The combined verdict is `data.combined`: the verb takes the worst of the two stages (FAIL beats CONCERNS beats PASS) when Stage 1 ran at the same commit, else Stage 2's own. When only Stage 1 ran (FAIL short-circuit or `--stage spec`), the Step 7 verdict is the combined one. Never work the combination out by hand.

Present both stages' outputs **verbatim** (or nearly so — trim only restatements). Stage 1 first, then Stage 2, then the combined verdict. Don't summarize away the evidence; specifics are the point. When Stage 2 was short-circuited, mark it `Skipped (Stage 1 returned FAIL)` in the output block.

Structure:

```
Review: `rota/foo` → main (3 commits, 5 files)

## Stage 1 — Spec Compliance — PASS

### [F03] Quick-switch projects — PASS
<evidence: each plan outcome ↔ diff line(s)>

### [B07] Timer badge — PASS
<evidence>

## Stage 2 — Code Quality — CONCERNS

### 1. Convention compliance — CONCERN
- src/Foo.swift:42 — uses raw URLSession; KNOWLEDGE says all network calls go through NetworkClient
- ...

### 2. Obvious quality — PASS
<evidence>

### 3. Stale scaffolding — PASS
<evidence>

### 4. Silent failure check — PASS
<evidence>

Verdict: CONCERNS
```

Short-circuit variant (Stage 1 returned FAIL):

```
Review: `rota/foo` → main (3 commits, 5 files)

## Stage 1 — Spec Compliance — FAIL

### [F03] Quick-switch projects — FAIL
- .rota/plans/M01-F03.md outcome "Cmd+Tab overlay on the project picker" was not fulfilled — diff adds the overlay but doesn't wire the Cmd+Tab keybinding.
- diff went off-target into src/Settings.swift (not implied by the plan).

## Stage 2 — Code Quality — Skipped (Stage 1 returned FAIL)

Verdict: FAIL
```

Single-stage variant (Stage 1 skipped via no-plan fallback or `--stage quality`):

```
Review: `rota/foo` → main (3 commits, 5 files)

## Stage 1 — Spec Compliance — Skipped (no plans found for referenced items)

## Stage 2 — Code Quality — PASS

### 0. Intent match — PASS
<evidence (legacy fallback when Stage 1 didn't run)>

### 1. Convention compliance — PASS
...

Verdict: PASS
```

## Step 10 — Route Based on Verdict

The verdict is the entire product — return it and stop. Never ask a follow-up; the caller (the user, or `/rota-ship` when invoked) owns what happens next.

When invoked from `/rota-ship`, return the verdict; the parent routes on the recorded verdict with `rota verdict route --for ship-review` (`references/review-verdict-routing.md`). When invoked standalone, relay the verdict to the user using the *Producer-side relay* table in the reference — short summary:

- **PASS** — *"Ready to ship. Run `/rota-ship`."*
- **CONCERNS** — print the concerns inline (already done in Step 6), then suggest *"Address via `/rota-work` and rerun `/rota-review`, or accept and ship via `/rota-ship`."*
- **FAIL** — tell the user the merge would regress. Suggest fixing via `/rota-work` or `/rota-debug`. Don't route to `/rota-ship`.

## Queue mode (`--queue`, issue mode)

Works through every `needs-review` item's open PR / MR. Issue mode only (`backlog.backend: "issues"`; see `references/issue-mode.md` for the label lifecycle). In file mode say *"`--queue` needs the issue backend; use `/rota-ship` and `rota ship merge` here"* and stop.

```bash
rota review queue --json
```

`data.items` is `[{"id","number","title","prs":[{"number","title","branch","url","body"}]}]`. Empty: report *"Review queue is empty"* and stop. Per entry, in order:

1. **PRs.** None: report *"<ID> is `needs-review` but has no PR with a closing keyword"* and skip. Several: review each.
2. **Checkout.** `git status --short` must be clean, else stop. Check the PR out through the adapter: `rota tracker call -- pr checkout <n>` (GitHub) or `-- mr checkout <n>` (GitLab).
3. **Review.** Run Steps 2-9 on the checked-out branch, scoped to `<base>...HEAD` (`<base>` from `rota git base`). The reviewer is read-only; so is the loop, apart from the verbs below.
4. **Route** on `rota verdict route <branch> --for queue --json`, field `data.next`:
   - **`ask` / `merge`** (PASS) — `ask`: `AskUserQuestion` (Header `"Merge"`, *"Merge PR <n> for <ID>?"*, options *Merge (Recommended)* / *Skip* / *Stop*); `merge` (loop mode): merge without asking. Merge with `rota ship pr-merge <n>` (in an umbrella `--repo <name>` is required; queue entries carry `repo` and qualified IDs): it merges and closes the linked items the host left open; `data.sha` and `data.closed` report the result. For `ask`, pass the Merge answer along: `--confirm --confirm-note "<answer>"` (ignored unless `ship.mergeApproval` covers the PR). Exit 4 with `data.unproven` means nothing was merged because an item has no proof: it is now `changes-requested`; report it and move on. Exit 4 with `data.blockedBy: "verdict"` means the PR's branch has a recorded review or second-opinion FAIL: nothing changed; report it and move on. Exit 4 with `data.blockedBy: "manual gate"` is the `merge-approval` gate (`ship.mergeApproval` requires a human for this merge; `data.paths` names the files that put it there): nothing changed. Ask the user in an `AskUserQuestion` that loop mode never auto-picks, then re-run with `--confirm --confirm-note "<their answer>"`. In loop mode, merge with `--escalate` instead and move on to the next PR; re-run with `--approval <data.escalation.id>` once `rota round escalate check` reports it answered (`references/manual-gates.md`, "Merge approval in an unattended round"). Then post the verdict on each linked item (`rota item comment add <ID> --kind feedback --body-file -`) and on the PR (`rota tracker call -- pr comment <n> --body-file -` on GitHub, `-- mr note <n> --message "<verdict>"` on GitLab).
   - **`request-changes`** (CONCERNS / FAIL) — post the findings as a `feedback` comment on each linked item and on the PR (same commands), then `rota item state <ID> --to changes-requested`. The author's next `/rota-work` claim reads the feedback. No merge, under any autonomy level.
5. **Return.** `git checkout <base>` before the next entry.

Exit 5 or 6 (tracker unavailable or rate-limited) from any verb stops the queue with a report of what was done and what is left. Never retry in a loop. Routing table: `references/review-verdict-routing.md` (Queue routing).

## Rules

- **Read-only.** Never edit, commit, or stage. The verdict is the entire product; recording it with `rota verdict add` (the gitignored `.rota/verdicts.json`) is the one write.
- **Evidence over opinion.** Every concern must cite file:line or commit hash.
- **Scope is bounded.** Only the diff against the base is reviewed — don't wander into unchanged code.
- **Call it honestly.** If conventions were violated but the user has a good reason, the reviewer still reports CONCERN — the user decides what to do.
- **Don't re-run on a passed branch.** If `rota verdict show <branch> --json` has a review record with `verdict` PASS and `stale: false`, skip Steps 7 and 8 and report that verdict.
- **Stage gating lives in the verb.** A Stage 1 FAIL routes to `report` and skips Stage 2; otherwise both stages run and `data.combined` is the worst of the two.

## References

- [`references/knowledge-consult.md`](references/knowledge-consult.md) — Canonical K+D query pattern (`rota knowledge query` + `rota decisions query`) used by every cycle-starting skill.
- [`references/review-verdict-routing.md`](references/review-verdict-routing.md) — PASS / CONCERNS / FAIL routing for `/rota-review` consumers.
