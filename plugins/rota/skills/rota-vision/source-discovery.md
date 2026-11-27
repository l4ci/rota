# Step 2 — Source discovery

Find a concept, spec or PRD document no milestone covers yet, so Step 3 can offer to seed milestones from it. Silent, no flag, no verb: you list and judge.

## Candidates

`git ls-files '*.md'`, then drop `.rota/`, `.worktrees/`, `docs/contributing/`, vendored trees (`node_modules/`, `vendor/`, `third_party/`), `CHANGELOG*`, `LICENSE*`, `AGENTS.md` and `CLAUDE.md`. Keep a file when its name or first heading reads as a plan: spec, concept, PRD, design, vision, roadmap, plan, requirements, brief.

`README.md` counts when most of it is plan-shaped, judged by content, not only headings: it states goals, must-haves, constraints, nice-to-haves, a roadmap or milestones, whether under such headings or as plain paragraphs under a single title. A brief with one `# Title` and Must-haves / Constraints paragraphs qualifies. A README that mostly documents a working product (install, usage, API reference) is not a source. When it is a toss-up, leave it out.

## Covered

A candidate is covered when an existing milestone's plan carries a `Source: <path>` line naming it (`Source: <path>#<heading>` covers that chunk). Read the lines from the plans Step 2 already loaded (`rota milestone show <MNN>`); issue mode reads the tracking-issue body, never `.rota/milestones/*.md`. A document with some chunks covered stays a candidate for the rest.

## Result

- **No uncovered candidate** (none found, or all covered): run Step 3 unchanged.
- **One or more uncovered candidates:** Step 3 opens with `I found <path> (<title>)` and offers *Seed from it* as the Recommended option. With several, offer a pick between them.

## Seeding

Read the whole document. Draft one Step 6 block per coherent chunk, with title, goal, acceptance and dependencies drawn from the text; park what is too vague for acceptance under Not yet specified. Drop each chunk an existing milestone already covers, by `Source:` line or by goal, and show the existing milestones beside the proposal so the user sees what is left out. Never propose a duplicate of an existing milestone; extend it in Edit mode instead. Each new plan carries `Source: <path>#<heading>`, written in Step 7. Challenge and confirmation (Steps 5-6) run as usual.
