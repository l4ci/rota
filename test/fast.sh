#!/usr/bin/env bash
# The fast test tier (test.fast): map changed files to the targeted checks a
# worker runs before a PR. Called as `bash test/fast.sh <files...>` via
# `rota test run fast` ({files} = files changed against the base branch).
# Go changes also run test/gofmt.sh on the changed files (the check CI runs).
# Repository source scans always run, independently of this mapping. Their
# registry and registration check live in test/treescan/.
# Anything else not mapped here is left to the merge gate (test/gate.sh).
#
# Mapping: a .go file runs its package; a file under <pkg>/testdata/ (goldens,
# frozen jsonl) runs <pkg>; an asset in a dir whose .go files //go:embed (e.g.
# internal/knowledge/skills_block.md) runs that package; skills/ is embedded by
# the root package (embed.go), so it runs ".". Every other path-to-check rule is a
# row of FAST_PATH_MAP below, the one glob-to-check table (#3956): `infra` (bash -n
# on the shell files, a parse of the python ones, the Python harness tests), `docs`
# (validate-skills and doclint, which also run the doclint-only scripts) or
# `section:<file>` (a smoke section). Each lint section declares its own trigger
# paths in a `# lint-trigger:` line (#3771); fast.sh reads those tags, not the table.
# A non-test .go file (or embed asset) in a package also runs every package that
# imports it in production or tests, transitively (go list -test .Deps), so an
# internal/fsio change runs dependents and the internal/cli goldens (#1303).
# internal/config runs TestFrozenConfig.
# FAST_DRY_RUN=1 prints the selected checks as "plan: <check>" and runs nothing;
# test/sections/120_fast_mapping.sh asserts the mapping through it.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

go_all=0
docs=0
infra=0
pkgs=()
src_pkgs=()
sections=()
frozen=0

# The one glob-to-check table: `glob|check`, one glob per row. check is `infra`,
# `docs` or `section:<file>`. test/sections/179_fast_section_lints.sh asserts that
# every script test/doclint.sh runs maps to `docs`.
FAST_PATH_MAP=(
  'internal/doctor/*|section:83_doctor.sh'
  'internal/host/*|section:51_herdr_dispatch.sh'
  'internal/worker/dispatch.go|section:51_herdr_dispatch.sh'
  'test/lib/*|infra'
  'test/fakes/*|infra'
  'test/e2e/*|infra'
  'test/runner.sh|infra'
  'test/lib.sh|infra'
  'test/gate.sh|infra'
  'test/gate-*.py|infra'
  'test/gate_sandbox.py|infra'
  'test/gate-*.txt|infra'
  'internal/flakeledger/infrastructure-markers.txt|infra'
  'test/py-tests.*|infra'
  'test/*_test.py|infra'
  'test/fast.sh|infra'
  'test/treescan/*|infra'
  'skills/*|docs'
  'docs/*|docs'
  '*.md|docs'
  'test/validate-skills.py|docs'
  'test/doclint.sh|docs'
  # scripts and data test/doclint.sh runs
  'scripts/skills-docs.py|docs'
  'scripts/skilldocs.py|docs'
  'scripts/site-sync.py|docs'
  'scripts/fmlite.py|docs'
  'scripts/videos.py|docs'
  'scripts/video_catalog.py|docs'
  'test/lint-verbs.py|docs'
  'test/lint-sections.py|docs'
  'test/check-doc-stamps.py|docs'
  'test/videos-drift.sh|docs'
  'test/evals/*|docs'
)

# Test seam for section 120: the section rows as `glob|file`.
if [ "${FAST_PRINT_SECTION_MAP:-}" = 1 ]; then
  for mapping in "${FAST_PATH_MAP[@]}"; do
    case "$mapping" in *'|section:'*) printf '%s\n' "${mapping/|section:/|}" ;; esac
  done
  exit 0
