echo "white-box census: no white-box assertion outside a marked block"
# test/whitebox-scan.awk fails any line that looks at bin/ internals (helper
# files, SKILL.md or reference greps) unless it sits between "white-box-begin: <tag>" and "white-box-end".
# The format is documented in docs/design/5.0-smoke-whitebox.md. Sample lines
# below are assembled from fragments so this file never trips the scan itself.
SCAN="$TESTDIR/whitebox-scan.awk"
WB_TMP="$(mktemp -d)"

BEGIN_KEEP='# white-box-begin: A9 #53 keep'
BEGIN_DOC='# white-box-begin: A9 #53 doclint'
BEGIN_UNIT='# white-box-begin: go-unit A5 #49'
END_MARK='# white-box-end'
SAMPLES=(
  '"$''BIN/x" arg'
  'ls "$''REPO"/bin/x'
  'D="$''REPO"'
  'D=$''REPO/bin'
  'D=$''BIN'
  'B''IN=/x'
  'export B''IN'
  'os.environ["B''IN"]'
  'ls "$''REPO/bin"'
  'ls bin/''*'
  'install_''helpers'
  'grep -q x rota-plan/SKILL''.md'
  'PYTHON''PATH=x python3 -c pass'
  'git ls-''files -s bin/x'
  'cat "$''REPO/references/x.md"'
)

wb_scan() { awk -f "$SCAN" "$1" 2>&1 || true; }
wb_file() { printf '%s\n' "$@" > "$WB_TMP/t.sh"; echo "$WB_TMP/t.sh"; }

# Clean input and comments are accepted.
OUT="$(wb_scan "$(wb_file 'echo ok' '"$ROTA_BIN" status' "# ${SAMPLES[0]}")")"
[ -z "$OUT" ] || fail "scanner flagged a clean file: $OUT"

# Near misses stay clean: plugin metadata and a name that merely ends in bin.
for S in 'cat "$''REPO/.claude-plugin/plugin.json"' 'D="$TMP_B''IN"'; do
  OUT="$(wb_scan "$(wb_file 'echo ok' "$S")")"
  [ -z "$OUT" ] || fail "scanner flagged a harmless line: $S => $OUT"
done

# Every signature must trip the scan outside a block, and pass inside every tag form.
for S in "${SAMPLES[@]}"; do
  OUT="$(wb_scan "$(wb_file 'echo ok' "$S")")"
  [ -n "$OUT" ] || fail "scanner missed a white-box line: $S"
  for B in "$BEGIN_KEEP" "$BEGIN_DOC" "$BEGIN_UNIT"; do
    OUT="$(wb_scan "$(wb_file "$B" "$S" "$END_MARK")")"
    [ -z "$OUT" ] || fail "scanner flagged a line inside '$B': $S => $OUT"
  done
  OUT="$(wb_scan "$(wb_file "$BEGIN_KEEP" "$S" "$END_MARK" "$S")")"
  [ -n "$OUT" ] || fail "scanner ignored a line after the block closed: $S"
done

# Malformed markers are rejected.
expect_flag() {
  local what="$1"; shift
  OUT="$(wb_scan "$(wb_file "$@")")"
  [ -n "$OUT" ] || fail "scanner accepted $what"
}
expect_flag "an unclosed block"          "$BEGIN_KEEP" 'echo x'
expect_flag "an end with no begin"       'echo x' "$END_MARK"
expect_flag "a nested begin"             "$BEGIN_KEEP" "$BEGIN_UNIT" "$END_MARK" "$END_MARK"
expect_flag "a phase with the wrong issue" '# white-box-begin: go-unit A3 #48' "$END_MARK"
expect_flag "go-unit for A9"             '# white-box-begin: go-unit A9 #53' "$END_MARK"
expect_flag "an unknown A9 kind"         '# white-box-begin: A9 #53 other' "$END_MARK"
expect_flag "a begin with no tag"        '# white-box-begin:' "$END_MARK"
expect_flag "the retired one-line marker" '# white-box: kept until A9 (#53)'
rm -rf "$WB_TMP"

# The real sections.
OUT="$(awk -f "$SCAN" "$TESTDIR"/sections/*.sh)" || fail "white-box scan failed to run"
[ -z "$OUT" ] || fail "white-box lines outside a marked block (wrap them in white-box-begin/end, or convert them to \$ROTA_BIN verb calls):
$OUT"
pass "every white-box assertion sits in a tagged white-box block"
