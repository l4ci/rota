echo "manual gates: gated verbs refuse without --confirm at every autonomy level (B1, #54)"

MGT="$(mktemp -d)"

OUT=$(cd "$MGT" && hvj gate list) || fail "gate list failed: $OUT"
python3 - "$OUT" <<'PY' || fail "gate list: unexpected registry: $OUT"
import json, sys
gates = json.loads(sys.argv[1])["data"]["gates"]
enforced = [g["name"] for g in gates if g["enforced"]]
assert enforced == ["tag-push", "release-publish", "public-filing", "merge-approval", "debug-reset"], enforced
assert {"issue-close", "decision-write", "pr-open"} <= {g["name"] for g in gates}
PY
pass "gate list prints the registry with the five enforced gates"

# A project whose origin is a local bare repo, with a release tag to push.
mg_project() { # mg_project <dir> <autonomy level> [<ship json>]
  mkdir -p "$1/.rota"
  printf '{"backlog":{"backend":"file"},"autonomy":{"level":"%s"}%s}\n' "$2" "${3:+,\"ship\":$3}" > "$1/.rota/config.json"
  ( cd "$1" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && printf '.rota/gate-audit.jsonl\n.rota/**/*.lock\n' > .gitignore && echo seed > seed.txt && git add seed.txt .gitignore .rota/config.json \
    && git commit -q -m seed && git init -q --bare ../"$(basename "$1")-origin.git" \
    && git remote add origin ../"$(basename "$1")-origin.git" && git tag -a v1.0.0 -m v1.0.0 )
}

for LEVEL in off auto loop; do
  P="$MGT/push-$LEVEL"
  mg_project "$P" "$LEVEL"
  RC=0; OUT=$(cd "$P" && hvj release push 1.0.0 2>/dev/null) || RC=$?
  [ "$RC" = 4 ] || fail "release push without --confirm under $LEVEL should exit 4, got $RC: $OUT"
  [ "$(echo "$OUT" | jget data.blockedBy)" = "manual gate" ] && [ "$(echo "$OUT" | jget data.gate)" = "tag-push" ] \
    || fail "release push refusal data under $LEVEL: $OUT"
  [ -z "$(git -C "$P" ls-remote --tags origin v1.0.0)" ] || fail "a refused push reached origin under $LEVEL"
  [ ! -e "$P/.rota/gate-audit.jsonl" ] || fail "a refused push wrote the audit log under $LEVEL"

  OUT=$(cd "$P" && hvj release push 1.0.0 --confirm --confirm-note "Yes, push v1.0.0") || fail "confirmed push under $LEVEL: $OUT"
  [ -n "$(git -C "$P" ls-remote --tags origin v1.0.0)" ] || fail "the confirmed push did not reach origin under $LEVEL"
  python3 - "$P/.rota/gate-audit.jsonl" "$LEVEL" <<'PY' || fail "audit line under $LEVEL: $(cat "$P/.rota/gate-audit.jsonl")"
import json, sys
lines = open(sys.argv[1]).read().splitlines()
assert len(lines) == 1, lines
a = json.loads(lines[0])
assert (a["gate"], a["verb"], a["target"], a["note"], a["autonomy"]) == ("tag-push", "release push", "v1.0.0", "Yes, push v1.0.0", sys.argv[2]), a
PY
  [ -z "$(git -C "$P" status --porcelain)" ] || fail "the audit log dirtied the tree under $LEVEL"
done
pass "release push refuses at off, auto and loop, and a confirmed push is audited"

RC=0; ( cd "$MGT/push-off" && hvj release push 1.0.0 --confirm >/dev/null 2>&1 ) || RC=$?
[ "$RC" = 2 ] || fail "--confirm without --confirm-note should exit 2, got $RC"
pass "--confirm needs --confirm-note"

# ship.mergeApproval paths: only a merge touching a listed path is gated.
P="$MGT/merge"
mg_project "$P" loop '{"mergeApproval":"paths","mergeApprovalPaths":["migrations"]}'
( cd "$P" && git checkout -q -b rota/code && echo c > code.txt && git add code.txt && git commit -q -m code \
  && git checkout -q main && git checkout -q -b rota/mig && mkdir migrations && echo m > migrations/001.sql \
  && git add migrations && git commit -q -m mig && git checkout -q main )
OUT=$(cd "$P" && echo "merge: code" | hvj ship merge rota/code --body-file -) || fail "an unlisted merge was gated: $OUT"
RC=0; OUT=$(cd "$P" && echo "merge: mig" | hvj ship merge rota/mig --body-file - 2>/dev/null) || RC=$?
[ "$RC" = 4 ] && [ "$(echo "$OUT" | jget 'data.paths[0]')" = "migrations/001.sql" ] \
  || fail "a merge touching migrations/ should exit 4 naming the file, got $RC: $OUT"
git -C "$P" rev-parse -q --verify refs/heads/rota/mig >/dev/null || fail "a refused merge deleted the branch"
OUT=$(cd "$P" && echo "merge: mig" | hvj ship merge rota/mig --body-file - --confirm --confirm-note "ok, merge it") \
  || fail "confirmed merge failed: $OUT"
[ "$(wc -l < "$P/.rota/gate-audit.jsonl")" -eq 1 ] || fail "merge audit: $(cat "$P/.rota/gate-audit.jsonl")"
pass "ship.mergeApproval paths gates only the merge that touches a listed path"

# rota init ignores the audit log.
( mkdir -p "$MGT/init" && cd "$MGT/init" && "$ROTA_BIN" init --no-blocks >/dev/null ) || fail "rota init failed"
grep -qx '.rota/gate-audit.jsonl' "$MGT/init/.gitignore" || fail "rota init does not ignore .rota/gate-audit.jsonl"
pass "rota init gitignores the gate audit log"

rm -rf "${MGT:?}"
