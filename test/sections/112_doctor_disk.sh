echo "#85: rota doctor warns on low free disk and names reclaimable leftovers"

TMP_DD="$(mktemp -d)"
trap 'rm -rf "$TMP_DD"' EXIT
mkdir -p "$TMP_DD/bin" "$TMP_DD/proj/.rota" "$TMP_DD/tmpdir"
ln -s "$(command -v git)" "$TMP_DD/bin/git"
printf '{"doctor":{"minFreeDiskPercent":10}}\n' > "$TMP_DD/proj/.rota/config.json"
# A temp dir a run left behind long ago, in the temp root doctor scans.
mkdir -p "$TMP_DD/tmpdir/tmp.leaked" && head -c 4096 /dev/zero > "$TMP_DD/tmpdir/tmp.leaked/blob"
touch -d '3 hours ago' "$TMP_DD/tmpdir/tmp.leaked"
mkdir -p "$TMP_DD/tmpdir/tmp.fresh"

dd_doctor() { # dd_doctor <free:total>: doctor text in the fixture; the exit code does not matter here
  ( cd "$TMP_DD/proj" && TMPDIR="$TMP_DD/tmpdir" ROTA_TEST_DOCTOR_PATH="$TMP_DD/bin" ROTA_TEST_DOCTOR_DISK="$1" "$ROTA_BIN" doctor 2>&1 ) || true
}
dd_field() { # dd_field <check> <field>: from the --json envelope
  ( cd "$TMP_DD/proj" && TMPDIR="$TMP_DD/tmpdir" ROTA_TEST_DOCTOR_PATH="$TMP_DD/bin" ROTA_TEST_DOCTOR_DISK="$3" "$ROTA_BIN" --json doctor 2>/dev/null || true ) | python3 -c '
import json,sys
cs={c["name"]:c for c in json.load(sys.stdin)["data"]["checks"]}
print(cs[sys.argv[1]].get(sys.argv[2],"ABSENT") if sys.argv[1] in cs else "NOCHECK")' "$1" "$2"
}

OUT="$(dd_doctor 1000:100000)"
case "$OUT" in *$'warn\tdisk\t'*"under the 10% threshold"*) ;; *) fail "1% free should warn about disk: $OUT" ;; esac
[ "$(dd_field disk status 1000:100000)" = "warn" ] || fail "disk check status should be warn"
case "$(dd_field disk hint 1000:100000)" in *"1 leaked temp dirs"*) ;; *) fail "the warning should name the leaked temp dir (and not the fresh one): $(dd_field disk hint 1000:100000)" ;; esac
pass "low free disk warns and names the leaked temp dirs"

[ "$(dd_field disk status 50000:100000)" = "NOCHECK" ] || fail "a healthy disk should add no disk line"
pass "a healthy disk adds no line"

( cd "$TMP_DD/proj" && printf '{"doctor":{"minFreeDiskPercent":0}}\n' > .rota/config.json )
[ "$(dd_field disk status 1000:100000)" = "NOCHECK" ] || fail "doctor.minFreeDiskPercent 0 should turn the check off"
rm -rf "$TMP_DD"
trap 'rm -rf "$TMP"' EXIT
pass "doctor.minFreeDiskPercent 0 turns the check off"
