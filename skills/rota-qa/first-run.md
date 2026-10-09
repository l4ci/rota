# `/rota-qa` first-run mode

Loaded by `skills/rota-qa/SKILL.md` when `.rota/qa/` is empty for the active scope or the user runs `/rota-qa first-run`. `restructure` reuses steps 1 and 2.

1. **Detect surfaces.** Never assume browser:
   - `package.json` with `next` / `vite` / `react` / `vue` / `svelte` → web UI
   - `App.swift` / `*.xcodeproj` / `Package.swift` with `@main` → macOS/iOS app
   - `openapi.yaml` / `swagger.json` / Express/FastAPI/Hono routes → HTTP API
   - `bin/*` shell entry / `main.py` CLI / `cobra` / `clap` → CLI
   - `lib/` or `src/` with no entry point + tests dir → library (unit + contract only)
   - Mixed → multiple targets, one strategy file each
2. **Detect existing test infra.** `find` for `playwright.config.*`, `cypress.config.*`, `pytest.ini`, `jest.config.*`, `vitest.config.*`, `*XCTest*`, `*.smoke.sh`, `tests/`, `test/`, `__tests__/`, CI workflow files. Note what's wired; missing tooling stays a note, not a proposal.
3. **Probe quality tooling.** Lighthouse / Pagespeed, axe-core / pa11y, ZAP / semgrep configs, dependency audit (`npm audit`, `pip-audit`, `cargo-audit`), perf budgets, contract tests. Presence only; never propose installing anything here.
4. **Propose strategy.** Per target, draft a one-screen strategy and show the user before writing:
   - **Surface** — web, API, CLI, mobile or lib
   - **Watch globs** — paths whose changes trigger after-work QA
   - **Executable checks** — concrete commands grouped by pillar (performance / security / functional). Each: `name` · `command` · `pass criterion`, e.g. `lighthouse --budget-path=.budget.json`, `npm audit --audit-level=high`, `bash test/smoke.sh`.
   - **Audit checks** — usability rubric for hand or LLM inspection (empty states, error recovery, copy clarity, first-run flow). No commands.
   - **Infra requirements** — what must be running for `run` (e.g. `npm run dev` on `:3000`, staging URL, sandbox creds). `run` refuses to start without them.
   - **Out of scope** — explicit non-goals.
5. **Approve & write.** Ask with `Approve as drafted (Recommended)` / `Edit before writing` / `Cancel`. On approval, write `.rota/qa/<target>.md` with frontmatter (`target`, `surface`, `summary`, `created`, `touched`, `watch-globs`) and the five body sections.
6. **Index.** `rota qa index` regenerates the `## Project QA` block in `CLAUDE.md`.
7. **Commit.** `chore(qa): scaffold QA strategy for <target> (.rota/qa/, ## Project QA block)`.
