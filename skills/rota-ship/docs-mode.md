# Docs Mode (`/rota-ship --docs`)

Loaded by `skills/rota-ship/SKILL.md` Step 0 (`--docs`) and Step 8.6 (`docs.afterWork`). Step labels D1–D6 and D-A1–D-A6 are cited elsewhere: keep them.

### Modes

| Detected when | Mode |
|---|---|
| `<docs.path>/` missing or empty | First-run (discovery + scaffold) |
| Post-cycle, from `/rota-work` or Step 8.6 | After-work (propose updates) |
| `/rota-refactor`, or `--docs restructure` | Restructure (audit + reorganize) |
| Manual invoke, no signal | First-run if missing; else after-work in manual mode (trigger gate bypassed, Step D1) |

Docs Mode and `/rota-qa` share a three-mode skeleton and diverge on artifact root, gate strength and authoring tier: `references/three-mode-skill-shape.md`.

> **One `docs/` tree per project**, at the umbrella root or inside one chosen sub-repo (`docs.repo` records the choice). Never several trees, never a per-sub-repo `docs/` beside an umbrella one, never writes to more than one target. `docs/` is the consumer-facing surface: contributor and contract content lives in the owning `rota-*/SKILL.md`, and cross-refs from `docs/` and `README.md` point at `docs/` pages or specific SKILL.md files, never a central internals doc.

### Step D1 — First-Run Detection

Read `docs.path` (default `"docs"`). Missing or empty `<docs.path>/` means first-run (D2–D6). Otherwise branch on `docs.afterWork`:

- **`true`**: Route to the After-work sub-flow as manual mode. Print *"After-work mode is on. Checking docs against changes since the last `docs:` commit."* then start at D-A1. The user running `/rota-ship --docs` by hand is the trigger, so the trigger gate is bypassed and D-A6 omits `Resolves:`.
- **`false`**: ask (header `"After-work"`, *"`<docs.path>/` is initialized but `docs.afterWork` is off. Enable after-work mode? `/rota-work` and `/rota-ship` will then propose doc updates after each cycle."*): `"Enable (Recommended)"` runs `rota config set docs.afterWork true`, prints *"After-work mode enabled."* and exits; `"Leave off"` exits. Ambiguous answers mean Leave off: never flip a flag the user did not ask for.

