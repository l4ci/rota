# Close upstream issues (Step 13)

Closes upstream issues shipped in this release but still open (pushed straight to main, or `/rota-ship` chose "leave open"). Same shape as `/rota-ship` Step 6c, scoped to the release range. Skip in `--dry-run`.

`rota issues imported --json --open-only` lists candidates (already-closed or unresolvable ones are dropped). Empty `data.entries`: skip silently.

> **Manual gate — closing public upstream issues.** Closing posts a comment and changes issue state remotely. **Always manual** — never auto-invoked, regardless of `autonomy.level`. See `references/manual-gates.md`.

Ask (single-select): header `"Close"`, *"Close N upstream issue(s) released in `v<new_version>`? (`<#N list>`)"*, options `Yes, close all` / `Pick subset` / `No, leave open`.

- **Close all:** run `rota issues close <N> --commit <release-commit-sha> --item <ID> [--repo <name>]` per candidate, in one parallel batch. `--repo` only for entries with a non-null `repo`.
- **Pick subset:** multiSelect `AskUserQuestion` (header `"Pick issues"`, *"Which issue(s) should be closed?"*, options `"#N (item <ID>)"`, chunk at 4), then close the selection as above.
- **Leave open:** print *"Skipping upstream issue close — N issue(s) left open. Run `gh issue close <N>` / `glab issue close <N>` manually if desired."*
