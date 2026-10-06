#!/usr/bin/env bash
# The fast test tier (test.fast): map changed files to the targeted checks a
# worker runs before a PR. Called as `bash test/fast.sh <files...>` via
# `rota test run fast` ({files} = files changed against the base branch).
# Anything not mapped here is left to the merge gate (test/gate.sh).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [ "$#" -eq 0 ]; then
  echo "fast: no changed files"
  exit 0
fi

go_all=0
docs=0
pkgs=()
sections=()

add_pkg() {
  local p="$1" q
  for q in ${pkgs[@]+"${pkgs[@]}"}; do [ "$q" = "$p" ] && return 0; done
  pkgs+=("$p")
}

for f in "$@"; do
  case "$f" in
    go.mod|go.sum) go_all=1 ;;
    *.go)
      d="$(dirname "$f")"
      [ -d "$d" ] || continue
      if [ "$d" = "." ]; then add_pkg "."; else add_pkg "./$d"; fi
      ;;
  esac
  case "$f" in
    skills/*|docs/*|*.md|test/validate-skills.py|test/doclint.sh) docs=1 ;;
  esac
  case "$f" in
    test/sections/*.sh) [ -f "$f" ] && sections+=("$f") ;;
  esac
done

if [ "$go_all" = 1 ]; then
  echo "fast: go vet ./... && go test ./..."
  go vet ./...
  go test ./...
elif [ "${#pkgs[@]}" -gt 0 ]; then
  echo "fast: go vet ${pkgs[*]}"
  go vet "${pkgs[@]}"
  echo "fast: go test ${pkgs[*]}"
  go test "${pkgs[@]}"
fi

if [ "$docs" = 1 ]; then
  echo "fast: validate-skills"
  python3 test/validate-skills.py
  echo "fast: doclint"
  bash test/doclint.sh
fi

if [ "${#sections[@]}" -gt 0 ]; then
  echo "fast: smoke sections ${sections[*]}"
  SECTION_LIST="$(printf '%s\n' "${sections[@]}")" bash test/runner.sh
fi
