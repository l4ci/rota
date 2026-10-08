# Upstream issues

Under `backlog.backend: "issues"` the open issues on GitHub or GitLab are the backlog: nothing is imported. `/rota-capture` creates items with `rota item create`, and `/rota-ship` closes them with `Closes #N` in the PR body.

`/rota-capture --from-github` / `--from-gitlab` and `rota issues list` were removed (#69): importing duplicated issues once already.

## Remaining verbs

| Verb | Use |
|------|-----|
| `rota issues imported` | File-backend items that still carry a `GH: #N` / `GL: #N` tag |
| `rota issues label <N> --add\|--remove <label>` | Label an upstream issue |
| `rota issues close <N> --commit <hash>` | Close an issue naming the shipping commit |
| `rota issues provider` | `github`, `gitlab` or `unknown` for the origin remote |

On the file backend, add the `GH: #N` tag by hand when creating an item with `rota item create`; `/rota-ship` then emits `Closes #N` and the direct-push path offers a manual-gated close prompt.

## Body sections rota reads

| Heading | Read by | Content |
|---------|---------|---------|
| `## Files` | the `overlap` readiness check | one path or glob per bullet |
| `## Touches` | the `overlap` readiness check; copied into the worker brief | one symbol, endpoint, schema, migration or config key per bullet |
| `## Out of scope` | copied into the worker brief | the ticket's boundary |

See `docs/contributing/contract/backlog.md` for verb shapes, flags and exits, and `skills/references/issue-mode.md` for the issue backend.
