#!/usr/bin/env bash
# Builds the Codex plugin tree into <out>/plugins/rota: the committed manifest
# plus every skill with the shared references/ copied in as real files. Codex
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
for d in "$root"/skills/rota-*/; do
  name=$(basename "$d")
  mkdir -p "$dst/skills/$name"
  # -L would follow references; copy it explicitly instead. The supported
  # layout is checked above by `rota skills layout`, so everything found here ships.
  (cd "$d" && find . -type f ! -type l -print0 | while IFS= read -r -d '' f; do
    mkdir -p "$dst/skills/$name/$(dirname "$f")"
    cp "$f" "$dst/skills/$name/$f"
  done)
  mkdir -p "$dst/skills/$name/references"
  cp "$root"/skills/references/*.md "$dst/skills/$name/references/"
done
