---
name: rota-install
description: Use on "install rota", "/rota-install", when the rota plugin's session-start line says the binary is missing or does not match, or when a rota skill fails because `rota` is not on PATH.
---

# rota-install — Install the rota Binary

The rota plugin ships the skills; the skills call the `rota` binary. This skill installs the binary at the plugin's version and checks the result. It never runs `rota skills install`: the plugin already provides the skills, and a second copy would show every skill twice.

## Steps

1. **Plugin version.** Read `"version"` from `${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json`. Call it V.
2. **Already there?** Run `rota --version`. Its second word is the installed version. If it is V, say so and go to step 5. If it is not X.Y.Z (a `dev` build), it is the user's own build: say so and ask before replacing it.
3. **Homebrew** when `brew` is on PATH:
   - `rota` installed through Homebrew (`brew list rota` succeeds): `brew update && brew upgrade rota`.
   - Otherwise: `brew install l4ci/tap/rota`.

   The tap serves the latest release only. If `rota --version` now reports something other than V, do not install a second binary next to it: report both versions and the two fixes, `claude plugin update rota@rota` (plugin older than the binary) or waiting for the tap (tap older than the plugin).
4. **Install script** when there is no `brew`:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh -s -- --version V
   ```

   It installs to `~/.local/bin/rota` and checks the sha256, plus the minisign signature when `minisign` is installed. If it prints a `PATH` line, pass it on: the user adds it to their shell profile, since this skill edits no profile.
5. **Check.** Run `rota doctor` and report its result in a few lines. The `skills` check should pass, counting the plugin's copy as a root. Failing checks unrelated to the install (no `.rota/` in this directory, no forge CLI) are the user's to fix later: list them, do not fix them here.

## Rules

- Never `sudo`, never edit shell profiles, never run `rota skills install`.
- A failed download or signature check is reported as is. Do not retry with weaker options (`--strict` off, another source) without asking.
- Claude Code only. Codex users install with `rota skills install` (see the rota install docs).
