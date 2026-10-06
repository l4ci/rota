# `/rota-qa` first-run mode

Loaded by `skills/rota-qa/SKILL.md` when `.rota/qa/` is empty for the active scope or the user runs `/rota-qa first-run`. `restructure` mode reuses steps 1 and 2 below.

Run when `.rota/qa/` is empty for the active scope (umbrella: per-repo; single-repo: when no `.rota/qa/*.md` files exist).

1. **Detect surfaces.** Inspect what kind of product this is — never assume browser:
   - `package.json` with `next` / `vite` / `react` / `vue` / `svelte` → web-UI surface
   - `App.swift` / `*.xcodeproj` / `Package.swift` with `@main` → macOS/iOS app surface
   - `openapi.yaml` / `swagger.json` / Express/FastAPI/Hono routes → HTTP-API surface
   - `bin/*` shell entry / `main.py` CLI / `cobra` / `clap` Rust → CLI surface
   - `lib/` or `src/` with no entry point + tests dir → library surface (unit + contract only)
   - Mixed → multiple targets, one strategy file each
2. **Detect existing test infra.** `find` for: `playwright.config.*`, `cypress.config.*`, `pytest.ini`, `jest.config.*`, `vitest.config.*`, `*XCTest*`, `*.smoke.sh`, `tests/`, `test/`, `__tests__/`, CI workflow files. Note what's already wired; missing tooling stays a note, not a proposal.
3. **Probe quality tooling.** Check for: Lighthouse / Pagespeed, axe-core / pa11y, ZAP / semgrep configs, dependency audit (`npm audit`, `pip-audit`, `cargo-audit`), perf budgets, contract tests. Presence only — never propose installing anything in this mode.
4. **Propose strategy.** For each target, draft a one-screen strategy with these sections — show the user before writing:
   - **Surface** — what kind of thing this is (web, API, CLI, mobile, lib)
   - **Watch globs** — paths whose changes should trigger after-work QA
   - **Executable checks** — runners with concrete commands, grouped by pillar (performance / security / functional). Each entry: `name` · `command` · `pass criterion`. Examples: `lighthouse --budget-path=.budget.json` · `pa11y http://localhost:3000` · `npm audit --audit-level=high` · `bash test/smoke.sh` · `playwright test --grep @smoke`.
   - **Audit checks** — usability dimensions to inspect by hand or LLM (empty states, error recovery, copy clarity, first-run flow). Rubric, no commands.
   - **Infra requirements** — what must be running for `run` mode (e.g. `npm run dev` on `:3000`, deployed staging URL, sandbox creds). Skill refuses to run if these aren't met.
   - **Out of scope** — explicit non-goals (e.g. "no load testing", "no real-payment flows").
5. **Approve & write.** Use `AskUserQuestion` with `Approve as drafted (Recommended)` / `Edit before writing` / `Cancel`. On approval, write `.rota/qa/<target>.md` with frontmatter (`target`, `surface`, `summary`, `created`, `touched`, `watch-globs`) and the five body sections.
6. **Index.** Run `rota qa index` to regenerate the `## Project QA` block in `CLAUDE.md`.
7. **Commit.** `chore(qa): scaffold QA strategy for <target> (.rota/qa/, ## Project QA block)`.
