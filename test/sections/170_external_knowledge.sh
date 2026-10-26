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
assert "Big (26 bullets > 25)" in c["detail"], c
assert "/rota-learn --external-review" in c["hint"], c' <<<"$OUT" || fail "doctor lacks the external knowledge size warning: $OUT"
pass "doctor warns on a topic over knowledge.externalMaxBullets"

# split moves the bullets into subtopic files, keeps every line, and reads reach the parts (#2109).
count_bullets() { cat "$EKCLONE"/topics/big.md "$EKCLONE"/topics/big/*.md 2>/dev/null | grep -c '^- \*\*'; }
[ "$(count_bullets)" = "26" ] || fail "fixture should hold 26 bullets, has $(count_bullets)"
python3 - "$TMP_EK/plan.json" <<'PY'
import json, sys
t = lambda a, b: ["Bullet %d" % i for i in range(a, b + 1)]
json.dump({"subtopics": [
    {"name": "Alpha", "hook": "the first thirteen", "titles": t(1, 13)},
    {"name": "Beta", "hook": "the next seven", "titles": t(14, 20)},
]}, open(sys.argv[1], "w"))
PY
BEFORE="$(git -C "$EKCLONE" rev-list --count HEAD)"
RC=0; OUT=$(ek knowledge split --external --topic Big --plan "$TMP_EK/plan.json") || RC=$?
[ "$RC" = "0" ] || fail "split exited $RC: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] && [ "$(jget data.left <<<"$OUT")" = "6" ] && [ "$(jget 'data.subtopics[0].file' <<<"$OUT")" = "topics/big/alpha.md" ] || fail "split data: $OUT"
[ ! -e "$EKCLONE/topics/big.md" ] || fail "split left the flat file"
[ "$(count_bullets)" = "26" ] || fail "split lost or doubled bullets: $(count_bullets) left"
[ "$(git -C "$EKCLONE" rev-list --count HEAD)" = "$((BEFORE + 1))" ] || fail "split should make exactly one commit"
[ "$(git -C "$EKREMOTE" rev-parse main)" = "$(git -C "$EKCLONE" rev-parse HEAD)" ] || fail "split did not push"
grep -q '^- \[Big\](topics/big/README.md) — a topic past the bullet limit$' "$EKCLONE/README.md" || fail "root line not rewritten: $(cat "$EKCLONE/README.md")"
pass "split moves the bullets into topics/big/ in one pushed commit"

RC=0; OUT=$(ekn knowledge query --external Big) || RC=$?
case "$OUT" in *"- [Alpha](alpha.md) — the first thirteen"*"**Bullet 21**"*) ;; *) fail "parent read lacks the sub-index or a left-over bullet: $OUT" ;; esac
case "$OUT" in *"**Bullet 1**"*) fail "parent read printed a moved bullet: $OUT" ;; esac
RC=0; OUT=$(ekn knowledge query --external --all Big) || RC=$?
case "$OUT" in *"## Big: Alpha"*"**Bullet 13**"*"## Big: Beta"*"**Bullet 20**"*) ;; *) fail "--all lacks the subtopic sections: $OUT" ;; esac
RC=0; OUT=$(ekn knowledge query --external "Big: Beta") || RC=$?
case "$OUT" in *"**Bullet 14**"*) ;; *) fail "subtopic read lacks its bullet: $OUT" ;; esac
case "$OUT" in *"**Bullet 1**"*|*"**Bullet 21**"*) fail "subtopic read printed other files: $OUT" ;; esac
pass "query reads the parent, --all and one subtopic"

RC=0; OUT=$(ekn knowledge query --external --match "BULLET 14") || RC=$?
[ "$RC" = "0" ] && [ "$OUT" = "$(printf '## Big: Beta\n- **Bullet 14** — Filler. <!-- 2026-10-01 source:app -->')" ] || fail "--match should print only the one bullet under its subtopic: rc=$RC $OUT"
RC=0; OUT=$(ek knowledge query --external --match "no such words") || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.matches <<<"$OUT")" = "0" ] && [ -z "$(jget data.text <<<"$OUT")" ] || fail "--match without a hit should be exit 0, matches 0: rc=$RC $OUT"
RC=0; ekn knowledge query --external --match "" >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "--match '' should be exit 2, got $RC"
pass "query --match returns only matching bullets across subtopics"

# A flat file an older rota writes beside the folder is flagged, then folded back in.
printf '# Big\n\n- **Old write** — From an older rota. <!-- 2026-10-02 source:app -->\n' > "$EKCLONE/topics/big.md"
printf -- '- [Big](topics/big.md) — a topic past the bullet limit\n' >> "$EKCLONE/README.md"
git -C "$EKCLONE" add -A && git -C "$EKCLONE" commit -q -m "older rota add" && git -C "$EKCLONE" push -q origin main || fail "planting the stray failed"
ERR="$TMP_EK/stray.err"
RC=0; OUT=$(cd "$EKPROJ" && "$ROTA_BIN" knowledge query --external Big 2>"$ERR") || RC=$?
case "$OUT" in *"(stray: topics/big.md)"*"**Old write**"*) ;; *) fail "stray bullets not printed: $OUT" ;; esac
grep -q 'rota knowledge split --external --topic Big' "$ERR" || fail "no stray hint on stderr: $(cat "$ERR")"
RC=0; OUT=$(ek knowledge split --external --topic Big) || RC=$?
[ "$RC" = "0" ] || fail "fold exited $RC: $OUT"
[ "$(jget data.folded <<<"$OUT")" = "1" ] && [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "fold data: $OUT"
[ ! -e "$EKCLONE/topics/big.md" ] || fail "fold left the stray file"
[ "$(grep -c '\](topics/big' "$EKCLONE/README.md")" = "1" ] || fail "fold left a second root line: $(cat "$EKCLONE/README.md")"
grep -q '\*\*Old write\*\*' "$EKCLONE/topics/big/README.md" || fail "stray bullet not in the folder README"
[ "$(git -C "$EKREMOTE" rev-parse main)" = "$(git -C "$EKCLONE" rev-parse HEAD)" ] || fail "fold did not push"
pass "split folds a stray flat file back into the folder"

# hit --external logs per machine, outside the clone, and stats reports it (#2109).
BEFORE="$(git -C "$EKCLONE" rev-parse HEAD)"
RC=0; OUT=$(ek knowledge hit --external --topic "Big: Beta" --title "Bullet 14") || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.hits <<<"$OUT")" = "1" ] && [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "hit --external data: rc=$RC $OUT"
RC=0; OUT=$(ek knowledge hit --external --topic "Big: Beta" --title "Bullet 14") || RC=$?
[ "$(jget data.hits <<<"$OUT")" = "2" ] || fail "second hit should count 2: $OUT"
RC=0; ek knowledge hit --external --topic "Big: Beta" --title "No such bullet" >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "hit on an unknown bullet should be exit 3, got $RC"
[ "$(git -C "$EKCLONE" rev-parse HEAD)" = "$BEFORE" ] && [ -z "$(git -C "$EKCLONE" status --porcelain)" ] || fail "hit changed the clone"
RC=0; OUT=$(ek knowledge stats --external) || RC=$?
[ "$(jget 'data.tornHits' <<<"$OUT")" = "0" ] && case "$OUT" in *'"lastHit"'*) ;; *) false ;; esac || fail "stats lacks hits: $OUT"
RC=0; OUT=$(ekn knowledge stats --external) || RC=$?
case "$OUT" in *"Big: Beta: "*", 2 hits"*) ;; *) fail "stats text lacks the 2 hits: $OUT" ;; esac
pass "hit --external counts per machine without touching the clone"

rm -rf "$TMP_EK"