Phases: *Mode select* (D1), *Inspect docs/* (D2), *Discover topics* (D3), *Propose plan* (D4), *Write/update* (D5), *Cross-link* (D6), *Report*. Track these phases with the host's task tool if it has one.

### Step D2 — Read Project Signals

One parallel batch: `README.md`/`README.rst`; root manifests (`package.json`, `pyproject.toml`, `Cargo.toml`, `go.mod`, `composer.json`, `setup.py`, `*.gemspec`, `Gemfile`); one-level listings of `bin/` and `src/` (shape only, no contents); `git log --oneline -20`; other root `.md` files (CHANGELOG, CONTRIBUTING, LICENSE: note, don't duplicate). Don't dump any of it to the user.

### Step D3 — Form Hypothesis (silent)

Classify the project: CLI tool (`bin/*`, flag examples), library (public API, no CLI), web app/service (routes, server entry), plugin/extension (host-system manifest), framework (primitives, "core concepts"), data project (pipelines, notebooks), or "mixed". Name the user-facing surface for that type: commands and flags, public API, routes and UI, host integration points. Don't narrate the analysis; it shapes D4.

### Step D4 — Propose Tailored Tree

Show a plain-markdown tree under `<docs.path>/` with a one-line purpose per file, tailored to the type and surface (spine + usage + reference layout, `references/docs-conventions.md` page-naming section). If the tailored tree departs from that layout, say so in one line (*"This project ships only reference material; no `usage/` pages proposed."*). Ask (header `"Scaffold"`, *"Approve this docs structure?"*): `"Approve as proposed (Recommended)"` / `"Edit"` (free text; revise and re-ask) / `"Minimal — README.md + getting-started.md only"` / `"Cancel"` (print *"Scaffold cancelled. Run `/rota-ship --docs` again whenever you're ready."* and exit). On ambiguity, default to Recommended, naming it.

### Step D5 — Scaffold on Approval

Write each file with a title heading, a one-line purpose comment matching the project's `.md` style, and honest empty section stubs (`## What you'll learn`, `## Steps`): no invented content. `<docs.path>/README.md` is the only page with real content, a TOC linking every other page. Seed `.docsignore` from `references/docs-conventions.md` (`.docsignore` seed section) if absent. Never overwrite an existing file. Then `rota config set docs.afterWork true` unless already true: approving the scaffold is opting into the docs flow, so no second question.

### Step D6 — Closing Summary

```
Scaffolded <docs.path>/ — N pages.

Files:
  - <docs.path>/README.md  (index / TOC)
  - <docs.path>/getting-started.md
  - ...

Next:
  Run /rota-work on a feature, then /rota-ship --docs will fill in pages from the changes.
  Or write <docs.path>/getting-started.md yourself first to set the voice.
```

### Docs After-Work Sub-Flow

Entered from Step 8.6 or manually per Step D1.

**D-A1 — Trigger gate.** For post-cycle entries apply `references/post-cycle-trigger-gate.md`. Manual entry bypasses the gate (Step D1). If `<docs.path>/` is missing or empty, print *"`/rota-ship --docs` not yet initialized — run `/rota-ship --docs` to scaffold."* and exit; never scaffold mid-cycle.

**D-A2 — Gather context** in one parallel batch: `git log --oneline <last-docs-marker>..HEAD` (marker is the last `docs:` commit; fall back to the last 20 commits); `git diff <marker>..HEAD` over paths not matched by `.docsignore`; the `<docs.path>/` tree and each page's H1/H2 outline.

**D-A3 — Classify changes.** Per remaining diff file: user-facing surface (doc-relevant) or internal-only. Doc-relevant hints: `bin/*` entry points, public API exports, route handlers, CLI flags, config keys, plugin-manifest entries, README-shaped behavior. Internal hints: tests, build and CI and lint config, unexported helpers, behavior-neutral refactors, dependency bumps. Judgment leads; the hints do not gate. Nothing doc-relevant: print *"No user-facing changes since last `docs:` commit. Skipping."* and exit.

**D-A4 — Map to pages and draft.** Per doc-relevant change pick an existing page (e.g. `usage/<command>.md` for a flag change) or `*needs new page*` with a proposed path and one-line rationale (never auto-create in propose mode). Draft a before/after fragment per page as a unified-diff-shaped block anchored to a real H2. If no concrete edit is possible, mark it "*needs prose — author yourself*" and leave it out of the apply set. Before showing, run the self-audit in `references/humanizing-prose.md` on every `+` line (not `-` lines) and show the post-audit drafts.

**D-A5 — Approval gate.** Show all drafts in one batch, then ask (header `"Docs"`, *"Apply these doc updates?"*): `"Apply all (Recommended)"` / `"Apply selectively"` (free text: page paths) / `"Skip"` (write nothing, don't advance the `docs:` marker) / `"Cancel"` (same as Skip for now). `docs.autoCreate: true` skips this gate and commits directly.

**D-A6 — Commit.** Write the chosen edits without overwriting unrelated content, then one commit:

```
docs: <one-line summary>

- <page>: <one-line per-page change>

Resolves: #12, #15   (file backend: [B07], [F03])
```

`Resolves:` lists the triggering cycle's IDs from the calling brief; omit it on manual entry.

### Docs Mode Principles

- First-run is interactive: always pass D4's question before writing.
- Stubs are honest empty sections; the user fills the substance.
- `<docs.path>/README.md` is the spine, `.docsignore` the safety boundary.
- After-work proposes by default; only `docs.autoCreate: true` commits without approval.
