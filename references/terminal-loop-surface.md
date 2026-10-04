# Terminal-loop-surface block

Canonical bash block referenced by every terminal-path skill that surfaces `[Auto:Loop]` decisions before halting. Per the F19 terminal-path-only convention, this surface fires only from terminal paths — never from a loop-internal step.

## The block (cite this from SKILL.md)

Run after the terminal-path framing paragraph, before any final user message:

```bash
rota decisions auto-since   # empty stdout when nothing matches
rota status loop clear      # clear the session marker so the next /rota-work loop entry stamps a fresh start
```

If `rota decisions auto-since` produces no output, skip silently — there's nothing to surface.

## How to cite from a SKILL.md

In the terminal-path step, replace the inline bash block + skip-silently sentence with this one-line citation:

> Surface any `[Auto:Loop]` decisions per `references/terminal-loop-surface.md` (silent when empty).

Keep the surrounding terminal-path framing (which path, why it's terminal, what the user will do next) inline per-skill — only the command pair + skip rule extracts to the reference.

## Why this lives here

- Four sites previously inlined the same 2-line block with diverging inline comments — drift evidence.
- F19 terminal-path-only convention establishes the semantic; this reference owns the literal command pair so future changes (e.g. adding a flush phase, changing exit code semantics) are one-edit.

## See also

- F19 terminal-path-only convention — surfacing fires only on terminal paths.
- Cited from: `rota-work` Step 1 guard-fail, `rota-work` no-argument empty-backlog branch, `rota-pause` Step 6, `rota-debug` Iron Law halt.
