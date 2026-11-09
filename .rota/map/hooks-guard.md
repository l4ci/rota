---
subsystem: hooks-guard
summary: Claude Code hooks: statusline state dump, Stop/prompt context guard, SessionStart handoff, PreToolUse worker guard, settings merge, plugin hooks/ dir
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose
Logic behind `rota hook` and `rota statusline`: records session state, makes a long orchestrator hand off before context runs out, delivers the handoff at session start, recalls path-scoped knowledge and denies destructive git for workers (PreToolUse), and merges these entries into Claude Code settings files. hooks/ holds the plugin's own SessionStart hook.
Depends on: config, fsio, jsonx, knowledge, rotastate, rotatree, shlex, stamp, status (the `internal/hook` package imports no round or worker code; the lease identity check lives in cli).

## Entry points
- internal/cli/hook.go:42 — `hookCommands`: `rota hook` verbs (stop, prompt, guard, session-start, install); :36 `statuslineCommands`; `hookGuard` at :370, `hookInstall` at :477
- internal/cli/hook_identity.go:19 — `hookIdentify`: is the hook process the orchestrator, from the round lease
- internal/hook/guard.go:35 — Guard: reason a worker may not run a Bash line, "" if allowed
- internal/hook/guard_decide.go:32 — DecideGuard: one PreToolUse payload
- internal/hook/stop.go:33 — DecideStop; internal/hook/contextguard.go:37 DecideGuardPrompt
- internal/hook/handoff.go:121 — ShouldInject: SessionStart handoff injection
- internal/hook/settings.go:43 — LoadSettings; :91 SettingsPath (user/project scopes)
- internal/hook/state.go:26 — State: per-session statusline record
- hooks/hooks.json:1 — plugin SessionStart hook; hooks/session-start.sh:1 version check

## Key files / dirs
- internal/hook/ — apply, contextguard, guard, guard_decide, guard_parse, handoff, payload, recall, settings, state, stop, usage, view
- hooks/ — hooks.json and session-start.sh (plugin only: points at /rota:rota-install when the binary is missing or version differs)
- internal/cli/hook.go — verbs and install; internal/cli/hook_identity.go — lease-based identity

## Stack
Go 1.22 for the hooks; POSIX sh for hooks/session-start.sh. Hook input is the Claude Code JSON payload on stdin.

## Conventions specific here
- Invariants: the guard is a guardrail against a mistaken agent, not a security boundary (a script calling git is not seen); it fails closed only when the line mentions git or gh and cannot be parsed or its repo state cannot be read; DecideGuard requires a non-nil Git func; hook logic is pure or takes clock, env, lease and paths from the caller.
- Orchestrator-or-not is decided in cli (`hookIdentify` over `roundlease.Env.Holds`), then passed into the pure `Decide*` functions.
- Worker guard only reaches worker worktrees from user or project scope, not project-local.

## Test commands
- go test ./internal/hook/...
- go test ./internal/cli/ -run 'Hook|Statusline'
- go test . -run Hook (plugin_hook_test.go at repo root); bash test/sections/90_orchestrator_hooks.sh

## Concerns
- `keepalive`, `hvmigrate` and `doctor` import `hook`, so it is shared by workers, migration and preflight code.
- guard parses shell lines itself (guard_parse.go), so exotic quoting is a fragile spot.
