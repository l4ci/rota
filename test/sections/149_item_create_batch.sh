echo "item create --batch-file: each filed item keeps its own title and summary; weak titles refused (#838)"

TMP_BC="$(mktemp -d)"
trap 'rm -rf "$TMP_BC"' EXIT
mkdir -p "$TMP_BC/.rota"
printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > "$TMP_BC/.rota/BACKLOG.md"
bc() { ( cd "$TMP_BC" && "$ROTA_BIN" --json "$@" 2>&1 ); }

cat > "$TMP_BC/findings.json" <<'JSON'
[
 {"kind":"tasks","title":"Extract the alpha seam","desc":"alpha summary line","body":"## Acceptance\n- [ ] alpha"},
 {"kind":"tasks","title":"Deepen the beta module","desc":"beta summary line","body":"## Acceptance\n- [ ] beta"},
 {"kind":"tasks","title":"Merge gamma helper copies","desc":"gamma summary line","body":"## Acceptance\n- [ ] gamma"}
]
JSON

rc=0
OUT=$(bc item create --batch-file "$TMP_BC/findings.json") || rc=$?
[ "$rc" -eq 0 ] || fail "batch create exited $rc: $OUT"
[ "$(printf '%s' "$OUT" | grep -o '"id"' | wc -l)" -eq 3 ] || fail "batch printed fewer than three ids: $OUT"

# Re-read what landed: every title sits on the same line as its own summary.
for pair in "alpha seam|alpha summary" "beta module|beta summary" "gamma helper|gamma summary"; do
  t="${pair%%|*}"; d="${pair##*|}"
  line=$(grep -F "$t" "$TMP_BC/.rota/BACKLOG.md" || true)
  case "$line" in *"$d"*) ;; *) fail "title [$t] is not paired with its summary [$d]: $line" ;; esac
done

# A weak title anywhere refuses the whole batch with exit 2 and files nothing.
printf '[{"kind":"tasks","title":"A perfectly fine title"},{"kind":"tasks","title":"x"}]' > "$TMP_BC/weak.json"
before=$(wc -l < "$TMP_BC/.rota/BACKLOG.md")
rc=0
OUT=$(bc item create --batch-file "$TMP_BC/weak.json") || rc=$?
[ "$rc" -eq 2 ] || fail "weak batch title: want exit 2, got $rc"
[ "$(wc -l < "$TMP_BC/.rota/BACKLOG.md")" -eq "$before" ] || fail "a refused batch still filed an item"

# A placeholder is refused on the single-item path too.
rc=0
OUT=$(bc item create --kind tasks --title TBD) || rc=$?
[ "$rc" -eq 2 ] || fail "placeholder title: want exit 2, got $rc"

rm -rf "$TMP_BC"
trap 'rm -rf "$TMP"' EXIT
pass "item create --batch-file pairs titles with bodies"
