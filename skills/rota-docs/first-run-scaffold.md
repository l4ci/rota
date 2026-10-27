# First-run scaffold for `/rota-docs`

Loaded by `skills/rota-docs/SKILL.md` Step D1 when `<docs.path>/` is missing or empty. Steps D2 to D6.

### Step D2 — Read Project Signals

One parallel batch: README, root manifests, one-level listings of `bin/` and `src/` (shape only), `git log --oneline -20`, other root `.md` files (CHANGELOG, CONTRIBUTING, LICENSE: note, don't duplicate). Don't dump any of it to the user.

### Step D3 — Form Hypothesis (silent)

Classify the project (CLI, library, web app/service, plugin, framework, data project, mixed) and name its user-facing surface (commands and flags, public API, routes and UI, host integration points). Don't narrate; it shapes D4.

### Step D4 — Propose Tailored Tree

Show a plain-markdown tree under `<docs.path>/` with a one-line purpose per file, tailored to the type and surface (spine + usage + reference layout, `references/docs-conventions.md` page-naming section). If it departs from that layout, say so in one line. Ask (header `"Scaffold"`, *"Approve this docs structure?"*): `"Approve as proposed (Recommended)"` / `"Edit"` (free text; revise and re-ask) / `"Minimal — README.md + getting-started.md only"` / `"Cancel"` (print *"Scaffold cancelled. Run `/rota-docs` again whenever you're ready."* and exit). On ambiguity, default to Recommended, naming it.

### Step D5 — Scaffold on Approval

Write each file with a title heading, a one-line purpose comment, and empty section stubs (`## What you'll learn`, `## Steps`): no invented content. `<docs.path>/README.md` is the only page with real content, a TOC linking every other page. Seed `.docsignore` from `references/docs-conventions.md` (`.docsignore` seed section) if absent. Never overwrite an existing file. Then `rota config set docs.afterWork true` unless already true: approving the scaffold is opting into the docs flow, so no second question.

### Step D6 — Closing Summary

```
Scaffolded <docs.path>/ — N pages.

Files:
  - <docs.path>/README.md  (index / TOC)
  - <docs.path>/getting-started.md
  - ...

Next:
  Run /rota-work on a feature, then /rota-docs will fill in pages from the changes.
  Or write <docs.path>/getting-started.md yourself first to set the voice.
```
