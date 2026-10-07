echo "--ui: refused off a terminal, on a verb without a view and with --json; plain output carries no ANSI (#540)"

# Smoke has no tty, so every --ui is refused: a verb without a view (version)
# and a verb with one (doctor) alike. The refusal comes before the verb runs
# and names the plain verb.
rc=0; ERR=$( "$ROTA_BIN" version --ui 2>&1 >/dev/null ) || rc=$?
[ "$rc" = 2 ] || fail "version --ui should exit 2, got $rc: $ERR"
case "$ERR" in *"no --ui view for this verb"*) ;; *) fail "version --ui should say it has no view: $ERR" ;; esac
case "$ERR" in *"hint: run: rota version"*) ;; *) fail "version --ui should hint the plain verb: $ERR" ;; esac

rc=0; ERR=$( "$ROTA_BIN" --ui doctor 2>&1 >/dev/null ) || rc=$?
[ "$rc" = 2 ] || fail "--ui doctor should exit 2, got $rc: $ERR"
case "$ERR" in *"hint: run: rota doctor"*) ;; *) fail "doctor --ui should hint the plain verb: $ERR" ;; esac

rc=0; ERR=$( "$ROTA_BIN" doctor --ui 2>&1 >/dev/null ) || rc=$?
[ "$rc" = 2 ] || fail "doctor --ui off a terminal should exit 2, got $rc: $ERR"
case "$ERR" in *"--ui needs an interactive terminal"*) ;; *) fail "doctor --ui should say it needs a terminal: $ERR" ;; esac
case "$ERR" in *"hint: run: rota doctor"*) ;; *) fail "doctor --ui should hint the plain verb: $ERR" ;; esac

rc=0; OUT=$( hvj doctor --ui 2>/dev/null ) || rc=$?
[ "$rc" = 2 ] || fail "doctor --ui --json should exit 2, got $rc: $OUT"
[ "$(echo "$OUT" | jget 'error.message')" = "--ui and --json do not mix" ] || fail "doctor --ui --json refusal: $OUT"

rc=0; OUT=$( hvj version --ui 2>/dev/null ) || rc=$?
[ "$rc" = 2 ] || fail "version --ui --json should exit 2, got $rc: $OUT"
[ "$(echo "$OUT" | jget 'error.code')" = "usage" ] || fail "version --ui --json should be a usage envelope: $OUT"
[ "$(echo "$OUT" | jget 'error.message')" = "--ui and --json do not mix" ] || fail "--json refusal comes first: $OUT"

# Output never depends on the terminal: plain and --json carry no ESC byte.
ESC=$(printf '\033')
case "$( "$ROTA_BIN" version )" in *"$ESC"*) fail "plain version output carries an ESC byte" ;; esac
case "$( hvj version )" in *"$ESC"*) fail "version --json output carries an ESC byte" ;; esac
pass "--ui refusals exit 2 with a hint naming the plain verb; plain and --json output carry no ANSI"
