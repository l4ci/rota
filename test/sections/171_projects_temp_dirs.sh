#!/usr/bin/env bash
echo "temporary projects: init explains the skip and cleanup removes legacy entries (#2107)"

TMP_PT="$(mktemp -d "$TMP/projects-temp.XXXXXX")"
(
  export XDG_CONFIG_HOME="$TMP_PT/config" XDG_STATE_HOME="$TMP_PT/state"
  export TMPDIR="$TMP_PT/scratch"
  mkdir -p "$TMPDIR/repo"
  OUT=$(cd "$TMPDIR/repo" && "$ROTA_BIN" init --no-blocks) || vfail
  NOTICE_LINES=$(printf '%s\n' "$OUT" | awk '$0 == "project not registered: temporary directory" { n++ } END { print n+0 }')
  echo "temp init notice lines: $NOTICE_LINES"
  [ "$NOTICE_LINES" = 1 ] || fail "temporary init must explain its skip on exactly one line: $OUT"
  [ -f "$TMPDIR/repo/.rota/config.json" ] || fail "temporary init must still seed .rota/"
  [ ! -e "$XDG_STATE_HOME/rota/projects.json" ] || fail "temporary init wrote a registry"

  mkdir -p "$XDG_STATE_HOME/rota"
  python3 - "$XDG_STATE_HOME/rota/projects.json" "$TMPDIR/repo" <<'PY'
import json, os, sys
with open(sys.argv[1], 'w') as f:
    json.dump({'projects': [{'name': 'repo', 'path': os.path.realpath(sys.argv[2]),
                             'lastSeen': '2026-01-01T00:00:00Z'}]}, f)
PY
  OUT=$("$ROTA_BIN" projects cleanup) || vfail
  case "$OUT" in "removed"$'\t'"repo"$'\t'*) ;; *) fail "cleanup must list the temporary entry as removed: $OUT" ;; esac
  OUT=$("$ROTA_BIN" --json projects) || vfail
  [ "$(jget data.projects <<<"$OUT")" = '[]' ] || fail "cleanup left the temporary entry registered: $OUT"
  [ -f "$TMPDIR/repo/.rota/config.json" ] || fail "cleanup removed project files"
)
rm -rf "$TMP_PT"
pass "temporary init emits one notice and cleanup removes the legacy registration"
