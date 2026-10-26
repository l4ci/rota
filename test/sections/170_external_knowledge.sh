echo "external knowledge: seen: stamps, edit verbs and the (seen on …) migration (#2110)"

TMP_EK="$(mktemp -d "$TMP/extknow.XXXXXX")"
EKREMOTE="$TMP_EK/remote.git"; EKSEED="$TMP_EK/seed"; EKCLONE="$TMP_EK/clone"; EKPROJ="$TMP_EK/proj"
git init -q --bare -b main "$EKREMOTE"
mkdir -p "$EKSEED/topics"
(
  cd "$EKSEED" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && printf '# Knowledge index\n\n- [Xcode](topics/xcode.md) — build tooling\n' > README.md \
    && printf '# Xcode\n\n- **Build number** — Bump it. (seen on 26.5) <!-- 2026-10-01 source:app -->\n' > topics/xcode.md \
    && git add -A && git commit -q -m seed && git remote add origin "$EKREMOTE" && git push -q origin main
) || fail "external knowledge fixture setup failed"
git clone -q "$EKREMOTE" "$EKCLONE" && git -C "$EKCLONE" config user.email t@t && git -C "$EKCLONE" config user.name t
mkdir -p "$EKPROJ/.rota"
printf 'config/features=PackedStringArray("4.8", "Forward Plus")\n' > "$EKPROJ/project.godot"
( cd "$EKPROJ" && git init -q -b main . ) || fail "project fixture failed"
ek() { ( cd "$EKPROJ" && "$ROTA_BIN" --json "$@" ); }
ekn() { ( cd "$EKPROJ" && "$ROTA_BIN" "$@" ); }
RC=0; ek config set knowledge.external "$EKCLONE" --global >/dev/null || RC=$?
[ "$RC" = "0" ] || fail "config set knowledge.external exited $RC"

# add --seen writes the stamp.
RC=0; OUT=$(printf 'Kill tweens on exit.\n' | ek knowledge add --external --topic Godot --title "Tween leak" --body-file - --seen godot@4.6) || RC=$?
[ "$RC" = "0" ] || fail "add --seen exited $RC: $OUT"
grep -q 'seen:godot@4.6 -->' "$EKCLONE/topics/godot.md" || fail "stamp lacks seen:godot@4.6: $(cat "$EKCLONE/topics/godot.md")"
pass "add --seen writes seen:godot@4.6 into the stamp"

# query flags a bullet the project has outgrown (project.godot says 4.8).
RC=0; OUT=$(ekn knowledge query --external Godot) || RC=$?
[ "$RC" = "0" ] || fail "query exited $RC"
case "$OUT" in *"seen on godot 4.6, project on 4.8: recheck"*) ;; *) fail "no recheck marker: $OUT" ;; esac
RC=0; OUT=$(ek knowledge query --external Godot) || RC=$?
[ "$(jget 'data.recheck[0].tool' <<<"$OUT")" = "godot" ] || fail "data.recheck missing the tool: $OUT"
pass "query flags a bullet seen on an older project version"

# amend --seen re-confirms; the flag goes away.
RC=0; OUT=$(ek knowledge amend --external --topic Godot --fragment "Tween leak" --seen godot@4.8) || RC=$?
[ "$RC" = "0" ] || fail "amend --seen exited $RC: $OUT"
RC=0; OUT=$(ekn knowledge query --external Godot) || RC=$?
case "$OUT" in *recheck*) fail "bullet still flagged after amend: $OUT" ;; esac
[ "$(git -C "$EKREMOTE" rev-parse main)" = "$(git -C "$EKCLONE" rev-parse HEAD)" ] || fail "amend did not push"
pass "amend --seen clears the recheck flag"

# rm drops the bullet.
RC=0; OUT=$(ek knowledge rm --external --topic Godot --title "Tween leak") || RC=$?
[ "$RC" = "0" ] || fail "rm exited $RC: $OUT"
if grep -q 'Tween leak' "$EKCLONE/topics/godot.md"; then fail "rm left the bullet"; fi
pass "rm --external removes the bullet"

# migrate: seed old-style suffixes, then preview, apply, re-apply.
cat > "$EKCLONE/topics/godot.md" <<'MD'
# Godot

