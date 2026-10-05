---
name: rota-refactor
description: Use on "review the architecture", "find refactoring opportunities", "deepen modules", or scoped to one area ("/rota-refactor internal/cli").
---

# rota-refactor

> The architecture vocabulary, heuristics and candidate format below are adapted from `improve-codebase-architecture` in [mattpocock/skills](https://github.com/mattpocock/skills) (MIT); see [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

Surface architectural friction and file each finding as an issue. The default run changes no code. The aim is code that is easier to test and easier for an agent to navigate.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — exploration and ranking (default `opus`)
- `models.worker` — `--fix` implementation subagents (default `sonnet`)
- `refactor.verifyCommands` — shell commands run as gates in `--fix` verification (default `[]`)
- `refactor.confirmBeforeExecute` — `--fix` only: pause before fixing (default `true`)

## Args

- `<area>` — a path, directory or subsystem name. Scope the review to it. Several workers can each take one area. Without an area, review the whole repo (see Explore for prioritization).
- `--fix` — after filing, implement the candidates you pick (Fix path below). Without it the run ends at Step 4.
- `--designs` — for structural candidates, draft competing interfaces before recommending one (`references/refactor-design-approaches.md`). Off by default; it is the expensive step.
- `--interactive` — present the ranked candidates and ask which to file instead of filing all of them.

Umbrella projects: `rota refactor targets --json` lists the sub-repos. Run once per sub-repo with the global `--repo <name>` so each finding lands on the tracker that owns the code. Do not fan out sub-agents from here; the orchestrator of a round assigns one area per worker.

## Vocabulary

Use these terms in every finding. Do not drift into "component", "service", "API" or "boundary".

- **Module** — anything with an interface and an implementation: a function, a package, a skill, a command.
- **Interface** — everything a caller must know to use the module: types, invariants, error modes, ordering, required config. Not just the signature.
- **Implementation** — the code behind the interface.
- **Depth** — how much behaviour sits behind how small an interface. **Deep** = a lot behind a little. **Shallow** = the interface is about as complex as the implementation.
- **Seam** — the place a module's interface lives, where behaviour can change without editing in place.
- **Adapter** — a concrete thing that satisfies an interface at a seam.
- **Leverage** — what callers get from depth: one implementation pays back across many call sites and tests.
- **Locality** — what maintainers get from depth: change, bugs and knowledge concentrate in one place.

## Heuristics

- **Deletion test.** Imagine deleting the module. If the complexity vanishes, it was a pass-through. If it reappears in N callers, the module was earning its keep. A candidate is one where deletion would concentrate complexity, not just move it.
- **The interface is the test surface.** Callers and tests cross the same seam. A module you have to test past its interface is the wrong shape.
- **One adapter is a hypothetical seam; two are a real one.** Do not propose a seam unless something actually varies across it.
- **Extraction that lost locality.** Pure functions pulled out for testability while the bugs live in how they are called.
- **Leaks across seams.** Tightly coupled modules that reach into each other's internals or share hidden state.
- **Hard to test through the interface.** Code that needs mocks of its own internals, or has no test at its seam.
- **Concept sprawl.** Understanding one idea takes bouncing between many small modules.

## Flow

Orient → Explore → Rank → File → *(opt-in)* Fix

### Step 1 — Orient

Read what the project already decided before looking at code. Pull only what touches the area:

```bash
rota glossary read <term>…
rota knowledge query <topic>…
rota decisions query <topic>…
```

Use the glossary's names for domain concepts ("the claim module", not "the FooHandler"). Do not re-suggest something a recorded decision rules out. If friction is real enough to warrant reopening a decision, file the candidate anyway and name the decision it contradicts in the body.

Run `rota git guard clean --context "/rota-refactor"` only under `--fix`.

### Step 2 — Explore

Dispatch one exploration agent on the **orchestrator** model (a `light` model is enough when the area is small). Scope it to the area. Rule: rank files by inbound imports, then size, then recent change; read the top fifth in full and one hop of callers and importers; sample the rest. Stop at 8–12 candidates, or after 30+ files with no new kind of friction in the last 5. Do not pad.

For each candidate the agent reports: files with line ranges, the friction in vocabulary terms, the deletion-test result, and what is hard to test today. It does not propose interfaces.

### Step 3 — Rank

Assign each candidate a strength:

- **Strong** — deletion test concentrates complexity, a real seam exists, tests would get simpler.
- **Worth exploring** — plausible depth gain, but a design choice or a missing second adapter is unsettled.
- **Speculative** — a hunch the code does not yet back.

Mark one **top recommendation** and say why it goes first. Candidates that are only a one-line fix with no design choice are *simple*; file them like the rest, labelled in the body. With `--designs`, run the competing-design step now for the structural ones.

### Step 4 — File

File one issue per candidate. Never file a candidate you cannot state acceptance for.

**Dedup first.** Fetch refactor issues in every state and compare on files and problem, not title:

```bash
rota tracker call -- issue list --label refactor --state all --limit 200 --json number,title,state,body
```

- A match that is open: skip, and comment with any new evidence.
- A match that is closed: skip unless the friction demonstrably came back; then file a new issue that cites it (`Related: #<n>`).
- A closed match whose reason was "won't do": treat as rejected, do not re-file.

**Create.** Body in a scratch file, then:

```bash
rota item create --json --kind tasks --title "<verb-first title>" --desc "<one line>" --body-file <scratch>
rota issues label <number> --add refactor
```

(File-mode backlogs have no tracker: `rota item create` alone, and the item is the finding.)

When findings must land in order (one builds on another's seam, or both rewrite the same lines), file the prerequisite first and pass `--depends-on <its ID>` on the next; see `references/dependent-items.md`. Independent findings get no edge.

Body sections:

- **Pointers** — paths and line ranges. Paths live here only, never in Acceptance, which states behavior.
- **Problem** — the friction, in vocabulary terms, with the deletion-test result.
- **Solution** — plain description of what would change. No signatures unless `--designs` ran.
- **Benefits** — locality and leverage, and how the tests improve.
- **Strength** — Strong / Worth exploring / Speculative, and whether it is the top recommendation.
- **Conflicts** — the recorded decision it contradicts, if any, and why it is worth reopening.
- **## Acceptance** — checkable boxes: the interface the callers use afterwards, which duplicated logic is gone, which tests cross the seam. Include "existing tests still pass".
- **## Out of scope** — one to three bullets on what the fix must not touch, or "nothing noted". `rota round assign` copies it into the worker brief.

Report: a table of filed issues (number, title, strength), the skipped duplicates with the issue they matched, and the top recommendation. Zero filed is a valid result.

**Rejections.** If the user rejects a candidate with a reason a later review would need to avoid re-suggesting it, offer `/rota-decide` to record it. Skip ephemeral reasons ("not now") and self-evident ones.

Under `--interactive`, show the ranked list before filing and let the user drop or reorder candidates; the grilling conversation about the shape of a chosen candidate belongs in `/rota-brainstorm`, not here.

Without `--fix`, stop here.

## Fix path (`--fix`)

Fix only the candidates the user named, or all filed in this run if they said "all". With `refactor.confirmBeforeExecute` true, confirm the list once with `AskUserQuestion` first.

1. **Group.** Independent files run in parallel; one agent per file when files overlap; order real dependencies.
2. **Dispatch** workers on the **worker** model. Each brief names the exact files, the problem, the chosen approach (the design from `--designs` for structural items), and the acceptance criteria from the issue. Constraints: read before editing, minimal diff, no unrelated cleanup.
3. **Verify** with one **orchestrator** agent that reads the changed files, runs every `refactor.verifyCommands` entry verbatim and reports PASS / FAIL / CONCERN per fix. A non-zero exit is a FAIL. With no commands configured, say the tree was not gated. Re-dispatch FAILs once; report what still fails.
4. **Commit** once: stage explicit paths, subject `refactor: <summary>`, body listing the issues (`Closes #<n>` each). Then zero the pressure counter:

```bash
rota refactor reset
```

Report the commit and the issues it closes in a few lines. Do not recap exploration or designs.

## Key principles

- **Findings first.** The default output is issues, not diffs. A review that finds nothing worth filing says so.
- **Same words every time.** Vocabulary drift makes the findings unsearchable and the dedup unreliable.
- **Respect recorded decisions.** Reopen one only with real friction, and say so.
- **Minimal diffs on the fix path.** Each fix touches only what it needs.
- **Verify before commit.** `--fix` never commits without orchestrator sign-off.

## References

- [`references/refactor-design-approaches.md`](references/refactor-design-approaches.md) — competing-interface choreography for `--designs`.
- [`references/dependent-items.md`](references/dependent-items.md) — ordering filed findings with `--depends-on`.
- [`references/knowledge-consult.md`](references/knowledge-consult.md) — the knowledge and decisions query pattern used in Orient.
