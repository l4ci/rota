#!/bin/sh
# SessionStart hook of the rota Claude Code plugin: one line for the user when
# the rota binary is missing or is not the plugin's version, nothing otherwise.
# A dev build (no X.Y.Z version) is someone's own binary and is left alone.
root=${CLAUDE_PLUGIN_ROOT:-$(dirname "$0")/../..}
want=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$root/.claude-plugin/plugin.json" | head -n 1)
[ -n "$want" ] || exit 0
if command -v rota >/dev/null 2>&1; then
  have=$(rota --version 2>/dev/null | awk '{print $2; exit}')
  have=${have#v}
  case $have in
    "$want") exit 0 ;;
    [0-9]*.[0-9]*.[0-9]*) msg="rota $have does not match the rota plugin $want: run /rota:rota-install, or claude plugin update rota@rota" ;;
    *) exit 0 ;;
  esac
else
  msg="rota: the plugin's skills need the rota binary: run /rota:rota-install"
fi
printf '{"systemMessage": "%s"}\n' "$msg"
