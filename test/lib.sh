#!/usr/bin/env bash
# Shared helpers for smoke test sections.
#
# Sourced by test/runner.sh. Defines pass/fail for assertion narration.
#
# Local-trap convention (F38) — when a section creates its own tmp tree,
# it MUST install a local trap immediately after mktemp -d and restore the
# global trap before its terminal `pass` line. Example:
#
#   TMP_X="$(mktemp -d)"
#   trap 'rm -rf "$TMP_X"' EXIT
#   ... assertions ...
#   trap 'rm -rf "$TMP"' EXIT
#   pass "..."
#
# This survives mid-block fail() under set -euo pipefail; the bare
# rm-at-end pattern leaks. Apply the same pattern in any new section.

pass() { printf '  \033[32mOK\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; exit "${ROTA_SMOKE_ASSERTION_EXIT:-1}"; }

# git shim (#3133): a fixture's bare `git config user.*` writes to whatever repo
# the cwd is in (or that `-C` names). If a cd before it failed, that is the real checkout, and the
# identity then sticks to every linked worktree. Refuse an identity write whose
# repo (cwd, or the -C target) is not under the run's scratch root, naming the
# calling line. Every other subcommand passes straight through.
git() {
  if [ -n "${RUN_TMP:-}" ]; then
    # Resolve the leading -C dirs (git applies them in order) to find the repo
    # the call really targets, then the subcommand and its key.
    local -a rest=("$@"); local dir="$PWD" sub="" key="" scope=local a
    while [ "${rest[0]:-}" = -C ] && [ "${#rest[@]}" -ge 2 ]; do
      case "${rest[1]}" in /*) dir="${rest[1]}" ;; *) dir="$dir/${rest[1]}" ;; esac
      rest=("${rest[@]:2}")
    done
    sub="${rest[0]:-}"
    if [ "$sub" = config ]; then
      for a in "${rest[@]:1}"; do
        case "$a" in
          --global|--system) scope=$a ;;
          -*) ;;
          *) [ -n "$key" ] || key=$a ;;
        esac
      done
      case "$key" in
        user.*)
          local top here
          top="$(command git -C "$dir" rev-parse --show-toplevel 2>/dev/null)" || top=""
          here="$(cd "${top:-$dir}" 2>/dev/null && pwd -P)" || here=""
          case "$scope:$here" in
            local:"$RUN_TMP"/*) ;;
            *) printf '  \033[31mFAIL\033[0m git config %s targets %s outside the run root (%s:%s); use a scratch dir\n' \
                 "$key" "${here:-$dir}" "${BASH_SOURCE[1]:-?}" "${BASH_LINENO[0]:-?}" >&2
               return 1 ;;
          esac ;;
      esac
    fi
  fi
  command git "$@"
}

# identity_config_scan <file>...: fail, listing each line, when a section writes
# git identity with `-C "$PWD"` or `-C .`. That names the same repo as a bare
# call but slips past a reader's eye; give the scratch dir explicitly (#3133).
# The runtime shim above catches every other form.
identity_config_scan() {
  local hits
  hits=$(grep -nE 'git[[:space:]]+-C[[:space:]]+("\$PWD"|\$PWD|"\."|\.)[[:space:]]+config[[:space:]]+(--[a-z]+[[:space:]]+)*user\.' "$@" 2>/dev/null) || return 0
  printf '\033[31merror: git -C "$PWD"/. config user.* is a bare write in disguise; name the scratch dir (#3133):\033[0m\n%s\n' "$hits" >&2
  return 1
}

# vfail is the handler for a captured verb call (#579): `OUT=$(hvj item show X) || vfail`.
# It reads the failing status, finds the source line of the call and fails with the
# verb's own text, so the message names which call broke without a hand-written string.
vfail() {
  local rc=$? file="${BASH_SOURCE[1]}" line="${BASH_LINENO[0]}" text
  # A multi-line capture reports its closing line; walk back to the line that
  # opens the `$(` so the message names the verb.
  local first=$line
  local opener='^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*="?\$\(' src
  while [ "$first" -gt 1 ]; do
    src=$(sed -n "${first}p" "$file")
    grep -qE "$opener" <<<"$src" && break # here-string: a piped grep -q is SIGPIPE-flaky (section 71)
    first=$((first - 1))
  done
  text=$(sed -n "${first},${line}p" "$file" 2>/dev/null | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*|| vfail.*$//' | tr '\n' ' ' | sed -e 's/ \\ / /g')
  fail "verb exited $rc at ${file##*/}:$first-$line: $text"
}

