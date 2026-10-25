# Library surface menu

Loaded by [`first-run.md`](first-run.md) step 3 for a library target (no entry point). Entries are examples: `name · command · pass criterion · tool`. The user edits them before approving. Probe each tool with `command -v`; mark it `installed` or `not installed`. Offer not-installed entries unselected; never install.

Pillar keys: `functional`, `security`, `performance`.

## functional

- unit tests · `go test ./...` / `pytest` / `npm test` / `cargo test` · exit 0 · the runner itself (probe it: `go`, `pytest`, `npm`, `cargo`)
- contract tests · tests that call only the public API · exit 0, no unexported symbol used · the runner itself (probe it: `go`, `pytest`, `npm`, `cargo`)

## security

- dependency audit · `govulncheck ./...` / `npm audit --audit-level=high` / `pip-audit` / `cargo audit` · no findings at or above high · govulncheck, npm, pip-audit, cargo-audit

## performance

- benchmark budget · `go test -bench . -run '^$'` / `pytest --benchmark-only` · no benchmark regresses past the stated budget · the runner itself (probe it: `go`, `pytest`, `cargo`)

## Audit rubric

Hand or LLM inspection; no commands, severity-ranked.

- Public API is small, named consistently and documented with an example.
- Errors are typed or documented, not stringly.
- Breaking changes are called out in the changelog.
- README install and first-use steps work from a clean checkout.
