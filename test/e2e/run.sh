#!/usr/bin/env bash
# The e2e tier (test.e2e, #386): scripted stub-worker scenarios for the round
# machinery. One command; exits non-zero when any scenario fails.
#
#   bash test/e2e/run.sh              run every scenario
#   bash test/e2e/run.sh dead 03      run the scenarios whose file name matches any argument
#
# Each test/e2e/scenarios/*.sh runs in its own bash with a fresh sandbox
# (E2E_TMP) and the test/e2e/lib.sh helpers. rota is built once from this
# checkout unless ROTA_BIN names a binary (absolute path). Real tmux, herdr, gh
# and glab are replaced by poison stand-ins that fail loudly, so a scenario can
# never reach a live host or forge.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
REPO="$(cd "$HERE/../.." && pwd -P)"
RUN_TMP="$(mktemp -d)" || exit 2
trap 'rm -rf "${RUN_TMP:?}"' EXIT

if [ -z "${ROTA_BIN:-}" ]; then
  ROTA_BIN="$RUN_TMP/rota"
  (cd "$REPO" && go build -ldflags "-X github.com/l4ci/rota/internal/version.Version=$(tr -d '[:space:]' <VERSION)" \
    -o "$ROTA_BIN" ./cmd/rota) || { echo "e2e: go build ./cmd/rota failed" >&2; exit 2; }
fi
export ROTA_BIN
E2E_POISON="$RUN_TMP/poison"; mkdir -p "$E2E_POISON"
for cli in tmux herdr gh glab; do
  printf '#!/bin/sh\necho "e2e: unexpected real %s call: $*" >&2\nexit 99\n' "$cli" >"$E2E_POISON/$cli"
  chmod +x "$E2E_POISON/$cli"
done
export E2E_POISON E2E_FAKES="$REPO/test/fakes" E2E_LIB="$HERE/lib.sh"

failed=()
ran=0
for f in "$HERE"/scenarios/*.sh; do
  name="$(basename "$f" .sh)"
  if [ "$#" -gt 0 ]; then
    hit=0; for a in "$@"; do case "$name" in *"$a"*) hit=1 ;; esac; done
    [ "$hit" = 1 ] || continue
  fi
  ran=$((ran + 1))
  echo "scenario $name"
  E2E_TMP="$(mktemp -d "$RUN_TMP/sc.XXXXXX")"; export E2E_TMP
  if ! bash -c 'set -euo pipefail; source "$E2E_LIB"; source "$1"' _ "$f"; then
    failed+=("$name")
  fi
  rm -rf "${E2E_TMP:?}"
done
[ "$ran" -gt 0 ] || { echo "e2e: no scenario matched" >&2; exit 2; }
if [ "${#failed[@]}" -gt 0 ]; then
  echo "e2e: ${#failed[@]} of $ran scenario(s) failed: ${failed[*]}" >&2
  exit 1
fi
echo "e2e: $ran scenario(s) passed"
