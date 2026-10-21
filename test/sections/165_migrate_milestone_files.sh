echo "migrate milestone-files: file mode refuses, the files stay (#1288)"

TMP_MF="$(mktemp -d)"
trap 'rm -rf "$TMP_MF"' EXIT
mkdir -p "$TMP_MF/.rota/milestones"
printf '{"issues":{"source":"file"}}\n' > "$TMP_MF/.rota/config.json"
printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > "$TMP_MF/.rota/BACKLOG.md"
printf -- '---\nid: M01\ntitle: T\nstatus: planned\ndepends: []\ncreated: 2026-01-01\n---\n' > "$TMP_MF/.rota/milestones/M01.md"

rc=0
( cd "$TMP_MF" && "$ROTA_BIN" migrate milestone-files --apply >/dev/null 2>&1 ) || rc=$?
[ "$rc" -eq 4 ] || fail "file mode exited $rc, want 4"
[ -f "$TMP_MF/.rota/milestones/M01.md" ] || fail "file mode lost its milestone file"
[ ! -e "$TMP_MF/.rota/archive/milestones/M01.md" ] || fail "file mode archived a live milestone"
rm -rf "$TMP_MF"
trap 'rm -rf "$TMP"' EXIT
pass "migrate milestone-files refuses in file mode"
