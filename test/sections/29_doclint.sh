echo "doclint: skills and references pass the validator; the prose lint catches drift (#173)"
VALIDATE="$TESTDIR/validate-skills.py"
DL_TMP="$(mktemp -d)"
SK='SKILL''.md'

# The real tree.
OUT="$(cd "$REPO" && python3 "$VALIDATE" 2>&1)" || fail "validate-skills fails on the repo: $OUT"
pass "skills and references pass the doclint"

# The prose lint (#173): drop a pinned phrase from a copy of the real skills and
# the validator names the file; a deleted target file is reported, not skipped.
# white-box-begin: A9 #53 doclint
PL="$DL_TMP/prose"; mkdir -p "$PL"
cp -R "$REPO"/rota-* "$REPO/references" "$REPO/docs" "$REPO/README.md" "$REPO/CHANGELOG.md" "$PL/"
OUT="$(cd "$PL" && python3 "$VALIDATE" 2>&1)" || fail "prose lint fails on a copy of the repo: $OUT"
sed -i 's/rota status loop start/rota status loop begin/' "$PL/rota-work/$SK"
RC=0; OUT="$(cd "$PL" && python3 "$VALIDATE" 2>&1)" || RC=$?
[ "$RC" = 1 ] && grep -qF "rota-work/$SK: must call rota status loop start" <<<"$OUT" \
  || fail "prose lint missed a dropped phrase (rc $RC): $OUT"
rm -f "$PL/references/manual-gates.md"
RC=0; OUT="$(cd "$PL" && python3 "$VALIDATE" 2>&1)" || RC=$?
[ "$RC" = 1 ] && grep -qF "references/manual-gates.md: prose rule target is missing" <<<"$OUT" \
  || fail "prose lint skipped a missing target file (rc $RC): $OUT"
# white-box-end
pass "the prose lint names a skill that lost a pinned phrase and a missing target file"

rm -rf "${DL_TMP:?}"
