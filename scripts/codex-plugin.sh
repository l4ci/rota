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
rm -rf "$dst"
mkdir -p "$dst/skills"
cp -R "$root/plugins/rota/.codex-plugin" "$dst/.codex-plugin"
for d in "$root"/skills/rota-*/; do
  name=$(basename "$d")
  mkdir -p "$dst/skills/$name"
  # -L would follow references; copy it explicitly instead. Only the supported
  # layout (internal/skills.SupportedFile: flat markdown, agents/*.yaml) is
  # copied; anything else is refused, as the binary would not ship it.
  (cd "$d" && find . -type f ! -type l -print0 | while IFS= read -r -d '' f; do
    case "$f" in
      ./*/*/*) echo "codex-plugin: unsupported skill content skills/$name/${f#./}" >&2; exit 1 ;;
      ./*/*) case "$f" in ./agents/*.yaml) ;; *) echo "codex-plugin: unsupported skill content skills/$name/${f#./}" >&2; exit 1 ;; esac ;;
      *.md) ;;
      *) echo "codex-plugin: unsupported skill content skills/$name/${f#./}" >&2; exit 1 ;;
    esac
    mkdir -p "$dst/skills/$name/$(dirname "$f")"
    cp "$f" "$dst/skills/$name/$f"
  done)
  mkdir -p "$dst/skills/$name/references"
  cp "$root"/skills/references/*.md "$dst/skills/$name/references/"
done
