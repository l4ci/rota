---
name: rota-refactor
description: Use on "review the architecture", "find refactoring opportunities", "deepen modules", "map the repo", "refresh the project map" (--map), or scoped to one area ("/rota-refactor internal/cli").
---

# rota-refactor

> The architecture vocabulary, heuristics and finding format below are adapted from `improve-codebase-architecture` in [mattpocock/skills](https://github.com/mattpocock/skills) (MIT); see [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

Surface architectural friction and file each finding as an issue. The default run changes no code.

## Configuration

Read `.rota/config.json`:

- `orchestrator.model`: main session model: exploration and ranking (Claude default `opus`)
- `map.softcap_subsystems`: `--map` only: the most subsystems the map names (default 20)

`--fix` also reads `round.tiers.claude.standard`, `test.full` and `refactor.confirmBeforeExecute`; [`fix-path.md`](fix-path.md) lists them.

## Args

- `<area>`: a path, directory or subsystem name. Scopes the review. Without one, review the whole repo (see Explore).
- `--fix`: after filing, implement the findings you pick (Fix path below). Without it the run ends at Step 4.
- `--designs`: for structural findings, draft competing interfaces before recommending one (`references/refactor-design-approaches.md`). Off by default (expensive).
- `--map`: map the repo instead of reviewing it: write one `.rota/map/<name>.md` per subsystem, stamped with `last_mapped_commit`. Files issues never; changes no code. Read [`options.md`](options.md) (Map mode) and follow it instead of the Flow below.
- `--interactive`: present the ranked findings and ask which to file instead of filing all.

In an umbrella project, read [`options.md`](options.md) (Umbrella projects).

## Vocabulary

Use these terms in every finding (not "component", "service", "API", "boundary"); drift breaks search and dedup.

- **Module**: anything with an interface and an implementation: function, package, skill, command.
- **Interface**: everything a caller must know: types, invariants, error modes, ordering, required config. Not just the signature.
- **Implementation**: the code behind the interface.
- **Depth**: behaviour behind interface size. **Deep** = a lot behind a little. **Shallow** = interface about as complex as the implementation.
- **Seam**: where a module's interface lives; behaviour can change there without editing in place.
- **Adapter**: a concrete thing satisfying an interface at a seam.
- **Leverage**: callers' payoff from depth: one implementation serves many call sites and tests.
- **Locality**: maintainers' payoff from depth: change, bugs and knowledge concentrate in one place.

## Heuristics

- **Deletion test.** Imagine deleting the module. Complexity vanishes: pass-through. It reappears in N callers: the module earns its keep. A finding is a module where deletion would concentrate complexity, not just move it.
- **The interface is the test surface.** Callers and tests cross the same seam; a module tested past its interface is the wrong shape.
- **One adapter is a hypothetical seam; two are a real one.** Do not propose a seam unless something actually varies across it.
- **Extraction that lost locality.** Pure functions pulled out for testability while the bugs live in how they are called.
- **Leaks across seams.** Modules reaching into each other's internals or sharing hidden state.
- **Hard to test through the interface.** Needs mocks of its own internals, or no test at its seam.
- **Concept sprawl.** One idea takes bouncing between many small modules.
- **Do not widen the interface for tests.** A function exported only so a test can reach it is a leak, not a seam. Test through the existing interface, or find the deeper seam.

## Flow

Orient → Explore → Rank → File → *(opt-in)* Fix

Copy this checklist and track your progress:
```
- [ ] Step 1 — Orient
- [ ] Step 2 — Explore
- [ ] Step 3 — Rank
- [ ] Step 4 — File
```

### Step 1 — Orient

Pull what the project already decided that touches the area:

```bash
rota glossary read <term>…
rota knowledge query <topic>…
rota decisions query <topic>…
```

Use the glossary's names for domain concepts. Do not re-suggest what a recorded decision rules out; if friction warrants reopening one, file the finding and name the decision in the body.

### Step 2 — Explore

Dispatch one exploration subagent on `orchestrator.model` (`light` is enough for a small area), scoped to the area. With an `<area>`, rank files by inbound imports, then size, then recent change. With none, rank by churn first (`git log --since=90.days --name-only --pretty=format: | sort | uniq -c | sort -rn`), then inbound imports, then size; read the top fifth in full plus one hop of callers and importers; sample the rest. Stop at 8 to 12 findings, or after 30+ files with no new kind of friction in the last 5. Do not pad.

Read [`less-code.md`](less-code.md) into the brief: it also looks for code to delete or shrink, in the same pass and under the same cap. Per finding it reports files with line ranges, one concrete case (Step 4), the friction in vocabulary terms, the deletion-test result, what is hard to test today and the dependency category (Step 3). It also reports the areas it did not read, sampled files included. No interface proposals.

### Step 3 — Rank

Assign each finding a strength:

- **Strong**: deletion test concentrates complexity, a real seam exists, tests would get simpler.
- **Worth exploring**: plausible depth gain, but a design choice or a missing second adapter is unsettled.
- **Speculative**: a hunch the code does not yet back.

A less-code finding with a proven case, such as a deletion whose unused-code search is clean, can be Strong.

Give each finding an **impact tier** beside its strength: **must**, **should** or **nice**, plus one `If skipped:` line (tiers in [`less-code.md`](less-code.md)). Order the findings by tier, then strength. The cap stays 8 to 12 across both lenses.

When two fixes solve the same problem, take the one with the smaller net diff. Add a layer only when it removes more than it adds. Batch small findings (net change of 20 lines or fewer, no design choice) per package directory into one issue of at most 10 items. List each item with its path, line range, kind and estimate. The issue takes the highest tier of its items.

Tag each finding with a **dependency category**, which sets the test approach at its seam:

- **In-process**: pure computation or in-memory state. Test through the interface directly; no adapter.
- **Local-substitutable**: has a local stand-in (temp dir, in-memory DB, fake clock). Test with the stand-in running.
- **Remote-owned**: a service the project owns across a network boundary. Put a port at the seam; test with an in-memory adapter, and the real one with a contract test.
- **True external**: a third party the project does not control. Put a port at the seam; mock only at that adapter.

Before filing a finding, grep for the seam it proposes: one that already exists is no finding. Mark one **top recommendation** and say why it goes first. One-line fixes with no design choice are *simple*: batch them per the small-finding rule above when they meet it, otherwise file them like the rest, labelled in the body. With `--designs`, run the competing-design step now for structural ones.

### Step 4 — File

File one issue per finding, except a batch of small findings, which is one issue (Step 3). Never file a finding you cannot state acceptance for.

**Check each finding first.** Code moves while a review runs, so a premise may already be fixed.

1. **Concrete case.** Name one input or situation and the wrong result or real cost it causes. Drop a finding with no concrete case.
2. **Re-read at current HEAD.** `git fetch <remote> <base>` (`<base>` from `rota git base`, `<remote>` from `git.remote`), then read the cited lines with `git show <remote>/<base>:<path>`; no checkout. With no remote, read local `<base>`. Drop a finding whose case no longer holds, and update line ranges that moved.
3. **Recently closed issues.** List them and read those that touch the same files: `rota tracker call -- issue list --state closed --limit 100 --json number,title,closedAt,body` (file-mode backlogs: `git log <remote>/<base> -- <path>`). Drop what they already fixed or decided.
4. **Unused code.** Before you call code unused, search beyond Go callers (or the language's own callers): tests, smoke sections, skill and docs text, embedded files, and verb or flag names registered as strings. A hit that needs the code ends the claim. List where you searched in the body.

**Dedup first.** Fetch refactor issues in every state and compare on files and problem, not title:

```bash
rota tracker call -- issue list --label refactor --state all --limit 200 --json number,title,state,body
```

- Open match: skip; comment any new evidence.
- Closed match: skip unless the friction demonstrably came back; then file a new issue citing it (`Related: #<n>`).
- Closed as "won't do": rejected, do not re-file.

**Create.** Build one JSON record per issue: one per finding, or one per batch of small findings (Step 3). A batch record's title names the area, its body lists each item (path, line range, kind, estimate), and its tier is the highest item tier. Build one record at a time: its title, summary (`desc`) and full body come from that same finding or batch, never from a title list and a body list kept apart. Write the array to a scratch file and file it in one call:

```bash
rota item create --json --batch-file <scratch.json>
rota issues label <number> --add refactor   # for each id the call printed
```

A record is `{"kind":"tasks","title":"<verb-first title>","desc":"<one line>","body":"<markdown>"}`; it also takes `tag`, `dependsOn`, `related`, `milestone`, `repos`, `subsystem`. The call refuses the whole batch (exit 2) when a title has fewer than three words or is a placeholder, and prints `{id, title}` per record: compare each against its finding before labelling.

(File-mode backlogs have no tracker: the same call, and the item is the finding.)

When findings must land in order (one builds on another's seam, or both rewrite the same lines), file the prerequisite first and pass `--depends-on <its ID>` on the next (`references/dependent-items.md`). Independent findings get no edge.

Body sections:

- **Pointers**: exact file paths and line ranges, never a bare directory (the round overlap check matches a directory against every file below it). Paths live here and in Behaviour to keep and Searched, never in Acceptance, which states behavior.
- **Problem**: the friction, in vocabulary terms, with the deletion-test result.
- **Concrete case**: the input or situation, and the wrong result or real cost.
- **## Behaviour to keep** (required when the fix merges, unifies or moves code): every check, error path, validation and report each copy does today, one bullet each with `file:line`, grouped by copy. Read every copy in full: a unify silently drops what only one copy does.
- **Searched** (unused-code findings only): the places searched and the terms used.
- **Solution**: what would change, in plain words. No signatures unless `--designs` ran.
- **Benefits**: locality, leverage, test improvement.
- **Strength**: Strong / Worth exploring / Speculative, and whether it is the top recommendation.
- **Net lines, tier and if skipped**: the estimate as `~-N` or `~+N`, the impact tier and the `If skipped:` line.
- **Dependencies**: the category (in-process, local-substitutable, remote-owned, true external) and the test approach it implies.
- **Conflicts**: the recorded decision it contradicts, if any, and why it is worth reopening.
- **## Acceptance**: checkable boxes: the interface the callers use afterwards, which duplicated logic is gone, which tests cross the seam. Include "existing tests still pass" and "unit tests superseded by seam-level tests are removed, not kept alongside". With a Behaviour to keep section, add "every behaviour listed under Behaviour to keep still runs, or the PR names its removal and justifies it".
- **## Out of scope**: one to three bullets on what the fix must not touch, or "nothing noted". `rota round assign` copies it into the worker brief.

Report: table of filed issues (number, title, tier, strength, net lines) with the summed net line estimate, skipped duplicates with the matched issue, dropped findings with the reason, the top recommendation. End it with one line, `Not checked: <areas the review did not read>` (`Not checked: nothing` only when every file in scope was read in full). Zero filed is valid.

A hosted round architecture-review worker ends with `ROTA-DONE <slot> issues:#a,#b` for filed issues or `ROTA-DONE <slot> issues:none` for no findings. The summary above that line ends with the same `Not checked:` line; the `ROTA-DONE` line stays last and unchanged. Printing that final line reports completion; `round report` is solo-only. Solo reporting with no findings uses `rota round report <slot> --state done --issues none` (include `--attempt <id>` when briefed).

When the user rejects a finding, or `--interactive` was passed, read [`options.md`](options.md) (Rejections, Interactive filing).

Without `--fix`, stop here.

## Fix path (`--fix`)

Read [`fix-path.md`](fix-path.md) when `--fix` was passed and follow it. `--fix` never commits without the verify subagent's sign-off.

## References

- [`less-code.md`](less-code.md): the less-code finding kinds, net line estimate, impact tier and batching of small findings.
- [`options.md`](options.md): umbrella projects, `--map` mode, `--interactive` filing, recording rejections.
- [`fix-path.md`](fix-path.md): `--fix` configuration, clean-tree guard and the fix steps.
- [`references/refactor-design-approaches.md`](references/refactor-design-approaches.md): competing-interface choreography for `--designs`.
- [`references/dependent-items.md`](references/dependent-items.md): ordering filed findings with `--depends-on`.
- [`references/knowledge-consult.md`](references/knowledge-consult.md): the knowledge and decisions query pattern used in Orient.
