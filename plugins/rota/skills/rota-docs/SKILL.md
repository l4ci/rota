---
name: rota-docs
description: Use on "update docs", "scaffold the docs", "sync the docs with recent changes", "restructure the docs", or "/rota-docs".
---

# rota-docs — Maintain Public Docs

Public-docs maintainer: first-run scaffold, after-work updates, restructure. Called by hand, by `/rota-ship` Step 8.6 (after-work, inline) and by `/rota-release`'s docs nudge. In a round each worker runs after-work in its own PR (`/rota-ship` Step 8.6); scaffold and restructure escalate to the maintainer there. `/rota-ship --docs` is a deprecated alias for this skill.

Step labels D1–D6 and D-A1–D-A6 are cited elsewhere: keep them.

### Modes

| Detected when | Mode |
|---|---|
| `<docs.path>/` missing or empty | First-run (discovery + scaffold) |
| Post-cycle, from `/rota-work` or Step 8.6 | After-work (propose updates) |
| `/rota-refactor`, or `/rota-docs restructure` | Restructure (audit + reorganize) |
| Manual invoke, no signal | First-run if missing; else after-work in manual mode (trigger gate bypassed, Step D1) |

**Who may run which mode.** After-work runs anywhere, for any agent, including a round worker. First-run scaffold and restructure follow `autonomy.level` ([`references/autonomy.md`](references/autonomy.md)): `"off"` asks the user first; `"roundtrip"` and `"auto"` run them. In a round (a worker slot or the orchestrator), either mode escalates to the maintainer instead of running (`ROTA-BLOCKED` for a worker): a docs tree or a reorganisation is the maintainer's call.

Shared three-mode skeleton with `/rota-qa`: `references/three-mode-skill-shape.md`.

> **One `docs/` tree per project**, at the umbrella root or inside one chosen sub-repo (`docs.repo` records the choice). Never several trees, never a per-sub-repo `docs/` beside an umbrella one, never writes to more than one target. `docs/` is consumer-facing: contributor and contract content lives in the owning `rota-*/SKILL.md`; cross-refs from `docs/` and `README.md` point at `docs/` pages or specific SKILL.md files, never a central internals doc.

### Step D1 — First-Run Detection

Read `docs.path` (default `"docs"`). Missing or empty `<docs.path>/` means first-run (D2–D6). Otherwise branch on `docs.afterWork`:

- **`true`**: Route to the After-work sub-flow as manual mode. Print *"After-work mode is on. Checking docs against changes since the last `docs:` commit."* then start at D-A1. The user running `/rota-docs` by hand is the trigger, so the trigger gate is bypassed and D-A6 omits `Resolves:`.
- **`false`**: ask (header `"After-work"`, *"`<docs.path>/` is initialized but `docs.afterWork` is off. Enable after-work mode? `/rota-work` and `/rota-ship` will then propose doc updates after each cycle."*): `"Enable (Recommended)"` runs `rota config set docs.afterWork true`, prints *"After-work mode enabled."* and exits; `"Leave off"` exits. Ambiguous answers mean Leave off: never flip a flag the user did not ask for.

### Step D2 — Read Project Signals

First-run only. Read [`first-run-scaffold.md`](first-run-scaffold.md) and follow it through D6.

### Step D3 — Form Hypothesis (silent)

See D2.

### Step D4 — Propose Tailored Tree

See D2.

### Step D5 — Scaffold on Approval

See D2.

### Step D6 — Closing Summary

See D2.

### Docs After-Work Sub-Flow

Entered from Step 8.6 or manually per Step D1.

**D-A1 — Trigger gate.** For post-cycle entries apply `references/post-cycle-trigger-gate.md`. Manual entry bypasses the gate (Step D1). If `<docs.path>/` is missing or empty, print *"`/rota-docs` not yet initialized — run `/rota-docs` to scaffold."* and exit; never scaffold mid-cycle.

**D-A2 — Gather context** in one parallel batch: `git log --oneline <last-docs-marker>..HEAD` (marker is the last `docs:` commit; fall back to the last 20 commits); `git diff <marker>..HEAD` over paths not matched by `.docsignore`; the `<docs.path>/` tree and each page's H1/H2 outline.

**D-A3 — Classify changes.** Per remaining diff file: user-facing surface (doc-relevant) or internal-only (tests, build/CI config, unexported helpers, behavior-neutral refactors, dependency bumps). Nothing doc-relevant: print *"No user-facing changes since last `docs:` commit. Skipping."* and exit.

**D-A4 — Map to pages and draft.** Per doc-relevant change pick an existing page or `*needs new page*` with a proposed path and one-line rationale (never auto-create in propose mode). Draft a unified-diff-shaped before/after fragment per page, anchored to a real H2. If no concrete edit is possible, mark it "*needs prose — author yourself*" and leave it out of the apply set. Before showing, run the self-audit in `references/humanizing-prose.md` on every `+` line (not `-` lines) and show the post-audit drafts.

**D-A5 — Approval gate.** In a round worker's PR there is no picker: apply every draft that has a concrete edit and list the pages touched under the PR body's Rulings. Otherwise show all drafts in one batch, then ask (header `"Docs"`, *"Apply these doc updates?"*): `"Apply all (Recommended)"` / `"Apply selectively"` (free text: page paths) / `"Skip"` (write nothing, don't advance the `docs:` marker) / `"Cancel"` (same as Skip for now). `docs.autoCreate: true` skips this gate and commits directly.

**D-A6 — Commit.** Write the chosen edits without overwriting unrelated content, then one commit:

```
docs: <one-line summary>

- <page>: <one-line per-page change>

Resolves: #12, #15   (file backend: [B07], [F03])
```

`Resolves:` lists the triggering cycle's IDs from the calling brief; omit it on manual entry.

### Docs Mode Principles

- First-run is interactive: always pass D4's question before writing.
- After-work proposes by default; only `docs.autoCreate: true` commits without approval.
