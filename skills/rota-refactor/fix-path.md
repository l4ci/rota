# Fix path (`--fix`)

Fix only the findings the user named, or all filed in this run if they said "all". With `refactor.confirmBeforeExecute` true, confirm the list once with `AskUserQuestion` first.

1. **Group.** Independent files run in parallel; one agent per file when files overlap; order real dependencies.
2. **Dispatch** `standard` subagents. Each brief names the exact files, the problem, the chosen approach (the design from `--designs` for structural items), and the acceptance criteria from the issue. Constraints: read before editing, minimal diff, no unrelated cleanup.
3. **Verify** with one subagent on the main session's model (`models.orchestrator`) that reads the changed files, runs every `refactor.verifyCommands` entry verbatim and reports PASS / FAIL / CONCERN per fix. A non-zero exit is a FAIL. With no commands configured, say the tree was not gated. Re-dispatch FAILs once; report what still fails.
4. **Commit** once: stage explicit paths, subject `refactor: <summary>`, body listing the issues (`Closes #<n>` each). Then zero the pressure counter:

```bash
rota refactor reset
```

Report the commit and closed issues in a few lines; no recap of exploration or designs.
