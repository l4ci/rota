---
name: git-guardrail-hook
branch: spike/git-guardrail-hook
status: done
finished: 2026-10-10
created: 2026-10-10
---

# spike/git-guardrail-hook

## Question

Can a worker's "never merge" and "stage explicit paths" rules (prose in `skills/references/worker-contract.md`) be enforced lightly: native `permissions.deny`, or a role-aware PreToolUse hook installed by `rota hook install`? (#685)

## What was tried

1. Read the Claude Code docs on permissions, hooks, worktrees and subagents (via a docs-lookup subagent, 2026-10-10) for deny-rule matching, hook payload and mode behaviour.
2. Read how rota launches workers (`internal/harness/claude.go`, `internal/worker/portblock.go`) and installs hooks (`internal/hook/settings.go`, `internal/cli/hook.go`).
3. Ran one live check: `claude -p --dangerously-skip-permissions --settings <file>` with a PreToolUse Bash hook (exit 2) and `ROTA_SLOT=sam` exported, then asked it to run `echo`.

## Findings

Facts about rota today:

- Workers run `claude --model <m> --dangerously-skip-permissions` and every slot's tab gets `ROTA_SLOT`, `ROTA_PORT_BASE`, `ROTA_PORT_BLOCK`, `ROTA_DB_SUFFIX` (`worker/portblock.go`). The orchestrator has no `ROTA_SLOT`. That env var is the role signal.
- `rota hook install` defaults to `--scope project-local` (`<root>/.claude/settings.local.json`). That file is untracked, so a worker worktree under `.worktrees/` does not have it. A guard installed at the default scope would never reach a worker. `project` (tracked) and `user` scopes do reach it.
- `rota hook` has Stop, UserPromptSubmit and SessionStart entries, all marker-tagged (`# rota-hook`), merged and removed by `Install`/`Uninstall`. A fourth marked entry fits the existing machinery.

Facts about Claude Code (docs, plus the live check where marked):

- Deny rules still block under `--dangerously-skip-permissions`. Compound commands are split on `&&`, `||`, `;`, `|`, `&`, newline before matching; leading `FOO=x` and `timeout`/`nice`/`nohup`/`time`/`command`/bare `xargs` are stripped.
- Deny rules do **not** see through `git -C <dir> push`, `git -c k=v push`, `/usr/bin/git`, `bash -c '...'`, `sudo`, `xargs -n1` or variable indirection. The docs call argument-constraining Bash patterns "fragile" and "not a security boundary".
- Patterns are glob/prefix on text: `git push --force *` misses `git push origin main --force` and `-f`; `git push * --force*` also matches `--force-with-lease`, which the policy must allow on the worker's own branch.
- A deny rule is static per settings file. It cannot say "own branch", "inside my worktree" or "unless I am the orchestrator". The orchestrator must run `gh pr merge`, so a deny for it can live only in a file the orchestrator does not read (per-worktree `settings.local.json`, which rota would have to write at dispatch).
- **Checked live:** a PreToolUse hook fires under `bypassPermissions`, receives `tool_input.command`, `cwd` and `permission_mode` as JSON on stdin, inherits `ROTA_SLOT`, and exit 2 blocks the call with stderr shown. (The docs left hook-under-bypass unstated.)
- Hooks also fire for subagent tool calls (payload carries `agent_id`), so a worker's own subagents are covered.
- A hook that denies cannot be overridden by an allow rule or a lower scope.

Policy table (AC-2), and which option can express it:

| Rule | `permissions.deny` | Hook (argv-parsed) |
|---|---|---|
| Push own branch, incl. `--force-with-lease` | cannot say "own" | allow when every refspec resolves to the current branch |
| Force-push (`-f`, `--force`, `--force-with-lease`, `+ref`, `--mirror`, `--delete`, `:ref`) to any other ref | misses `-f`/`-C`/arg order, can't exempt own branch | yes |
| `reset --hard` outside the worker's worktree | no (no path semantics) | yes: resolve effective repo (`-C`, `--git-dir`, `--work-tree`, payload cwd) and compare toplevel to the slot worktree |
| `branch -D` / `-d -f` / `--delete --force` | text only, bypassed by `-C` | yes |
| `clean -f`, `-fd`, `--force` | text only | yes |
| `git add -A`, `--all`, `.` | text only | yes |
| `gh pr merge` | blocks orchestrator too unless per-worktree file | yes, gated on `ROTA_SLOT` |

## Decision

**Recommend the role-aware PreToolUse hook. Do not use `permissions.deny` as the mechanism.** Reasons:

1. Three of the seven AC-2 rules (own-branch push, reset outside worktree, gh pr merge for workers only) cannot be written as static text rules at all.
2. The text rules that can be written are bypassed by `git -C`, `bash -c`, `sudo` and flag order, the exact cases AC-3 names.
3. Role comes free from `ROTA_SLOT`; the hook is a no-op for the orchestrator and for human sessions, so it is safe at user scope.
4. The hook layer is the only one verified to run under `--dangerously-skip-permissions` with the env intact.

`permissions.deny` is rejected as the primary mechanism, not as an extra layer: it adds nothing the hook lacks and doubles the places the policy lives. Skip it.

