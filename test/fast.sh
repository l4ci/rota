#!/usr/bin/env bash
# The fast test tier (test.fast): map changed files to the targeted checks a
# worker runs before a PR. Called as `bash test/fast.sh <files...>` via
# `rota test run fast` ({files} = files changed against the base branch).
# Go changes also run test/gofmt.sh on the changed files (the check CI runs).
# Anything not mapped here is left to the merge gate (test/gate.sh).
#
# Mapping: a .go file runs its package; a file under <pkg>/testdata/ (goldens,
# frozen jsonl) runs <pkg>; an asset in a dir whose .go files //go:embed (e.g.
# internal/knowledge/skills_block.md) runs that package; skills/ is embedded by
# the root package (embed.go), so it runs "."; test infra (test/lib, test/fakes,
# test/runner.sh, test/lib.sh, test/e2e) runs the "infra" check: bash -n on the shell
# files, py_compile on the python ones, and test/runner_leak_test.py.
# A change under test/sections/, test/lib.sh or test/runner.sh also runs section 71
# (the piped grep -q lint), which scans all of them.
# A non-test .go file (or embed asset) in a package also runs every package that
# imports it, transitively (go list .Deps), so a change to internal/fsio runs the
# dependents and the internal/cli goldens (#1303). A path-to-check table follows
# the loop: internal/config runs TestFrozenConfig, internal/doctor smoke 83,
# internal/host and internal/worker/dispatch.go smoke 51.
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
pipe_lint=0
pkgs=()
src_pkgs=()
sections=()
frozen=0

add_pkg() {
  local p="$1" q
  for q in ${pkgs[@]+"${pkgs[@]}"}; do [ "$q" = "$p" ] && return 0; done
  pkgs+=("$p")
}

# add_section <section file>: queue a smoke section once.
add_section() {
  local s q
  s="test/sections/$1"
  for q in ${sections[@]+"${sections[@]}"}; do [ "$q" = "$s" ] && return 0; done
  sections+=("$s")
}

# add_src_pkg <dir>: a source change; remember the package so its dependents run.
add_src_pkg() {
  local p="$1"
  [ "$p" = "." ] || p="./$p"
  add_pkg "$p"
  src_pkgs+=("$p")
}

for f in "$@"; do
  case "$f" in
    go.mod|go.sum) go_all=1 ;;
    *.go)
      d="$(dirname "$f")"
      [ -d "$d" ] || continue
      case "$f" in
        *_test.go) if [ "$d" = "." ]; then add_pkg "."; else add_pkg "./$d"; fi ;;
        *) add_src_pkg "$d" ;;
      esac
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
        add_src_pkg "$d"
      fi
      ;;
  esac
  case "$f" in
    internal/config/*) frozen=1 ;;
  esac
  case "$f" in
    internal/doctor/*) add_section 83_doctor.sh ;;
    internal/host/*|internal/worker/dispatch.go) add_section 51_herdr_dispatch.sh ;;
  esac
  case "$f" in
    test/lib/*|test/fakes/*|test/e2e/*|test/runner.sh|test/lib.sh|test/gate.sh|test/gate-*.py|test/gate_retry_test.py) infra=1 ;;
  esac
  case "$f" in
    skills/*|docs/*|*.md|test/validate-skills.py|test/doclint.sh) docs=1 ;;
  esac
  case "$f" in
    test/sections/*.sh) [ -f "$f" ] && sections+=("$f") ;;
  esac
  case "$f" in
    test/sections/*|test/lib.sh|test/runner.sh) pipe_lint=1 ;;
  esac
done

# Dependents of a changed source package: every package whose transitive imports
# include it. rel() strips the module path so names match add_pkg's "./dir".
if [ "${#src_pkgs[@]}" -gt 0 ]; then
  mod="$(go list -m)"
  while IFS= read -r dep; do
    add_pkg "$dep"
  done < <(go list -f '{{.ImportPath}} {{join .Deps " "}}' ./... | awk -v mod="$mod" -v list="${src_pkgs[*]}" '
    function rel(p) { if (p == mod) return "."; sub("^" mod "/", "./", p); return p }
    BEGIN { n = split(list, a, " "); for (i = 1; i <= n; i++) want[a[i]] = 1 }
    { for (i = 2; i <= NF; i++) if (rel($i) in want) { print rel($1); break } }' | sort -u)
fi

# Section 71 lints every section, lib.sh and runner.sh for piped grep -q, so any
# change under those paths runs it (#459) instead of leaving it to the gate.
if [ "$pipe_lint" = 1 ]; then
  lint=test/sections/71_no_pipe_grep_q.sh
  have=0
  for s in ${sections[@]+"${sections[@]}"}; do [ "$s" = "$lint" ] && have=1; done
  [ "$have" = 1 ] || sections+=("$lint")
fi

if [ "${FAST_DRY_RUN:-}" = 1 ]; then
  [ "$go_all" = 1 ] && echo "plan: go-all"
  for p in ${pkgs[@]+"${pkgs[@]}"}; do echo "plan: go $p"; done
  [ "$docs" = 1 ] && echo "plan: docs"
  [ "$infra" = 1 ] && echo "plan: infra"
  [ "$frozen" = 1 ] && echo "plan: frozen-config"
  for s in ${sections[@]+"${sections[@]}"}; do echo "plan: section $s"; done
  exit 0
fi

if [ "$go_all" = 1 ] || [ "${#pkgs[@]}" -gt 0 ]; then
  echo "fast: gofmt"
  bash test/gofmt.sh "$@"
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

if [ "$frozen" = 1 ]; then
  echo "fast: go test ./cmd/rota -run '^TestFrozenConfig\$'"
  go test ./cmd/rota -run '^TestFrozenConfig$'
fi

if [ "$docs" = 1 ]; then
  echo "fast: validate-skills"
  python3 test/validate-skills.py
  echo "fast: doclint"
  bash test/doclint.sh
fi

if [ "$infra" = 1 ]; then
  echo "fast: infra (bash -n, py_compile, runner_leak_test)"
  for f in test/runner.sh test/lib.sh test/gate.sh test/gate-*.py test/gate_retry_test.py test/lib/* test/fakes/* test/e2e/*.sh test/e2e/scenarios/*.sh; do
    [ -f "$f" ] || continue
    case "$f" in
      *.py) python3 -c 'import ast,sys; ast.parse(open(sys.argv[1]).read())' "$f" ;;
      *.sh) bash -n "$f" ;;
      *) if head -1 "$f" | grep -q bash; then bash -n "$f"; fi ;;
    esac
  done
  python3 test/runner_leak_test.py
  python3 test/gate_retry_test.py
fi

if [ "${#sections[@]}" -gt 0 ]; then
  echo "fast: smoke sections ${sections[*]}"
  SECTION_LIST="$(printf '%s\n' "${sections[@]}")" bash test/runner.sh
fi
