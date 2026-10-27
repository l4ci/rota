---
subsystem: hooks-guard
summary: Claude Code hooks: statusline state dump, Stop/prompt context guard, SessionStart handoff, PreToolUse worker guard, settings merge, plugin hooks/ dir
touched: 2026-10-27
last_mapped_commit: a27543a2d266e573ab6ad79a0cf630c97d854188
---

## Purpose
Logic behind `rota hook` and `rota statusline`: records session state, makes a long orchestrator hand off before context runs out, delivers the handoff at session start, recalls path-scoped knowledge and denies destructive git for workers (PreToolUse), and merges these entries into Claude Code settings files. hooks/ holds the plugin's own SessionStart hook.
Depends on: round (lease probe), worker, roundlease, knowledge, config, fsio, jsonx, rotastate, shlex.

## Entry points
- internal/cli/hook.go:46 — `rota hook` verbs (stop, prompt, guard, session-start, install); :40 `rota statusline`
- internal/hook/guard.go:35 — Guard: reason a worker may not run a Bash line, "" if allowed
- internal/hook/guard_decide.go:33 — DecideGuard: one PreToolUse payload
- internal/hook/stop.go:57 — DecideStop; internal/hook/contextguard.go:35 DecideGuardPrompt
- internal/hook/session.go:15 — ShouldInject: SessionStart handoff injection
- internal/hook/settings.go:43 — LoadSettings; :91 SettingsPath (user/project scopes)
- internal/hook/state.go:26 — State: per-session statusline record
- hooks/hooks.json:1 — plugin SessionStart hook; hooks/session-start.sh:1 version check

## Key files / dirs
- internal/hook/ — apply, contextguard, guard*, identity, recall, session, settings, state, stop, usage
- hooks/ — hooks.json and session-start.sh (plugin only: points at /rota:rota-install when the binary is missing or version differs)
- internal/cli/hook.go — verbs and install (guard scope warning near line 566)

## Stack
Go 1.22 for the hooks; POSIX sh for hooks/session-start.sh. Hook input is the Claude Code JSON payload on stdin.

## Conventions specific here
- Invariants: the guard is a guardrail against a mistaken agent, not a security boundary (a script calling git is not seen); it fails closed only when the line mentions git or gh and cannot be parsed or its repo state cannot be read; DecideGuard requires a non-nil Git func; hook logic is pure or takes clock, env, lease and paths from the caller.
- Worker guard only reaches worker worktrees from user or project scope, not project-local.

## Test commands
- go test ./internal/hook/...
- go test ./internal/cli/ -run 'Hook|Statusline'
- go test . -run Hook (plugin_hook_test.go at repo root); bash test/sections/90_orchestrator_hooks.sh

## Concerns
- hook imports round and worker while keepalive imports hook: tight coupling to round-orchestration.
- guard parses shell lines itself (guard_parse.go), so exotic quoting is a fragile spot.
