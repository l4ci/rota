# CLI surface menu

Loaded by `first-run.md` step 3 for a CLI target. Entries are examples: `name · command · pass criterion · tool`. The user edits them before approving. Probe each tool with `command -v`; mark it `installed` or `not installed`. Offer not-installed entries unselected; never install.

## Functional

- help and usage audit · `<bin> --help` and each subcommand's `--help` · exit 0, usage text lists every flag · none
- exit-code contract · run a success, a bad-flag and a missing-input case · 0, 2 and non-zero as documented · none
- golden output · `<bin> <cmd> | diff - testdata/<cmd>.golden` · no diff · diff
- `--json` stability · `<bin> <cmd> --json | jq -e .` · valid JSON, keys unchanged · jq

## Security

- dependency audit · `govulncheck ./...` / `npm audit --audit-level=high` / `pip-audit` / `cargo audit` · no findings at or above high · govulncheck, npm, pip-audit, cargo-audit
- secret scan · `gitleaks detect --no-git` · no leaks · gitleaks

## Performance

- startup budget · `hyperfine --warmup 3 '<bin> --version'` · mean under the stated budget (e.g. 100 ms) · hyperfine

## Audit rubric

Hand or LLM inspection; no commands, severity-ranked.

- Error messages say what went wrong and what to do next.
- Flag naming is consistent across subcommands (same word, same short form).
- `--json` output is stable and documented; human output goes to stdout, diagnostics to stderr.
- First-run experience: a new user reaches a working command from `--help` alone.