### Shape

- New verb `rota hook guard`: PreToolUse hook, matcher `Bash`. Reads the payload, returns at once (exit 0) unless `ROTA_SLOT` is set. Exit 2 with a one-line reason on a violation.
- Parser works on argv, not grep. Tokenise the shell line (quotes, escapes), split on `&&`, `||`, `;`, `|`, `&`, newline, recurse into `$(...)`, backticks, `bash|sh|zsh -c '<script>'`, and unwrap `sudo`, `env`, `time`, `nice`, `nohup`, `timeout`, `command`, `xargs`. Then for each simple command: strip env assignments, resolve the program (basename `git`/`gh`), skip git global options (`-C <dir>`, `-c <k=v>`, `--git-dir`, `--work-tree`, `--exec-path`, `--namespace`) while recording `-C`/`--git-dir`/`--work-tree` for the repo-resolution step, and apply the table above to subcommand and flags.
- Fail closed only when the line mentions `git` or `gh` and cannot be parsed or the effective repo/branch cannot be resolved (deny with "guard could not parse: <why>; run the command plainly"). Any other parse failure is a pass, so the guard never blocks a build step.
- Own branch = `git -C <effective dir> branch --show-current` at hook time. A `git push` with no refspec is allowed unless it carries a force flag with an upstream other than the current branch. `HEAD` and `HEAD:<current>` count as own.
- Worker's worktree = the `.worktrees/<ROTA_SLOT>` ancestor of the payload `cwd`; if `cwd` is not under one, fall back to `git worktree list`. `reset --hard` is allowed only when the effective repo's toplevel (after `-C`/`--git-dir`/`--work-tree`) equals it.
- Install: add `PreToolUse` (matcher `Bash`, command `rota hook guard # rota-hook`) as a fourth marked entry in `Install`/`Uninstall`/`MarkedEvents`. No new flag is needed: the entry is inert without `ROTA_SLOT`. What does need a change is scope: the guard must sit in `project` or `user` scope to reach worktrees, so `rota hook install` should warn (and `rota doctor` should fail) when the guard exists only in `project-local`.
- `rota doctor`: new `guard-hook` check next to `stop-hook`: fails when no `PreToolUse` marked entry is in `project` or `user` scope (project-local alone does not count); warns when `work.workerCommand` is a Codex harness (no guard; see limits).

### Test list (AC-3)

Must deny (as a worker, `ROTA_SLOT` set):

- `git push --force origin main`, `git push -f origin main`, `git push origin +main`, `git push --force-with-lease origin main`, `git push origin :main`, `git push --delete origin main`, `git push --mirror`
- `git -C /home/vo/dev/rota push --force origin main` and `git -C ../other push -f` (the `-C` bypass)
- `git -c push.default=matching push --force`
- `/usr/bin/git push --force origin main`, `sudo git push --force`
- chained: `git add a && git push --force origin main`, `true; git push -f origin main`, `git status | cat; git branch -D x`
- `bash -c 'git push --force origin main'`, `sh -c "git -C x push -f"`, `echo $(git push -f origin main)`, `xargs git push --force`
- `git reset --hard` with `-C` or cwd outside the slot worktree; `git --work-tree=/x reset --hard`
- `git branch -D x`, `git branch -d -f x`, `git branch --delete --force x`
- `git clean -f`, `git clean -fd`, `git clean -xdf`
- `git add -A`, `git add --all`, `git add .`, `git add -u .` (treat `.`/`-A`/`--all` as the ban; `-u` plain is allowed)
- `gh pr merge 12`, `gh pr merge --squash`, `FOO=1 gh pr merge 12`, `cd x && gh pr merge`

Must allow:

- `git push origin HEAD`, `git push -u origin <current-branch>`, `git push --force-with-lease origin <current-branch>`, `git push --force-with-lease` (upstream is the current branch)
- `git -C <own worktree> push origin <current-branch>`
- `git reset --hard origin/main` inside the slot worktree; `git reset --soft HEAD~1`
- `git add path/to/file.go`, `git add -u`, `git branch -d merged-branch`, `git clean -n`
- `git log --grep='push --force'`, `echo "git push --force"` (text in an argument is not a command)
- With `ROTA_SLOT` unset (orchestrator): every line above, including `gh pr merge` and force-push.
- Non-Bash tool calls and non-git commands pass untouched.

### Limits to state in docs

- A guardrail, not a boundary. A script or `python -c` that calls git, or a binary that shells out, is not seen. Same stance as the docs' own caveat for deny rules.
- Codex workers: no guard. Codex hooks are keyed differently (rota already needs `--dangerously-bypass-hook-trust` for its prompt check); a Codex guard is a separate follow-up and is out of scope for the first slice.
- Other merge routes (`gh api .../merge`, `rota` verbs that merge) are not blocked by the first slice; `rota worker` verbs are the intended channel and stay allowed.
- The guard reads the process env; a worker could `unset ROTA_SLOT` in a command, but the hook sees the Claude process env, not the command's, so that does not help it.

## Follow-up

Implementation issue filed: #702.
