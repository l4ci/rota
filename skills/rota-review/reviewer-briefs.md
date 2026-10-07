# Reviewer briefs (Step 7)

Fill the bracketed parts from Steps 2-6 and drop any empty section. Both briefs open with the shared block, then add their own rubric.

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

## Spec reviewer brief

Shared block, plus these sections (items and Review Focus go here; the Standards reviewer gets them only via the shared block):

```
**Axis: Spec.** Does the diff deliver what the items promised, nothing more? Do not judge style, conventions or test quality: another reviewer does.

**Review Focus (from the plan, verbatim):**
<the plan's `## Review Focus` lines per item, or drop this section>
Check each named edge is handled and pinned by a test; an unhandled or untested one is a CONCERN.

**Recorded proof:**
<rows from `rota proof show <ID>` per item, or "none recorded">
Rows are verification already run (check, result, sha, evidence). Do NOT re-run a check that has a PASS row at the current sha; spot-check one row. A FAIL row or a row at a stale sha is a gap to cite. A behavior change whose proof has no FAIL row (RED run before the change) is a CONCERN; a docs or skill-only change is exempt when its proof row says `no test seam: docs/skill change`.

**Rubric. For each item, return PASS / CONCERN / FAIL with evidence.**

1. **Intent match** — does the diff deliver every outcome in the item's spec? PASS cites the diff line for each outcome. A partially met outcome (stub, missing test for a named outcome) is a CONCERN. A missing outcome, or edits to files the spec does not imply, is a FAIL. If an item's spec is too thin to check, say so rather than inventing one.
2. **Drift** — trace each change back to the item's intent. Sensible steps nobody asked for (extra options, generalised helpers, adjacent cleanups) are a CONCERN naming the drift path, never a FAIL on their own.
```

## Standards reviewer brief

Shared block, plus:

```
**Axis: Standards.** Does the diff meet the project's standards? Do not judge whether it matches the items' intent: another reviewer does.

**Relevant project conventions (from KNOWLEDGE.md):**
- <bullet>

**Hard boundaries (from DECISIONS.md):**
<entries from `rota decisions query`, full rule + forbids/permits>

**Plan relies on:**
<the plan's `## Relies on` entries, each with its full KNOWLEDGE bullet or DECISIONS entry, or drop this section>
Check the diff against each entry. One the diff contradicts or ignores is a CONCERN (a FAIL for a DECISIONS entry) that names the entry in the finding's title.

**Possible stale scaffolding (deterministic pre-flight grep):**
<file:line>: <matched line text>

**Rubric. Return PASS / CONCERN / FAIL per heading, with evidence.**

1. **Convention compliance** — does the diff respect the KNOWLEDGE.md bullets and each `**Plan relies on:**` entry? Any regression on a captured gotcha?
2. **Code-smell baseline** — dead code, error swallowing, security smells, API contract breaks, performance cliffs, untested new branches. A short baseline, not a full code review; focus on what the user would regret after merge. A documented project standard (a KNOWLEDGE bullet, a DECISIONS entry, the repo's own idiom) overrides the baseline: code that follows it is never a smell.
3. **Tests** — flag a **tautological** test (the expected value is computed the way the code computes it, so it cannot disagree with the code) and an **implementation-coupled** test (pinned to internals or call order instead of behaviour, so a correct refactor breaks it). Each is a CONCERN with file:line.
4. **Stale scaffolding** — judge each `**Possible stale scaffolding:**` match: a leftover *Task N* / *placeholder* / *not yet wired* / *added later* / *in flight* annotation that should have gone once the work landed is a CONCERN with file:line; legitimate prose (a markdown placeholder section, a docstring describing user-visible "in flight" semantics, an enum value named `placeholder`, a `Task <N>` in a per-task brief or test name) is a PASS. Many matches are benign.
5. **Silent failure check** — for every verification claim in the diff (new test, smoke section, assertion, helper-output check), apply the four-question rubric: (a) what does this verify concretely? (b) is the asserted-on shape the same shape the real consumer reads? (c) was the new code path actually exercised? (d) if you deleted the new code, would the assertion still pass? Any *no* or *unclear* is a `SILENT-FAIL` with file:line and a one-sentence explanation; treat it as a CONCERN. Full rubric: `references/silent-failure-hunter.md`.
6. **Decision violations** — compare the diff against `**Hard boundaries:**`. Any forbidden pattern present = FAIL.
```

## Both briefs end with

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

Never put a finding from one reviewer into the other's block or rerank them.