- **Tween leak** — Kill tweens on exit. (seen on Godot 4.6) <!-- 2026-09-01 source:game -->
- **Two tools** — Build it. (seen on Swift 6, macOS 26) <!-- 2026-09-02 source:app -->
- **Legacy** — Old one. (seen on three.js r186) <!-- 2026-08-02 source:kluft+Upracked -->
- **Mid** — Pinned (seen on 3.49.2). Go on. <!-- 2026-09-03 source:db -->
MD
git -C "$EKCLONE" add -A && git -C "$EKCLONE" commit -q -m "seed suffixes" && git -C "$EKCLONE" push -q origin main || fail "seeding suffixes failed"
BEFORE="$(git -C "$EKCLONE" rev-list --count HEAD)"
RC=0; OUT=$(ek migrate external-knowledge) || RC=$?
[ "$RC" = "0" ] || fail "migrate preview exited $RC: $OUT"
[ "$(jget data.count <<<"$OUT")" = "4" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "preview data: $OUT"
[ "$(jget 'data.manualReview[0].title' <<<"$OUT")" = "Mid" ] || fail "mid-body suffix not in manualReview: $OUT"
[ "$(git -C "$EKCLONE" rev-list --count HEAD)" = "$BEFORE" ] || fail "preview committed"
pass "migrate preview lists 4 moves and the mid-body case, writes nothing"

RC=0; OUT=$(ek migrate external-knowledge --apply) || RC=$?
[ "$RC" = "0" ] || fail "migrate --apply exited $RC: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "apply should report changed: $OUT"
grep -q 'source:game seen:godot@4.6 -->' "$EKCLONE/topics/godot.md" || fail "godot stamp: $(cat "$EKCLONE/topics/godot.md")"
grep -q 'seen:swift@6,macos@26 -->' "$EKCLONE/topics/godot.md" || fail "two-tool stamp missing"
grep -q 'source:kluft+Upracked seen:three.js@r186 -->' "$EKCLONE/topics/godot.md" || fail "kluft+Upracked source not kept"
grep -q 'Pinned (seen on 3.49.2). Go on.' "$EKCLONE/topics/godot.md" || fail "mid-body text was touched"
grep -q 'seen:xcode@26.5 -->' "$EKCLONE/topics/xcode.md" || fail "tool-less suffix did not take the topic slug"
[ "$(git -C "$EKCLONE" rev-list --count HEAD)" = "$((BEFORE + 1))" ] || fail "apply should make exactly one commit"
[ "$(git -C "$EKREMOTE" rev-parse main)" = "$(git -C "$EKCLONE" rev-parse HEAD)" ] || fail "apply did not push"
pass "migrate --apply rewrites every suffix in one pushed commit"

AFTER="$(git -C "$EKCLONE" rev-list --count HEAD)"
RC=0; OUT=$(ek migrate external-knowledge --apply) || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "re-apply should report changed false: $OUT"
[ "$(git -C "$EKCLONE" rev-list --count HEAD)" = "$AFTER" ] || fail "re-apply committed"
pass "a second migrate --apply changes nothing"

# stats --external measures each topic; doctor warns on a topic past the bullet limit (#2109).
{
  printf '# Big\n\n'
  for i in $(seq 1 26); do printf -- '- **Bullet %s** — Filler. <!-- 2026-10-01 source:app -->\n' "$i"; done
} > "$EKCLONE/topics/big.md"
printf -- '- [Big](topics/big.md) — a topic past the bullet limit\n' >> "$EKCLONE/README.md"
git -C "$EKCLONE" add -A && git -C "$EKCLONE" commit -q -m "add big topic" && git -C "$EKCLONE" push -q origin main || fail "seeding the big topic failed"
RC=0; OUT=$(ekn knowledge stats --external) || RC=$?
[ "$RC" = "0" ] || fail "stats --external exited $RC: $OUT"
case "$OUT" in *"Big: 26 bullets, "*" bytes"*) ;; *) fail "stats --external lacks the Big line: $OUT" ;; esac
RC=0; OUT=$(ek knowledge stats --external) || RC=$?
[ "$(jget 'data.topics[2].file' <<<"$OUT")" = "topics/big.md" ] && [ "$(jget 'data.topics[2].bullets' <<<"$OUT")" = "26" ] || fail "stats --external JSON: $OUT"
pass "stats --external prints bullets and bytes per topic"

# Doctor looks tools up in ROTA_TEST_DOCTOR_PATH, so no real codex or forge CLI runs.
EKBIN="$TMP_EK/bin"; mkdir -p "$EKBIN"; ln -s "$(command -v git)" "$EKBIN/git"
RC=0; OUT=$(ROTA_TEST_DOCTOR_PATH="$EKBIN" ek doctor 2>/dev/null) || RC=$?
python3 -c '
import json,sys
cs={c["name"]:c for c in json.load(sys.stdin)["data"]["checks"]}
c=cs["external knowledge size"]
assert c["status"]=="warn", c
assert "Big (26 bullets, limit 25)" in c["detail"], c
assert "/rota-learn --external-review" in c["hint"], c' <<<"$OUT" || fail "doctor lacks the external knowledge size warning: $OUT"
pass "doctor warns on a topic over knowledge.externalMaxBullets"

rm -rf "$TMP_EK"
