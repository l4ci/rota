---
subsystem: hooks-guard
summary: Claude Code hooks: statusline state dump, Stop/prompt context guard, SessionStart handoff, PreToolUse worker guard, settings merge, plugin hooks/ dir
touched: 2026-11-20
last_mapped_commit: 394b73c9c13b7ed81d37a069763e1b19c9a5482d
---

## Purpose
Logic behind `rota hook` and `rota statusline`: records session state, makes a long orchestrator hand off before context runs out, delivers the handoff at session start, recalls path-scoped knowledge and denies destructive git for workers (PreToolUse), and merges these entries into Claude Code settings files. hooks/ holds the plugin's own SessionStart hook.
Depends on: config, fsio, jsonx, keepstate, knowledge, rotastate, rotatree, shlex, stamp, status (the `internal/hook` package imports no round or worker code; the lease identity check lives in cli).

## Entry points
- internal/cli/hook.go:38 — `hookCommands`: `rota hook` verbs (stop, prompt, guard, session-start, install); :32 `statuslineCommands`; `hookGuard` at :420; `hookInstall` at internal/cli/hook_install.go:65
- internal/cli/hook_identity.go:19 — `hookIdentify`: is the hook process the orchestrator, from the round lease
- internal/hook/guard.go:45 — Guard: reason a worker may not run a Bash line, "" if allowed
- internal/hook/guard_decide.go:35 — DecideGuard: one PreToolUse payload
- internal/hook/stop.go:34 — DecideStop; internal/hook/contextguard.go:38 DecideGuardPrompt (`DecideGuardStop` :73); internal/hook/prompt.go:23 `DecidePromptDigest`
- internal/hook/handoff.go:116 — ShouldInject: SessionStart handoff injection
- internal/hook/settings.go:51 — LoadSettings; :112 SettingsPath (user/project scopes)
- internal/hook/state.go:30 — State: per-session statusline record
- hooks/hooks.json:1 — plugin SessionStart hook; hooks/session-start.sh:1 version check

## Key files / dirs
- internal/hook/ — apply, contextguard, guard, guard_decide, guard_parse, handoff, payload, prompt, recall, settings, state, stop, usage, view
- internal/roundhook/ — `roundhook.go` only: the orchestrator-facing round view the Stop and prompt hooks print (`Digest`, `ExitDigest`, `NeedsWatch`, `StopBlock`); reads the registry and the `roundlease.Armed` marker, nothing else of the loop; imports escalation, landfreeze, rotastate, roundlease, worker; called from internal/cli (the watcher loop stays in round-orchestration)
- internal/keepstate/ — records the keepalive supervisor and the Stop hook share (see workers); `hook` imports it
- hooks/ — hooks.json and session-start.sh (plugin only: points at /rota:rota-install when the binary is missing or version differs)
- internal/cli/hook.go — verbs; internal/cli/hook_install.go — `hook install`; internal/cli/hook_identity.go — lease-based identity

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
- `hvmigrate`, `doctor` and the cli (worker, limit, keepalive, round start verbs) import `hook`, so it is shared by migration, preflight and verb code. `roundhook` is the round-aware half of the Stop hook and does import worker and roundwatch; `hook` itself does not.
- guard parses shell lines itself (guard_parse.go), so exotic quoting is a fragile spot.
