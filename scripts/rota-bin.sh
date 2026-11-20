#!/usr/bin/env bash
# Prints the path of a rota binary for skill checks. ROTA_BIN wins untouched;
# else this checkout's ./cmd/rota is built into <out-dir> (the caller owns and
# removes it; a fresh temp dir when omitted) and <out-dir>/rota is printed.
# The one place that holds the build, so the validator, the Codex builder and
# the Go tests share one env rule.
set -euo pipefail
if [ -n "${ROTA_BIN:-}" ]; then
  printf '%s\n' "$ROTA_BIN"
  exit 0
fi
root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:-$(mktemp -d)}
(cd "$root" && go build -o "$out/rota" ./cmd/rota) >&2
printf '%s\n' "$out/rota"
