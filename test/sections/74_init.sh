echo "A9: rota init seeds .rota/, runs the blocks, and rota init check reports"

TMP_IN="$(mktemp -d)"
trap 'rm -rf "$TMP_IN"' EXIT
jfield() { python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(json.dumps(d.get(sys.argv[1], "ABSENT")))' "$1"; }

# (a) clean dir: seeded, blocks written, AGENTS.md is the instructions file
mkdir -p "$TMP_IN/fresh"
OUT="$(hvj -C "$TMP_IN/fresh" init)" || vfail
for f in BACKLOG KNOWLEDGE DECISIONS MAP MILESTONES; do
  [ -f "$TMP_IN/fresh/.rota/$f.md" ] || fail "A9[a]: .rota/$f.md not seeded"
done
for f in counters status repos config; do
  [ -f "$TMP_IN/fresh/.rota/$f.json" ] || fail "A9[a]: .rota/$f.json not seeded"
done
grep -qx '.worktrees/' "$TMP_IN/fresh/.gitignore" || fail "A9[a]: .worktrees/ not ignored"
[ "$(printf '%s' "$OUT" | jfield changed)" = "true" ] || fail "A9[a]: changed is not true"
KEYS="$(printf '%s' "$OUT" | python3 -c 'import json,sys; print(",".join(b["key"] for b in json.load(sys.stdin)["data"]["blocks"]))')"
[ "$KEYS" = "skills,knowledge,milestones,decisions,map,qa" ] || fail "A9[a]: blocks were: $KEYS"
grep -q "<!-- rota-skills-start -->" "$TMP_IN/fresh/AGENTS.md" || fail "A9[a]: skills block missing from AGENTS.md"
grep -qx '@AGENTS.md' "$TMP_IN/fresh/CLAUDE.md" || fail "A9[a]: CLAUDE.md does not import AGENTS.md"
pass "A9[a]: rota init seeds .rota/ and writes the six blocks"

# (b) a second run is a no-op
OUT="$(hvj -C "$TMP_IN/fresh" init)" || vfail
[ "$(printf '%s' "$OUT" | jfield changed)" = "false" ] || fail "A9[b]: second run changed something"
[ "$(printf '%s' "$OUT" | jfield created)" = "[]" ] || fail "A9[b]: second run created paths"
pass "A9[b]: rota init is idempotent"

# (c) --no-blocks seeds only
mkdir -p "$TMP_IN/nb"
OUT="$(hvj -C "$TMP_IN/nb" init --no-blocks)" || vfail
[ "$(printf '%s' "$OUT" | jfield blocks)" = '"ABSENT"' ] || fail "A9[c]: blocks reported under --no-blocks"
[ ! -e "$TMP_IN/nb/AGENTS.md" ] || fail "A9[c]: AGENTS.md written under --no-blocks"
[ -f "$TMP_IN/nb/.rota/config.json" ] || fail "A9[c]: .rota/config.json not seeded"
pass "A9[c]: --no-blocks seeds .rota/ and nothing else"

# (d) rota init check: uninitialized reports every missing path, exit 1
mkdir -p "$TMP_IN/empty"
rc=0; OUT="$(hvj -C "$TMP_IN/empty" init check)" || rc=$?
[ "$rc" = 1 ] || fail "A9[d]: uninitialized init check exits $rc, want 1"
[ "$(printf '%s' "$OUT" | jfield missing)" = '[".rota"]' ] || fail "A9[d]: missing was $(printf '%s' "$OUT" | jfield missing)"
rm "$TMP_IN/nb/.rota/counters.json" "$TMP_IN/nb/.rota/status.json"
rc=0; OUT="$(hvj -C "$TMP_IN/nb" init check)" || rc=$?
[ "$rc" = 1 ] || fail "A9[d]: partial init check exits $rc, want 1"
[ "$(printf '%s' "$OUT" | jfield missing)" = '[".rota/counters.json", ".rota/status.json"]' ] || fail "A9[d]: partial missing was $(printf '%s' "$OUT" | jfield missing)"
hvj -C "$TMP_IN/fresh" init check >/dev/null || fail "A9[d]: initialized init check did not exit 0"
pass "A9[d]: init check lists every missing path, exit 1; 0 when initialized"

# (f) a corrupt counters.json is refused, not overwritten
mkdir -p "$TMP_IN/bad/.rota"
printf '{oops' > "$TMP_IN/bad/.rota/counters.json"
rc=0; hvj -C "$TMP_IN/bad" init --no-blocks >/dev/null || rc=$?
[ "$rc" = 70 ] || fail "A9[f]: corrupt counters exits $rc, want 70"
[ "$(cat "$TMP_IN/bad/.rota/counters.json")" = '{oops' ] || fail "A9[f]: corrupt counters.json was rewritten"
pass "A9[f]: corrupt counters.json exits 70 and is left alone"

trap 'rm -rf "$TMP"' EXIT
rm -rf "${TMP_IN:?}"
