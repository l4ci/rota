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
# one built from this checkout by rota-bin.sh. Refuse anything it rejects, as
# the binary would not ship it.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
rota=$("$root/scripts/rota-bin.sh" "$tmp")
bad=$("$rota" skills layout "$root/skills")
if [ -n "$bad" ]; then
  while IFS= read -r f; do echo "codex-plugin: unsupported skill content skills/$f" >&2; done <<<"$bad"
  exit 1
fi
rm -rf "$dst"
mkdir -p "$dst/skills"
cp -R "$root/plugins/rota/.codex-plugin" "$dst/.codex-plugin"
# Ship what the binary's Load ships for each skill, as real files: `rota skills
# files` lists "<dest>\t<source>" pairs relative to the skills dir, so this only
# copies and holds no layout rule. Capture first: a process substitution would
# hide a failing producer from set -e and publish an empty bundle.
files=$("$rota" skills files "$root/skills")
if [ -z "$files" ]; then
  echo "codex-plugin: \`rota skills files\` listed nothing" >&2
  exit 1
fi
while IFS=$'\t' read -r f rel; do
  src=$root/skills/$rel
  if [ -L "$src" ]; then
    echo "codex-plugin: refusing symlinked skill file ${src#"$root"/}" >&2
    exit 1
  fi
  mkdir -p "$dst/skills/$(dirname "$f")"
  cp "$src" "$dst/skills/$f"
done <<<"$files"
