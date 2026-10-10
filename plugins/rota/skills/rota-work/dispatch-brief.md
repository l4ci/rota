# Dispatch brief template (Step 6)

Loaded by `SKILL.md` Step 6. One brief per task; fill every field. Omit a bracketed line that doesn't apply.

```
You are implementing Task N of [total].
[UMBRELLA: "Sub-repo: <name>. Run all git operations from <absolute-sub-repo-path>; the umbrella's `.git/` is shared coordinator state, NOT your target."]
[WORKTREE: "Working directory: <absolute-worktree-path>. cd there before any file operations."]

**Goal:** [one sentence]

**Files:**
- Create: [paths]
- Modify: [paths with line references]

**What to do:**
[Precise instructions: what to read, what to change, exact code where possible]

**Known gotchas:**
[Relevant bullets from rota knowledge query output]

**Hard boundaries:**
[Relevant entries from rota decisions query: full rule plus forbids/permits. Subagents MUST respect these; Step 7 checks the diff for violations.]

**Canonical terms:**
[Relevant terms from rota glossary read: definition plus aliases. Use these names in code, comments and commit messages.]

**Critical constraints:**
[Behavior preservation, patterns to follow, things NOT to touch]

**RED before GREEN:** [behavior change: write the new test first, run it against the unchanged code and report the command plus the failing output line before touching production code; a test that already passes proves nothing, so fix the test. Write the test to `references/test-quality.md`. Docs, skill-text or other no-test-seam change: write `no test seam: docs/skill change` and skip.]

**Claims to verify before building on them:**
[Every factual claim this brief rests on: a line number, a call-site count, "function X already returns Y". Check each first. If one is false, STOP and report which claim and what is actually there; do not implement around it.]

**Do NOT run `git add` or `git commit`.** Write changes to files only. The main session commits (Step 7.5); leave a clean working-tree diff matching the brief.

**Suggested commit message:** [exact message; the main session uses it in Step 7.5]

**On completion:** report the RED command and failing line (or the no-test-seam note), the files you modified, plus any tool-generated siblings the toolchain produced, and confirm you did not stage or commit. Name any brief claim that turned out false, even if you worked around it.
```

Brief-writing rules, falsifiable-claims discipline, pre-baked citations, doc-writer ordering and the same-file Edit race: [`references/work-wave-planning.md`](references/work-wave-planning.md).