fi

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
      # A deleted or moved package leaves importers nobody can name here, so
      # run everything rather than nothing.
      [ -d "$d" ] || { go_all=1; continue; }
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
  for mapping in "${FAST_PATH_MAP[@]}"; do
    pattern="${mapping%|*}" check="${mapping##*|}"
    case "$f" in
      $pattern)
        case "$check" in
          infra) infra=1 ;;
          docs) docs=1 ;;
          section:*) add_section "${check#section:}" ;;
        esac
        ;;
    esac
  done
  case "$f" in
    test/treescan/*) add_pkg ./test/treescan ;;
  esac
  case "$f" in
    test/sections/*.sh) [ -f "$f" ] && sections+=("$f") ;;
  esac
done

# Dependents of a changed source package: every package whose production or test
# transitive imports include it. Test variants map to their runnable owner;
# production Match records remain, while synthetic test mains are excluded.
# rel() strips the literal module path so names match add_pkg's "./dir".
if [ "${#src_pkgs[@]}" -gt 0 ]; then
  if ! mod="$(go list -m)"; then
    echo "fast: dependency discovery failed (go list -m)" >&2
    exit 1
  fi
  # Check the whole producer pipeline before consuming any output. Process
  # substitution hides its status, even with pipefail, and may yield a partial
  # graph after go list fails to load another package's test imports.
  if ! deps="$(go list -test -f '{{if or .ForTest .Match}}{{if .ForTest}}{{.ForTest}}{{else}}{{.ImportPath}}{{end}} {{join .Deps " "}}{{end}}' ./... | awk -v mod="$mod" -v list="${src_pkgs[*]}" '
    function rel(p) {
      if (p == mod) return "."
      if (index(p, mod "/") == 1) return "./" substr(p, length(mod)+2)
      return ""
    }
    BEGIN { n = split(list, a, " "); for (i = 1; i <= n; i++) want[a[i]] = 1 }
    {
      gsub(/ \[[^]]+\]/, "", $0)
      owner = rel($1)
      if (owner == "") next
      for (i = 2; i <= NF; i++) if (rel($i) in want) { print owner; break }
    }' | sort -u)"; then
    echo "fast: dependency discovery failed (go list -test); selection aborted" >&2
    exit 1
  fi
  if [ -n "$deps" ]; then
    while IFS= read -r dep; do
      add_pkg "$dep"
    done <<<"$deps"
  fi
fi

# Section lints declare their own triggers: a test/sections/*.sh file with a
# `# lint-trigger: <glob>...` line runs whenever a changed file matches one glob
# (71: piped grep -q, #459; 164: printf into head, #1242, #3771). A new lint
# needs only its tag. Globs are read into an array so they are not expanded here.
for lf in test/sections/*.sh; do
  [ -f "$lf" ] || continue
  read -r -a lint_globs <<<"$(sed -n 's/^# lint-trigger: //p' "$lf")" || true
  [ "${#lint_globs[@]}" -gt 0 ] || continue
  for f in "$@"; do
    hit=0
    for g in "${lint_globs[@]}"; do
      case "$f" in $g) hit=1; break ;; esac
    done
    if [ "$hit" = 1 ]; then add_section "$(basename "$lf")"; break; fi
  done
done

if [ "${FAST_DRY_RUN:-}" = 1 ]; then
  [ "$go_all" = 1 ] && echo "plan: go-all"
  for p in ${pkgs[@]+"${pkgs[@]}"}; do echo "plan: go $p"; done
  [ "$docs" = 1 ] && echo "plan: docs"
  [ "$infra" = 1 ] && echo "plan: infra"
  [ "$frozen" = 1 ] && echo "plan: frozen-config"
  for s in ${sections[@]+"${sections[@]}"}; do echo "plan: section $s"; done
  echo "plan: tree-scans"
  exit 0
fi

# Run before selected package tests so a cheap architecture guard fails early.
# The runner validates registration even for changes with no package mapping,
# and bypasses Go's test cache so every invocation executes the scans afresh.
echo "fast: tree-scans"
go run ./test/treescan

if [ "$#" -eq 0 ]; then
  echo "fast: no changed files"
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
  echo "fast: infra (bash -n, py_compile, py-tests)"
  # Syntax-check every tracked file an infra row covers.
  infra_files="$(git ls-files --cached --others --exclude-standard test internal/flakeledger)"
  [ -n "$infra_files" ] || { echo "fast: infra check found no files under test/ or internal/flakeledger" >&2; exit 1; }
  while IFS= read -r f; do
    [ -f "$f" ] || continue
    hit=0
    for mapping in "${FAST_PATH_MAP[@]}"; do
      case "${mapping##*|}" in infra) ;; *) continue ;; esac
      case "$f" in ${mapping%|*}) hit=1; break ;; esac
    done
    [ "$hit" = 1 ] || continue
    case "$f" in
      *.py) python3 -c 'import ast,sys; ast.parse(open(sys.argv[1]).read())' "$f" ;;
      *.sh) bash -n "$f" ;;
      *) if head -1 "$f" | grep -q bash; then bash -n "$f"; fi ;;
    esac
  done <<<"$infra_files"
  bash test/py-tests.sh
fi

if [ "${#sections[@]}" -gt 0 ]; then
  echo "fast: smoke sections ${sections[*]}"
  SECTION_LIST="$(printf '%s\n' "${sections[@]}")" bash test/runner.sh
fi
