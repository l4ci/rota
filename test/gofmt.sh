#!/usr/bin/env bash
# The gofmt check CI runs (`gofmt -l .`), so a format error fails the local gate
# too (#672). Usage: bash test/gofmt.sh [files...]; no files means every tracked
# .go file. Prints the unformatted files and exits 1 when there are any.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

files=()
if [ "$#" -eq 0 ]; then
  while IFS= read -r f; do files+=("$f"); done < <(git ls-files '*.go')
else
  for f in "$@"; do
    case "$f" in *.go) [ -f "$f" ] && files+=("$f") ;; esac
  done
fi
[ "${#files[@]}" -gt 0 ] || exit 0

bad="$(gofmt -l "${files[@]}")"
if [ -n "$bad" ]; then
  echo "gofmt: these files are not formatted (run gofmt -w):" >&2
  echo "$bad" >&2
  exit 1
fi
