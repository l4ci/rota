echo "map stats: last_mapped_commit staleness warning, advisory, map.staleCommits (#926)"

MS="$(mktemp -d "$TMP/map-stale.XXXXXX")"
(
  cd "$MS" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo 0 > seed.txt && git add seed.txt && git commit -q -m seed
) || fail "map stale fixture repo setup failed"
MAPPED="$(git -C "$MS" rev-parse HEAD)"
for n in a b c; do
  ( cd "$MS" && echo "$n" > "$n.txt" && git add "$n.txt" && git commit -q -m "churn $n" ) || fail "churn commit $n failed"
done
mkdir -p "$MS/.rota/map"
printf '{}\n' > "$MS/.rota/config.json"
printf -- '---\nsubsystem: old\nsummary: mapped three commits ago\nlast_mapped_commit: %s\n---\nbody\n' "$MAPPED" > "$MS/.rota/map/old.md"
printf -- '---\nsubsystem: plain\nsummary: never stamped\n---\nbody\n' > "$MS/.rota/map/plain.md"
mst() { ( cd "$MS" && "$ROTA_BIN" "$@" ); }

# (a) below the default threshold: the value is reported, nothing warns
OUT="$(mst --json map stats 2>"$MS/err")" || fail "map stats should exit 0: $OUT"
[ "$(echo "$OUT" | jget data.subsystems[0].commitsBehind)" = "3" ] \
  || fail "old should be 3 commits behind: $OUT"
echo "$OUT" | grep -q '"plain".*commitsBehind' && fail "a file without last_mapped_commit must carry no commitsBehind: $OUT"
[ ! -s "$MS/err" ] || fail "no warning expected below the default threshold: $(cat "$MS/err")"
pass "map stats reports commitsBehind per stamped subsystem and stays quiet below map.staleCommits"

# (b) above the threshold: a note on stdout and stderr, still exit 0, same value in --json
mst config set map.staleCommits 2 >/dev/null || fail "config set map.staleCommits 2 failed"
rc=0; TXT="$(mst map stats 2>"$MS/err")" || rc=$?
[ "$rc" = 0 ] || fail "map stats must exit 0 when it warns, got $rc"
echo "$TXT" | grep -q '^note: map old is 3 commits behind HEAD' || fail "expected a note: line on stdout: $TXT"
grep -q 'map old is 3 commits behind HEAD' "$MS/err" || fail "expected the warning on stderr: $(cat "$MS/err")"
echo "$TXT" | grep -q 'plain' && echo "$TXT" | grep -q '^note: map plain' && fail "an unstamped file must not warn: $TXT"
JSON="$(mst --json map stats 2>/dev/null)" || fail "map stats --json should exit 0 when it warns"
[ "$(echo "$JSON" | jget data.subsystems[0].commitsBehind)" = "3" ] || fail "--json should carry commitsBehind: $JSON"
pass "map stats warns above map.staleCommits, exits 0, and --json carries the value"

# (c) 0 disables the warning; a negative value is rejected
mst config set map.staleCommits 0 >/dev/null || fail "config set map.staleCommits 0 failed"
rc=0; TXT="$(mst map stats 2>"$MS/err")" || rc=$?
[ "$rc" = 0 ] && ! echo "$TXT" | grep -q '^note:' && [ ! -s "$MS/err" ] \
  || fail "map.staleCommits 0 must not warn: rc=$rc $TXT $(cat "$MS/err")"
rc=0; mst config set map.staleCommits -1 >/dev/null 2>&1 || rc=$?
[ "$rc" != 0 ] || fail "a negative map.staleCommits must be rejected"
pass "map.staleCommits 0 disables the warning and a negative value is rejected"

# (d) the skill documents the mode
grep -q 'last_mapped_commit' "$REPO/skills/rota-refactor/options.md" || fail "options.md should describe --map and last_mapped_commit"
grep -q -- '--map' "$REPO/skills/rota-refactor/SKILL.md" || fail "SKILL.md should list --map"
pass "rota-refactor documents --map"
