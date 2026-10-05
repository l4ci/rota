# `/rota-ship` (Docs Mode) scaffold conventions

Used by `/rota-ship` (Docs Mode) Step D4 (Propose Tailored Tree) and Step D5 (Scaffold on Approval). The page-naming convention and the seed `.docsignore` are both consumed by `/rota-ship` (Docs Mode) alone — co-located here so the skill's prose stays focused on the UX flow.

## Page-naming convention

Tailored trees follow this layout — already in use across rota's own `docs/` and reusable for other projects:

- **Spine** — top-level pages: `README.md` (TOC), `getting-started.md` (5-minute walkthrough), `faq.md` (common questions, optional).
- **Phase-grouped usage pages** — `docs/usage/<verb-noun>.md`: examples — `picking-work.md`, `running-work.md`, `pausing-and-resuming.md`, `review-and-ship.md`. The verb-noun shape keeps file names self-documenting and groups related actions.
- **Reference material** — `docs/reference/`: full helper / command / config references — `slash-commands.md`, `cli-helpers.md`, `configuration.md`, etc. Reference pages are flat lists/tables; usage pages are narrative walkthroughs.

When the tailored proposal in Step D4 doesn't fit the convention (e.g., a CLI tool with no usage phases, or a library with API references but no walkthroughs), describe the deviation in one line in the proposal — *"This project ships only reference material; no `usage/` pages proposed."* — so the user sees the conscious choice.

## `.docsignore` seed

Seed `.docsignore` at the project's repo root if it doesn't already exist. The seed covers two purposes:

- **Safety boundary** — never read these as source material for public docs (secrets, credentials, internal/private folders).
- **Noise reduction** — common build/dependency dirs kept out so the diff is human-shaped.

```
# rota ship --docs: never read these as source material for public docs
.env
.env.*
**/secrets/**
**/credentials*
**/*.key
**/*.pem
**/internal/**
**/private/**

# Common build/dependency dirs (also kept out for noise reasons)
node_modules/
dist/
build/
.next/
target/
```

The seed is conservative — the user extends it for project-specific paths after the scaffold lands. `/rota-ship` (Docs Mode)'s after-work flow (Step D-A2) filters the diff through `.docsignore` before classifying changes.

## What this reference does NOT cover

- **First-run vs. after-work mode selection.** Lives in `rota-ship/docs-mode.md` Step D1 (mode detection branches on `<docs.path>/` state + `docs.afterWork`).
- **The post-cycle trigger gate.** Lives in `references/post-cycle-trigger-gate.md` — shared with `/rota-work` and `/rota-ship`.
- **The three-mode skeleton shared with `/rota-qa`.** Lives in `references/three-mode-skill-shape.md` — covers the family-level shape and intentional divergences.
