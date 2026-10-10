# Reuse proof (Step 4)

Loaded by `SKILL.md` Step 4 before dispatching runners.

**Reuse proof; the merge gate is the only full run.** The merge gate already ran the full suite (`test.full`) on the merged tree. Before dispatching, run `rota proof show <ID> --json` per item: a PASS row for the same check at the current `git rev-parse HEAD` (its `sha`) is reused, not re-run. Where a strategy check is the gate's own command, the gate's PASS at that sha is the QA run. Dispatch runners only for checks with no PASS at this sha (browser, lighthouse, audit and other surface checks). Mark reused rows as such in the report.
