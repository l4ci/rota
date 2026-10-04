echo "runner SECTION_LIST — a typo cannot fake an acceptance"
# SECTION_LIST narrows a run to the sections a phase owns. A relative path,
# a missing file or an empty list used to be skipped silently and the run still
# ended "All smoke tests passed." The runner now refuses all of them with exit 2
# before it sets anything up, and takes relative paths from the repo root.

slg_run() { # slg_run <list>: runs the real runner on that SECTION_LIST, prints "rc|output"
  local out rc=0
  out="$(SECTION_LIST="$1" bash "$TESTDIR/runner.sh" 2>&1)" || rc=$?
  printf '%s|%s' "$rc" "$out"
}

for bad in \
  "test/sections/no_such_section.sh" \
  "$TESTDIR/sections/no_such_section.sh" \
  "no_such_section.sh"; do
  RES="$(slg_run "$bad")"
  [ "${RES%%|*}" = 2 ] || fail "SECTION_LIST=$bad must exit 2, got: $RES"
  case "$RES" in *"is not an existing file"*) ;; *) fail "SECTION_LIST=$bad: no clear message: $RES" ;; esac
  case "$RES" in *"All smoke tests passed"*) fail "SECTION_LIST=$bad still printed a pass: $RES" ;; esac
done

RES="$(slg_run "test/lib.sh")"
[ "${RES%%|*}" = 2 ] || fail "a file outside test/sections must exit 2, got: $RES"
case "$RES" in *"not in test/sections"*) ;; *) fail "outside-sections message missing: $RES" ;; esac

for empty in "" "
"; do
  RES="$(slg_run "$empty")"
  [ "${RES%%|*}" = 2 ] || fail "an empty SECTION_LIST must exit 2, got: $RES"
  case "$RES" in *"names no section"*) ;; *) fail "empty-list message missing: $RES" ;; esac
done

# one good entry beside a typo refuses the whole run: nothing may pass
RES="$(slg_run "test/sections/71_no_pipe_grep_q.sh
test/sections/typo.sh")"
[ "${RES%%|*}" = 2 ] || fail "a typo beside a good entry must exit 2, got: $RES"
case "$RES" in *"All smoke tests passed"*) fail "a typo beside a good entry still passed: $RES" ;; esac

# a relative entry resolves against the repo root, whatever the cwd
RES="$(cd / && slg_run "test/sections/71_no_pipe_grep_q.sh")"
[ "${RES%%|*}" = 0 ] || fail "a relative entry must run, got: $RES"
case "$RES" in *"All smoke tests passed"*) ;; *) fail "relative entry did not run its section: $RES" ;; esac
pass "SECTION_LIST rejects missing, relative-miss, foreign and empty entries (exit 2) and resolves relative paths"
