#!/usr/bin/env bash
# Builds the Codex plugin tree into <out>/plugins/rota: the committed manifest
# plus every skill with the references it reaches copied in as real files. Codex
# drops symlinks when it caches a plugin (#676), so the skills/*/references
# links cannot ship as they are. release.yml publishes the result to the
# `codex` branch, which .agents/plugins/marketplace.json installs from.
set -euo pipefail
out=${1:?usage: codex-plugin.sh <out-dir>}
root=$(cd "$(dirname "$0")/.." && pwd)
dst=$out/plugins/rota
# The binary owns the skill layout rule (skills.SupportedFile): ROTA_BIN, else
# build one from this checkout. Refuse anything it rejects, as the binary would
# not ship it.
rota=${ROTA_BIN:-}
if [ -z "$rota" ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  (cd "$root" && go build -o "$tmp/rota" ./cmd/rota)
  rota=$tmp/rota
fi
bad=$("$rota" skills layout "$root/skills")
if [ -n "$bad" ]; then
  while IFS= read -r f; do echo "codex-plugin: unsupported skill content skills/$f" >&2; done <<<"$bad"
  exit 1
fi
rm -rf "$dst"
mkdir -p "$dst/skills"
cp -R "$root/plugins/rota/.codex-plugin" "$dst/.codex-plugin"
# Ship what the binary's Load ships for each skill (its own files plus the
# references it reaches), as real files: `rota skills files` is the one list.
while IFS= read -r f; do
  [ -n "$f" ] || continue
  mkdir -p "$dst/skills/$(dirname "$f")"
  case "$f" in
    */references/*) cp "$root/skills/references/${f##*/}" "$dst/skills/$f" ;;
    *) cp "$root/skills/$f" "$dst/skills/$f" ;;
  esac
done < <("$rota" skills files "$root/skills")
