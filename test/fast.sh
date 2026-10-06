#!/usr/bin/env bash
# The fast test tier (test.fast): map changed files to the targeted checks a
# worker runs before a PR. Called as `bash test/fast.sh <files...>` via
# `rota test run fast` ({files} = files changed against the base branch).
# Anything not mapped here is left to the merge gate (test/gate.sh).
#
# Mapping: a .go file runs its package; a file under <pkg>/testdata/ (goldens,
# frozen jsonl) runs <pkg>; an asset in a dir whose .go files //go:embed (e.g.
# internal/knowledge/skills_block.md) runs that package; skills/ is embedded by
# the root package (embed.go), so it runs "."; test infra (test/lib, test/fakes,
# test/runner.sh, test/lib.sh) runs the "infra" check: bash -n on the shell
# files, py_compile on the python ones, and test/runner_leak_test.py.
# FAST_DRY_RUN=1 prints the selected checks as "plan: <check>" and runs nothing;
# test/sections/120_fast_mapping.sh asserts the mapping through it.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [ "$#" -eq 0 ]; then
  echo "fast: no changed files"
  exit 0
fi

go_all=0
docs=0
infra=0
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
    */testdata/*)
      d="${f%%/testdata/*}"
      [ -d "$d" ] && { if [ "$d" = "." ]; then add_pkg "."; else add_pkg "./$d"; fi; }
      ;;
    skills/*) add_pkg "." ;;
    *.go) ;;
    *)
      d="$(dirname "$f")"
      if [ -d "$d" ] && compgen -G "$d/*.go" >/dev/null &&
         grep -l '^//go:embed' "$d"/*.go >/dev/null 2>&1; then
        if [ "$d" = "." ]; then add_pkg "."; else add_pkg "./$d"; fi
      fi
      ;;
  esac
  case "$f" in
    test/lib/*|test/fakes/*|test/runner.sh|test/lib.sh) infra=1 ;;
  esac
  case "$f" in
    skills/*|docs/*|*.md|test/validate-skills.py|test/doclint.sh) docs=1 ;;
  esac
  case "$f" in
    test/sections/*.sh) [ -f "$f" ] && sections+=("$f") ;;
  esac
done

if [ "${FAST_DRY_RUN:-}" = 1 ]; then
  [ "$go_all" = 1 ] && echo "plan: go-all"
  for p in ${pkgs[@]+"${pkgs[@]}"}; do echo "plan: go $p"; done
  [ "$docs" = 1 ] && echo "plan: docs"
  [ "$infra" = 1 ] && echo "plan: infra"
  for s in ${sections[@]+"${sections[@]}"}; do echo "plan: section $s"; done
  exit 0
fi

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

if [ "$infra" = 1 ]; then
  echo "fast: infra (bash -n, py_compile, runner_leak_test)"
  for f in test/runner.sh test/lib.sh test/lib/* test/fakes/*; do
    [ -f "$f" ] || continue
    case "$f" in
      *.py) python3 -c 'import ast,sys; ast.parse(open(sys.argv[1]).read())' "$f" ;;
      *.sh) bash -n "$f" ;;
      *) if head -1 "$f" | grep -q bash; then bash -n "$f"; fi ;;
    esac
  done
  python3 test/runner_leak_test.py
fi

if [ "${#sections[@]}" -gt 0 ]; then
  echo "fast: smoke sections ${sections[*]}"
  SECTION_LIST="$(printf '%s\n' "${sections[@]}")" bash test/runner.sh
fi
