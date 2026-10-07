echo "an empty test.full is warned about by init and round start before the gate refuses (#493)"

WV="$(mktemp -d "$TMP/warnverify.XXXXXX")"
git init -q --bare "$WV/origin.git"
(
  cd "$WV" && mkdir proj && cd proj && git init -q -b main . && git config user.email a@b && git config user.name n \
    && git commit -q --allow-empty -m init && git remote add origin "$WV/origin.git" && git push -q origin main
) || fail "warn-verify fixture setup failed"
WP="$WV/proj"
WVENV="env -u HERDR_ENV -u HERDR_PANE_ID -u TMUX -u TMUX_PANE"
wv() { ( cd "$WP" && $WVENV "$ROTA_BIN" "$@" 2>&1 ); }

# AC-2: init ends with the line, as its last line.
OUT=$(wv init) || fail "init failed: $OUT"
[ "$(echo "$OUT" | tail -n 1)" = "test.full is empty: the merge gate will refuse to merge until it is set (rota config set test.full '[...]')" ] \
  || fail "init should end with the empty test.full line: $OUT"
pass "init ends with the empty test.full line"

# AC-3: round start warns the same way.
printf 'stub worker contract\n' > "$WV/contract.md"
wv config set round.brief "$WV/contract.md" >/dev/null
OUT=$(wv round start --holder-pid $$ --slots 1) || fail "round start failed: $OUT"
case "$OUT" in *"test.full is empty: the merge gate will refuse"*) ;; *) fail "round start should warn about the empty test.full: $OUT" ;; esac
pass "round start warns while test.full is empty"

# Once test.full is set, neither speaks.
wv config set test.full '["true"]' >/dev/null
OUT=$(wv round start --holder-pid $$ --slots 1) || fail "round start failed: $OUT"
case "$OUT" in *"test.full is empty"*) fail "round start should not warn with test.full set: $OUT" ;; esac
OUT=$(wv init) || fail "init failed: $OUT"
case "$OUT" in *"test.full is empty"*) fail "init should not warn with test.full set: $OUT" ;; esac
pass "no warning once test.full is set"
