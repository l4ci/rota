echo "T4: knowledge replace and milestone overview — in-place corrections through verbs"

TMP_KRP="$(mktemp -d)"
trap 'rm -rf "$TMP_KRP"' EXIT
mkdir -p "$TMP_KRP/.rota"
( cd "$TMP_KRP" && git init -q )

cat > "$TMP_KRP/.rota/KNOWLEDGE.md" <<'MD'
# Knowledge

## Build

- **Run /hv-work first** — Use `.hv/status.json`; wrapped
  second line mentions /hv-work again. <!-- 2026-05-15 -->
- **Other tip** — Keep it short. <!-- 2026-05-15 -->
MD
hvj -C "$TMP_KRP" knowledge tier set --topic Build --title "Run /hv-work first" --tier confirmed >/dev/null

# ---------- replace: body edit across a wrapped bullet ----------
OUT=$(hvj -C "$TMP_KRP" knowledge replace --topic Build --old "/hv-work" --new "/rota-work") \
  || fail "replace returned non-zero: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "replace should report changed: $OUT"
grep -q "Run /rota-work first" "$TMP_KRP/.rota/KNOWLEDGE.md" || fail "title not replaced"
grep -q "mentions /rota-work again" "$TMP_KRP/.rota/KNOWLEDGE.md" || fail "wrapped line not replaced"
grep -q "Keep it short" "$TMP_KRP/.rota/KNOWLEDGE.md" || fail "sibling bullet lost"
pass "replace edits one wrapped bullet, sibling untouched"

# The title changed, so the tier entry follows it.
OUT=$(hvj -C "$TMP_KRP" knowledge tier get --topic Build --title "Run /rota-work first") || fail "tier get after retitle: $OUT"
[ "$(jget data.tier <<<"$OUT")" = "confirmed" ] || fail "tier did not follow the retitle: $OUT"
pass "replace re-keys the tier entry when the title changes"

# ---------- replace: refusals ----------
RC=0; hvj -C "$TMP_KRP" knowledge replace --topic Build --old "zzz" --new x >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "no match should exit 3; got $RC"
RC=0; hvj -C "$TMP_KRP" knowledge replace --topic Build --old "e" --new x >/dev/null 2>&1 || RC=$?
[ "$RC" = "4" ] || fail "match in two bullets should exit 4; got $RC"
RC=0; hvj -C "$TMP_KRP" knowledge replace --topic Build --old "x" >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "missing --new should exit 2; got $RC"
pass "replace refuses missing and ambiguous matches"

# ---------- milestone overview ----------
cat > "$TMP_KRP/.rota/MILESTONES.md" <<'MD'
# Milestones

hv-skills is old wording.

## Active milestones

_(none active)_
MD
OUT=$(printf 'rota plans before coding.\n' | hvj -C "$TMP_KRP" milestone overview --body-file -) \
  || fail "overview returned non-zero: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "overview should report changed: $OUT"
[ "$(cat "$TMP_KRP/.rota/MILESTONES.md")" = "$(printf '# Milestones\n\nrota plans before coding.\n\n## Active milestones\n\n_(none active)_')" ] \
  || fail "overview rewrite touched more than the overview"
RC=0; printf 'x\n## Sneaky\n' | hvj -C "$TMP_KRP" milestone overview --body-file - >/dev/null 2>&1 || RC=$?
[ "$RC" = "4" ] || fail "heading in the body should exit 4; got $RC"
pass "milestone overview replaces only the overview text"

rm -rf "$TMP_KRP"
trap 'rm -rf "$TMP"' EXIT