# build_rota <out> builds the rota binary the shell suites share (runner.sh and
# doclint.sh), stamped with the VERSION file so version-drift checks see a
# matching install, and with -tags rotatest so the tracker retry pauses and
# bulk-migrate pacing read their zeroing env. One definition keeps doclint and
# smoke on the same binary. Needs $REPO.
build_rota() {
  local v
  v="$(tr -d '[:space:]' < "$REPO/VERSION")"
  (cd "$REPO" && go build -tags rotatest -ldflags "-X github.com/l4ci/rota/internal/version.Version=$v" -o "$1" ./cmd/rota)
}

# fixture_copy <dest> <path>... makes <dest> and copies each repo-relative
# <path> into it, so a lint case can mutate a throwaway tree and assert the
# lint fails. Needs $REPO.
fixture_copy() {
  local dest="$1" p; shift
  mkdir -p "$dest"
  for p in "$@"; do cp -R "$REPO/$p" "$dest/"; done
}

# Black-box helpers (#46). Callers check the exit code themselves:
#   rc=0; out=$(hvj item show B01) || rc=$?
# hvj runs `"$ROTA_BIN" --json "$@"`, prints the envelope and returns the verb's
# own exit code (stderr is left alone; redirect it where a failure is expected).
hvj() { "$ROTA_BIN" --json "$@"; }

# jget <path> reads one value from an envelope on stdin. The path is dotted
# with [n] indexes (data.items[0].id). Strings print raw, bools as true/false,
# everything else as compact JSON. A missing path prints nothing and fails.
jget() {
  python3 -c '
import json, re, sys
v = json.load(sys.stdin)
try:
    for k in re.findall(r"[^.\[\]]+|\[\d+\]", sys.argv[1]):
        v = v[int(k[1:-1])] if k[0] == "[" else v[k]
except (KeyError, IndexError, TypeError):
    sys.exit(1)
print(v if isinstance(v, str) else json.dumps(v, separators=(",", ":")))
' "$1"
}

# Registry and doctor helpers shared by the dispatch, worker and doctor sections.
#
# slot_field <workers.json> <slot> <field> prints one field of a slot; a missing
# or null field prints nothing, so test it with `-z`.
slot_field() {
  python3 -c 'import json,sys; s=[x for x in json.load(open(sys.argv[1]))["slots"] if x["name"]==sys.argv[2]][0]; v=s.get(sys.argv[3]); print("" if v is None else v)' "$1" "$2" "$3"
}

# doctor_run <dir> [VAR=value ...] runs `rota --json doctor` in <dir> with the
# extra environment and sets RC and OUT. Env prefixed to the call
# (`ROTA_FAKE_X=1 doctor_run ...`) reaches doctor as well.
doctor_run() {
  local dir="$1"; shift
  RC=0; OUT=$( cd "$dir" && env "$@" "$ROTA_BIN" --json doctor 2>/dev/null ) || RC=$?
}

# doctor_check_field <check> <field> prints a field of one doctor check from $OUT
# (ABSENT when the check lacks the field).
doctor_check_field() {
  python3 -c '
import json,sys
cs={c["name"]:c for c in json.load(sys.stdin)["data"]["checks"]}
print(cs[sys.argv[1]].get(sys.argv[2],"ABSENT"))' "$1" "$2" <<<"$OUT"
}

