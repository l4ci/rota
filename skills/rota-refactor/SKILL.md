---
name: rota-refactor
description: Use on "review the architecture", "find refactoring opportunities", "deepen modules", or scoped to one area ("/rota-refactor internal/cli").
---

# rota-refactor

> The architecture vocabulary, heuristics and finding format below are adapted from `improve-codebase-architecture` in [mattpocock/skills](https://github.com/mattpocock/skills) (MIT); see [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

Surface architectural friction and file each finding as an issue. The default run changes no code.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — main session model: exploration and ranking (default `opus`)

`--fix` also reads `models.worker`, `refactor.verifyCommands` and `refactor.confirmBeforeExecute`; [`fix-path.md`](fix-path.md) lists them.

## Args

- `<area>` — a path, directory or subsystem name. Scopes the review. Without one, review the whole repo (see Explore).
- `--fix` — after filing, implement the findings you pick (Fix path below). Without it the run ends at Step 4.
- `--designs` — for structural findings, draft competing interfaces before recommending one (`references/refactor-design-approaches.md`). Off by default (expensive).
- `--interactive` — present the ranked findings and ask which to file instead of filing all.

In an umbrella project, read [`options.md`](options.md) (Umbrella projects).

## Vocabulary

Use these terms in every finding (not "component", "service", "API", "boundary"); drift breaks search and dedup.

- **Module** — anything with an interface and an implementation: function, package, skill, command.
- **Interface** — everything a caller must know: types, invariants, error modes, ordering, required config. Not just the signature.
- **Implementation** — the code behind the interface.
- **Depth** — behaviour behind interface size. **Deep** = a lot behind a little. **Shallow** = interface about as complex as the implementation.
- **Seam** — where a module's interface lives; behaviour can change there without editing in place.
- **Adapter** — a concrete thing satisfying an interface at a seam.
- **Leverage** — callers' payoff from depth: one implementation serves many call sites and tests.
- **Locality** — maintainers' payoff from depth: change, bugs and knowledge concentrate in one place.

## Heuristics

- **Deletion test.** Imagine deleting the module. Complexity vanishes: pass-through. It reappears in N callers: the module earns its keep. A finding is a module where deletion would concentrate complexity, not just move it.
- **The interface is the test surface.** Callers and tests cross the same seam; a module tested past its interface is the wrong shape.
- **One adapter is a hypothetical seam; two are a real one.** Do not propose a seam unless something actually varies across it.
- **Extraction that lost locality.** Pure functions pulled out for testability while the bugs live in how they are called.
- **Leaks across seams.** Modules reaching into each other's internals or sharing hidden state.
- **Hard to test through the interface.** Needs mocks of its own internals, or no test at its seam.
- **Concept sprawl.** One idea takes bouncing between many small modules.

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

Dispatch one exploration subagent on `models.orchestrator` (`light` is enough for a small area), scoped to the area. Rank files by inbound imports, then size, then recent change; read the top fifth in full plus one hop of callers and importers; sample the rest. Stop at 8–12 findings, or after 30+ files with no new kind of friction in the last 5. Do not pad.

Per finding it reports files with line ranges, the friction in vocabulary terms, the deletion-test result and what is hard to test today. No interface proposals.

### Step 3 — Rank

Assign each finding a strength:

- **Strong** — deletion test concentrates complexity, a real seam exists, tests would get simpler.
- **Worth exploring** — plausible depth gain, but a design choice or a missing second adapter is unsettled.
- **Speculative** — a hunch the code does not yet back.

Mark one **top recommendation** and say why it goes first. One-line fixes with no design choice are *simple*: file them like the rest, labelled in the body. With `--designs`, run the competing-design step now for structural ones.

### Step 4 — File

File one issue per finding. Never file a finding you cannot state acceptance for.

**Dedup first.** Fetch refactor issues in every state and compare on files and problem, not title:

```bash
rota tracker call -- issue list --label refactor --state all --limit 200 --json number,title,state,body
```

- Open match: skip; comment any new evidence.
- Closed match: skip unless the friction demonstrably came back; then file a new issue citing it (`Related: #<n>`).
- Closed as "won't do": rejected, do not re-file.

**Create.** Body in a scratch file, then:

```bash
rota item create --json --kind tasks --title "<verb-first title>" --desc "<one line>" --body-file <scratch>
rota issues label <number> --add refactor
```

(File-mode backlogs have no tracker: `rota item create` alone, and the item is the finding.)

When findings must land in order (one builds on another's seam, or both rewrite the same lines), file the prerequisite first and pass `--depends-on <its ID>` on the next (`references/dependent-items.md`). Independent findings get no edge.

Body sections:

- **Pointers** — paths and line ranges. Paths live here only, never in Acceptance, which states behavior.
- **Problem** — the friction, in vocabulary terms, with the deletion-test result.
- **Solution** — what would change, in plain words. No signatures unless `--designs` ran.
- **Benefits** — locality, leverage, test improvement.
- **Strength** — Strong / Worth exploring / Speculative, and whether it is the top recommendation.
- **Conflicts** — the recorded decision it contradicts, if any, and why it is worth reopening.
- **## Acceptance** — checkable boxes: the interface the callers use afterwards, which duplicated logic is gone, which tests cross the seam. Include "existing tests still pass".
- **## Out of scope** — one to three bullets on what the fix must not touch, or "nothing noted". `rota round assign` copies it into the worker brief.

Report: table of filed issues (number, title, strength), skipped duplicates with the matched issue, the top recommendation. Zero filed is valid.

When the user rejects a finding, or `--interactive` was passed, read [`options.md`](options.md) (Rejections, Interactive filing).

Without `--fix`, stop here.

## Fix path (`--fix`)

Read [`fix-path.md`](fix-path.md) when `--fix` was passed and follow it. `--fix` never commits without the verify subagent's sign-off.

## References

- [`options.md`](options.md) — umbrella projects, `--interactive` filing, recording rejections.
- [`fix-path.md`](fix-path.md) — `--fix` configuration, clean-tree guard and the fix steps.
- [`references/refactor-design-approaches.md`](references/refactor-design-approaches.md) — competing-interface choreography for `--designs`.
- [`references/dependent-items.md`](references/dependent-items.md) — ordering filed findings with `--depends-on`.
- [`references/knowledge-consult.md`](references/knowledge-consult.md) — the knowledge and decisions query pattern used in Orient.
