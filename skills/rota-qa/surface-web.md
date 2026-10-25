# Web surface menu

Loaded by [`first-run.md`](first-run.md) step 3 for a web UI target. Entries are examples: `name · command · pass criterion · tool`. The user edits them before approving. Probe each tool with `command -v` (or `npx --no-install <tool> --version`); mark it `installed` or `not installed`. Offer not-installed entries unselected; never install.

Pillar keys: `functional`, `security`, `performance`, `ui-a11y`.

## functional

- Playwright flows · `npx playwright test` · all specs pass · playwright
- smoke route check · `curl -fsS -o /dev/null -w '%{http_code}' <url>/` · 200 · curl

## security

- dependency audit · `npm audit --audit-level=high` · no findings at or above high · npm
- static scan · `semgrep --config auto --error` · exit 0 · semgrep

## performance

- Lighthouse budget · `lighthouse <url> --budget-path=.budget.json --quiet` · budget met · lighthouse

## ui-a11y

- axe scan · `npx @axe-core/cli <url> --exit` · no violations · axe-core
- pa11y scan (alternative to axe) · `pa11y <url>` · exit 0 · pa11y
- Playwright screenshots · `npx playwright test --update-snapshots=none` or capture per breakpoint to `.rota/qa-runs/` · captured for the visual audit; diff within threshold when baselines exist · playwright

## Audit rubric (visual audit)

Run on the Playwright screenshots, no commands, severity-ranked.

- Empty, error and loading states exist and read clearly.
- Visual hierarchy: one primary action per view, headings ordered.
- Contrast and focus visibility meet WCAG AA.
- Responsive breakpoints (mobile, tablet, desktop) hold layout without overflow.