# hookin <prompt> prints the UserPromptSubmit hook payload for a prompt.
hookin() { python3 -c 'import json,sys; print(json.dumps({"prompt": sys.argv[1]}))' "$1"; }

# parked <repo> <slot> succeeds when the slot's worktree sits on park/<slot>.
parked() { [ "$(git -C "$1/.worktrees/$2" symbolic-ref --short HEAD)" = "park/$2" ]; }

# cfg <config.json> <dispatch> [claude-command] writes a minimal round config.
cfg() {
  local extra=""
  [ -n "${3:-}" ] && extra=",\"commands\":{\"claude\":\"$3\"}"
  printf '{"round":{"dispatch":"%s"%s}}\n' "$2" "$extra" > "$1"
}

# F62 — preamble convention scan. Reads every test/sections/*.sh and fails
# if any forbidden pattern is found. Catches the class of regressions where
# per-section drift would shadow runner state or violate the F38 local-trap
# convention. Called once by runner.sh before the section loop.
check_section_conventions() {
  local sections_dir="$1"
  local violations=0

  # B22-class — top-level `TMP=` assignment shadows runner's $TMP.
  # Permitted: `TMP_<SUFFIX>=` (e.g., TMP_X, TMP_CFG) per the F38 convention.
  # Also permitted inside comments and inside `local`/`readonly` declarations.
  local matches
  matches=$(grep -nE '^[[:space:]]*TMP=' "$sections_dir"/*.sh 2>/dev/null || true)
  if [ -n "$matches" ]; then
    printf '\033[31merror: section file shadows runner $TMP (use TMP_<SUFFIX>=$(mktemp -d) instead):\033[0m\n' >&2
    echo "$matches" >&2
    violations=$((violations + 1))
  fi

  # B19-class — bare `trap '... EXIT'` that doesn't restore the global trap
  # before the section's terminal `pass` line. Detect by looking for files
  # that install a local trap but never reset it back to the global form
  # (`trap 'rm -rf "$TMP"' EXIT`). This is a heuristic, not exhaustive —
  # a section that installs its own trap MUST also reset it.
  local file
  for file in "$sections_dir"/*.sh; do
    [ -f "$file" ] || continue
    # Only check sections that install a local trap.
    grep -qE "^[[:space:]]*trap[[:space:]]+'.*\\\$TMP_" "$file" 2>/dev/null || continue
    # Must reset to the global trap before the section ends.
    if ! grep -qE "^[[:space:]]*trap[[:space:]]+'rm -rf[[:space:]]+\"?\\\$TMP\"?'[[:space:]]+EXIT" "$file" 2>/dev/null; then
      printf '\033[31merror: section installs a local trap but never restores the global trap (F38 convention):\033[0m\n' >&2
      echo "  $file" >&2
      violations=$((violations + 1))
    fi
  done

  # #3133 — an identity write aimed at the cwd under another name.
  identity_config_scan "$sections_dir"/*.sh || violations=$((violations + 1))

  # #579 — a bare `VAR=$(rota ...)` aborts the section silently under set -e
  # when the verb fails (#461 hid a failure this way). Capture the exit code
  # explicitly: `|| vfail`, `|| fail "..."` or `|| rc=$?`. The linter handles
  # quoted and multi-line captures; see test/section-template.sh.
  matches=$(python3 "$(dirname "${BASH_SOURCE[0]}")/lint-sections.py" "$sections_dir" 2>&1) || {
    printf '\033[31merror: bare verb capture under set -e (add `|| vfail`; see test/section-template.sh):\033[0m\n' >&2
    echo "$matches" >&2
    violations=$((violations + 1))
  }

  if [ "$violations" -gt 0 ]; then
    printf '\033[31merror: %d convention violation(s) in test/sections/ — fix before re-running smoke\033[0m\n' "$violations" >&2
    return 1
  fi
  return 0
}
