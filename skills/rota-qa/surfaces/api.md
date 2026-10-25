# API surface menu

Loaded by `first-run.md` step 3 for an HTTP API target. Entries are examples: `name · command · pass criterion · tool`. The user edits them before approving. Probe each tool with `command -v`; mark it `installed` or `not installed`. Offer not-installed entries unselected; never install.

## Functional

- contract tests · `schemathesis run openapi.yaml --base-url <url>` or `dredd` · no schema violations · schemathesis, dredd
- happy-path smoke · `curl -fsS <url>/health` · 200 and expected body · curl

## Security

- auth probes · request each protected route without a token and with a bad one · 401 / 403, never 200 · curl
- negative-path probes · malformed body, oversized body, wrong content type · 4xx, no 5xx and no stack trace in the body · curl
- dependency audit · `npm audit --audit-level=high` / `pip-audit` / `govulncheck ./...` · no findings at or above high · npm, pip-audit, govulncheck

## Performance

- latency budget · `k6 run script.js` or `hey -n 200 -c 10 <url>/<route>` · p95 under the stated budget · k6, hey

## Audit rubric

Hand or LLM inspection; no commands, severity-ranked.

- Error responses share one shape and say what to fix.
- Status codes and verbs follow the documented contract; no undocumented routes.
- Pagination, filtering and versioning are consistent across resources.
- The OpenAPI document matches observed behavior.
