# Wave planning and worker briefs

Used by `/rota-work` Steps 4 and 6: how to split tasks into waves without collisions, and how to write a brief a subagent can't misread.

## Wave-internal file collisions

Before grouping into waves, scan task pairs for **any two tasks whose modified-file sets intersect**. Under `work.isolation: "branch"` two write-only workers editing one file race on disk (the second worker's `Edit` reads sibling-mutated content). Resolve every intersecting pair by one of:

- **Absorption (preferred).** Fold one task's same-file portion into the other task's brief. The absorbed task then touches only files no other task writes, and both run in parallel.
- **Split ownership.** Each task owns a disjoint range, with the boundary named in both briefs.
- **Serialize across waves.**

Rename plus link-sweep is the canonical instance. `rota plan rename-check <old-name> [-- <scope>...]` is the ground truth for it; re-run it at verification to catch enumeration gaps, and tell the worker to run `git grep -l "<old-name>" -- <scope>` before reporting.

**Disjoint file sets don't prove independence.** Two tasks can break the merged tree while neither diff does: one **widens, narrows or re-types a shared symbol's signature** while a sibling adds a fresh call to it, or one **stops emitting a constant, key or output field** a sibling starts depending on. Each worker is internally consistent and per-task diff review passes both. When a brief changes a symbol's *shape* rather than its name, serialize it ahead of every task that references the symbol, or absorb the call-site updates into it. If the project has a whole-tree check (typecheck, compile, `rota-qa`), run it once after the wave's commits rather than per task.

## Brief rules

- Exact paths and line numbers, the pattern to follow, the suggested commit message, read-first, minimal diff, no unrelated changes. Workers do NOT stage or commit.
- **Enumerate the brief's falsifiable claims.** A brief precise enough to execute (line numbers, call-site counts, "the only adopter is X") is precise enough to be wrong, and a worker handed a wrong instruction implements it faithfully. List the claims the task's shape depends on in `**Claims to verify before building on them:**`. Stale plans are the usual source (`KNOWLEDGE.md`, *Re-grep the actual surface before executing a stored plan*). Leave the section out when the brief has no such claim (a new file, a grep-derived sweep).
- **Cross-referencing parallel artifacts.** When two parallel workers author artifacts that cite each other, pre-specify the citation language (path plus named role) in each brief. Serialize when a citation must quote or restate the other artifact.
- **Docs next to helpers.** A doc worker running beside the workers writing the helpers it documents will paraphrase the brief and drift from the code. Pin exact signatures verbatim into the doc brief, or serialize the doc worker after the helper commit.
- **Dispatch vs direct.** N near-identical mechanical edits on disjoint files (an 18-site SKILL.md sweep) go faster as orchestrator-direct parallel `Edit` calls. Litmus: is the task one `Edit` against a uniquely anchored `old_string`? Yes: inline.
- **Edit race.** Parallel workers editing different ranges of one file may see *"File has been modified since read"*. Re-`Read` and re-run the `Edit` with a byte-identical `old_string`; don't regenerate it.

## Verifying a completion

Trust the diff, not the worker's narrative: a worker re-entering files in a later wave can mis-attribute its own writes. Beyond the diff and structural greps:

- **Gap-fills.** Where a worker fills a gap left by extraction (sparse prose, missing rationale), expect plausible editorial that wasn't in the source. Check the claim weight of what it wrote, not just its shape.
- **A worker that disputes its brief is a PASS on the worker and a FAIL on the plan.** Confirm its finding against the code yourself, then re-derive the task from what is there; don't re-dispatch with a patched line number. If the correction changes what the task should accomplish, go back to planning. If it invalidates a stored plan's premise, say so in the commit message rather than editing the plan back. A worker that returns exactly what a falsifiable brief asked for with no friction is not evidence the brief was right: spot-check one claim.
- When a wave produced several completions, verify them in one parallel batch of reads and greps.
