# Codex pane captures for #2178

Captured with `herdr agent read capture-2178 --lines N` from Codex 0.164.0
on 2026-10-27. Each file keeps the relevant trailing pane region verbatim.
The temporary discovery session used the light-tier gpt-6-luna model.

- `codex-terminal-wait.txt`: the session ran `sleep 45` in a background
  terminal and awaited it with `write_stdin`. Captured while still waiting.
- `codex-blocked.txt`: the session's explicit blocked sentinel after the
  command finished.
- `codex-approval.txt`: the session requested approval to run `true`. Captured
  before answering; the prompt was then cancelled.

`codex-terminal-wait-narrow.txt` preserves the two-line capture from kit's
pane in round 40, supplied in the #2178 review feedback. Its long elapsed
time leaves only `esc t…` of the interrupt hint visible.

Tests also compose these captures and use synthetic quotes to check that
stronger attention states and unrelated pane text keep their meaning.
