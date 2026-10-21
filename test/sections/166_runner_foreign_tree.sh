echo "runner foreign tree — a section from another checkout never tests this checkout's binary (#1479)"
# A reviewer exports a branch (git archive: no .git) and points SECTION_LIST at its
# sections. With ROTA_BIN unset the runner would build ITS OWN checkout's binary and
# a pass would say nothing about the branch, so it refuses and names both trees.
# With ROTA_BIN set the section runs. An export also runs its own runner.

rft_tree() { # rft_tree <dir>: a no-.git export holding test/ and a marker section
  mkdir -p "$1"
  cp -R "$TESTDIR" "$1/test"
  cp "$TESTDIR/../VERSION" "$1/VERSION"
  printf 'touch "$RFT_MARK"\npass "foreign section ran"\n' > "$1/test/sections/999_marker.sh"
}
rft_run() { # rft_run <runner-dir> <list> [ROTA_BIN]: prints "rc|output"
  local out rc=0
  out="$(cd "$1" && env -u ROTA_BIN ${3:+ROTA_BIN="$3"} RFT_MARK="$RFT_DIR/mark" SECTION_LIST="$2" bash test/runner.sh 2>&1)" || rc=$?
  printf '%s|%s' "$rc" "$out"
}

RFT_DIR="$(mktemp -d)"
rft_tree "$RFT_DIR/export"
printf '#!/bin/sh\nexit 0\n' > "$RFT_DIR/rota"; chmod +x "$RFT_DIR/rota"
FOREIGN="$RFT_DIR/export/test/sections/999_marker.sh"

# AC-1: foreign section, ROTA_BIN unset (runner = this checkout)
RES="$(rft_run "$TESTDIR/.." "$FOREIGN")"
[ "${RES%%|*}" = 2 ] || fail "foreign section without ROTA_BIN must exit 2, got: $RES"
case "$RES" in *"$RFT_DIR/export"*) ;; *) fail "refusal does not name the section's tree: $RES" ;; esac
case "$RES" in *"$(cd "$TESTDIR/.." && pwd -P)"*) ;; *) fail "refusal does not name the runner's tree: $RES" ;; esac
case "$RES" in *ROTA_BIN*) ;; *) fail "refusal does not say how to set ROTA_BIN: $RES" ;; esac
[ ! -e "$RFT_DIR/mark" ] || fail "the foreign section ran despite the refusal"

# foreign section with ROTA_BIN set runs
RES="$(rft_run "$TESTDIR/.." "$FOREIGN" "$RFT_DIR/rota")"
[ "${RES%%|*}" = 0 ] || fail "foreign section with ROTA_BIN must run, got: $RES"
[ -e "$RFT_DIR/mark" ] || fail "foreign section did not run with ROTA_BIN set: $RES"
rm -f "$RFT_DIR/mark"

# a file outside any sections dir stays refused even with ROTA_BIN set
RES="$(rft_run "$TESTDIR/.." "$RFT_DIR/export/test/lib.sh" "$RFT_DIR/rota")"
[ "${RES%%|*}" = 2 ] || fail "a non-section entry must exit 2, got: $RES"

# AC-2: the export's own runner works without .git
[ ! -e "$RFT_DIR/export/.git" ] || fail "fixture export must have no .git"
RES="$(rft_run "$RFT_DIR/export" "test/sections/999_marker.sh" "$RFT_DIR/rota")"
[ "${RES%%|*}" = 0 ] || fail "the runner must work from a tree without .git, got: $RES"
case "$RES" in *"All smoke tests passed"*) ;; *) fail "no-.git export did not pass: $RES" ;; esac
[ -e "$RFT_DIR/mark" ] || fail "no-.git export did not run its section: $RES"
rm -rf "$RFT_DIR"
pass "runner refuses a foreign section without ROTA_BIN, runs it with one, and works in a no-.git export"
