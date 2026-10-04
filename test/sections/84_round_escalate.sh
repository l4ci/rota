echo "round escalate — ask the human on a thread, notify, find the answer (#60)"
# send posts a marked comment on an issue thread, raises a herdr notification and
# records the entry in .rota/workers.json; check finds the first later comment
# without a rota marker. Everything runs against FAKES: gh is test/fakes/gh (state in
# a JSON store), herdr is test/fakes/herdr (logs its argv). Nothing here reaches a
# real forge or a real herdr.

TMP_ES="$(mktemp -d)"
trap 'rm -rf "${TMP_ES:?}"' EXIT
FK="$TMP_ES/fake"
mkdir -p "$FK/bin" "$FK/nobin" "$FK/herdr"

# PATH-first wrappers, so `gh` and `herdr` can only be the fakes.
printf '#!/usr/bin/env bash\nexec bash "%s/fakes/gh" "$@"\n' "$TESTDIR" > "$FK/bin/gh"
printf '#!/usr/bin/env bash\nexec bash "%s/fakes/herdr" "$@"\n' "$TESTDIR" > "$FK/bin/herdr"
chmod +x "$FK/bin/gh" "$FK/bin/herdr"
# A PATH without any herdr: only gh (the fake) and what the fakes themselves need.
cp "$FK/bin/gh" "$FK/nobin/gh"
for tool in bash sh env git python3 dirname cat; do
  real="$(command -v "$tool" || true)"
  [ -n "$real" ] && ln -s "$real" "$FK/nobin/$tool"
done

PROJ="$TMP_ES/proj"
mkdir -p "$PROJ/.rota"
git -C "$PROJ" init -q -b main
echo '{"issues":{"provider":"github","retryWaitSeconds":0}}' > "$PROJ/.rota/config.json"
printf 'Which of the two options do you want?\n' > "$TMP_ES/body.md"

# es <env-prefix-free command...>: run in the project with the fakes first on PATH.
es() { ( cd "$PROJ" && PATH="$FK/bin:$PATH" FAKE_TRACKER_DB="$TMP_ES/gh.json" FAKE_HERDR="$FK/herdr" "$@" ); }
# es_noherdr: PATH holds no herdr at all, even if this machine has one.
es_noherdr() { ( cd "$PROJ" && PATH="$FK/nobin" FAKE_TRACKER_DB="$TMP_ES/gh.json" FAKE_HERDR="$FK/herdr" "$@" ); }
reg() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(json.dumps(eval(sys.argv[2])))' "$PROJ/.rota/workers.json" "$1"; }

# Safety: the gh the section is about to use must be the fake.
[ "$(es command -v gh)" = "$FK/bin/gh" ] || fail "gh must resolve to the fake under $FK/bin, got $(es command -v gh)"
[ "$(es command -v herdr)" = "$FK/bin/herdr" ] || fail "herdr must resolve to the fake"

es gh issue create --title "First" --body "one" >/dev/null
es gh issue create --title "Second" --body "two" >/dev/null

# ── send: comment, notification, record ─────────────────────────────────────
RC=0; OUT="$(es env HERDR_ENV=1 "$ROTA_BIN" --json round escalate send 1 --title "Pick an option" --body-file "$TMP_ES/body.md" 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] || fail "send: exit 0 expected, got $RC: $OUT"
[ "$(jget data.escalation.id <<<"$OUT")" = "e1" ] || fail "send: first id must be e1: $OUT"
[ "$(jget data.escalation.kind <<<"$OUT")" = "issue" ] || fail "send: kind issue: $OUT"
[ "$(jget data.escalation.status <<<"$OUT")" = "pending" ] || fail "send: status pending: $OUT"
[ "$(jget data.notified <<<"$OUT")" = "true" ] || fail "send: notified must be true inside herdr: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "send: changed must be true: $OUT"
[ "$(jget data.url <<<"$OUT")" = "https://github.com/fake/repo/issues/1#issuecomment-1" ] || fail "send: url from the created comment: $OUT"
[ "$(jget data.escalation.deadline <<<"$OUT" 2>/dev/null || true)" = "" ] || fail "send: no --timeout means no deadline: $OUT"
COMMENTS="$(es gh api --paginate repos/o/r/issues/1/comments)"
python3 -c '
import json, sys
c = json.load(sys.stdin)
b = c[0]["body"]
ok = len(c) == 1 and b.startswith("**rota escalation e1**: Pick an option\n") and "Which of the two options" in b \
    and "Answer in a new comment on this thread." in b and "m:" not in b and b.rstrip("\n").endswith("<!-- rota:escalation e1 -->")
sys.exit(0 if ok else 1)' <<<"$COMMENTS" || fail "send: the comment must carry the title, body, the answer ask (no m:) and end with the marker: $COMMENTS"
case "$(cat "$FK/herdr/log")" in *"notification show rota escalation e1 --body Pick an option (#1)"*) ;; *) fail "send: herdr notification missing: $(cat "$FK/herdr/log")" ;; esac
[ "$(reg 'd["escalations"][0]["status"]')" = '"pending"' ] || fail "send: registry must hold the pending entry: $(cat "$PROJ/.rota/workers.json")"
[ "$(reg 'd["escalations"][0]["commentId"]')" = '"1"' ] || fail "send: registry must keep the comment id"
pass "send posts the marked comment, notifies through herdr and records e1"

# ── one per thread ──────────────────────────────────────────────────────────
RC=0; OUT="$(es "$ROTA_BIN" --json round escalate send 1 --title "Again" --body-file "$TMP_ES/body.md" 2>/dev/null)" || RC=$?
[ "$RC" = "4" ] || fail "second send on the same thread must exit 4, got $RC: $OUT"
[ "$(jget data.pending <<<"$OUT")" = "e1" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "second send: failure data names the pending id: $OUT"
[ "$(python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' <<<"$(es gh api --paginate repos/o/r/issues/1/comments)")" = "1" ] || fail "a refused send must not post"
pass "a second send on a thread with a pending escalation exits 4 and posts nothing"

# ── check: nothing, then rota comments, then the answer ─────────────────────────────
RC=0; OUT="$(es "$ROTA_BIN" --json round escalate check 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] || fail "check must exit 0 whatever the status, got $RC"
[ "$(jget data.pending <<<"$OUT")" = "1" ] && [ "$(jget data.answered <<<"$OUT")" = "0" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "check before any answer: $OUT"
es gh api -X POST repos/o/r/issues/1/comments -f body="$(printf '<!-- rota:claim dana -->\nClaimed by dana')" >/dev/null
es gh api -X POST repos/o/r/issues/1/comments -f body="$(printf 'sounds right\n<!-- rota:escalation e9 -->')" >/dev/null
OUT="$(es "$ROTA_BIN" --json round escalate check 2>/dev/null)"
[ "$(jget data.pending <<<"$OUT")" = "1" ] || fail "rota-marked comments must not answer: $OUT"
es gh api -X POST repos/o/r/issues/1/comments -f body="$(printf 'take option two')" >/dev/null
RC=0; OUT="$(es "$ROTA_BIN" --json round escalate check 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.answered <<<"$OUT")" = "1" ] && [ "$(jget data.pending <<<"$OUT")" = "0" ] || fail "check with an answer: rc=$RC $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "storing the answer is a change: $OUT"
[ "$(jget 'data.escalations[0].status' <<<"$OUT")" = "answered" ] || fail "status answered: $OUT"
[ "$(jget 'data.escalations[0].answer.commentId' <<<"$OUT")" = "4" ] || fail "the answer is the first unmarked comment after the question (comment 4): $OUT"
case "$(jget 'data.escalations[0].answer.body' <<<"$OUT")" in "take option two") ;; *) fail "answer body: $OUT" ;; esac
[ "$(reg 'd["escalations"][0]["status"]')" = '"answered"' ] || fail "the answer must be stored: $(cat "$PROJ/.rota/workers.json")"
OUT="$(es "$ROTA_BIN" --json round escalate check e1 2>/dev/null)"
[ "$(jget data.changed <<<"$OUT")" = "false" ] && [ "$(jget 'data.escalations[0].status' <<<"$OUT")" = "answered" ] || fail "a named answered entry reads from the registry: $OUT"
RC=0; es "$ROTA_BIN" --json round escalate check e7 >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "an unknown id must exit 3, got $RC"
pass "check ignores rota-marked comments, stores the first unmarked one as the answer, and exits 3 for an unknown id"

# ── no herdr: comment only, notified false ──────────────────────────────────
RC=0; OUT="$(es_noherdr env HERDR_ENV=1 "$ROTA_BIN" --json round escalate send 2 --title "Second question" --body-file "$TMP_ES/body.md" --timeout 3600 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] || fail "send without a herdr binary must still succeed, got $RC: $OUT"
[ "$(jget data.notified <<<"$OUT")" = "false" ] && [ "$(jget data.escalation.notified <<<"$OUT")" = "false" ] || fail "no herdr: notified must be false: $OUT"
case "$(jget 'warnings[0]' <<<"$OUT")" in *"no notification"*) ;; *) fail "no herdr: a warning must say why: $OUT" ;; esac
[ "$(jget data.escalation.id <<<"$OUT")" = "e2" ] || fail "second escalation is e2: $OUT"
python3 -c '
import json, sys
from datetime import datetime
e = json.load(sys.stdin)["data"]["escalation"]
s = datetime.strptime(e["sentAt"], "%Y-%m-%dT%H:%M:%SZ"); d = datetime.strptime(e["deadline"], "%Y-%m-%dT%H:%M:%SZ")
sys.exit(0 if (d - s).total_seconds() == 3600 else 1)' <<<"$OUT" || fail "--timeout 3600 must set deadline = sentAt + 3600s: $OUT"
pass "without a herdr binary send posts the comment, reports notified false and warns"

# ── timed-out is derived, never stored ──────────────────────────────────────
python3 - "$PROJ/.rota/workers.json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
for e in d["escalations"]:
    if e["id"] == "e2":
        e["deadline"] = "2000-01-01T00:00:00Z"
json.dump(d, open(p, "w"), indent=1)
PY
RC=0; OUT="$(es "$ROTA_BIN" --json round escalate check 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] || fail "a timed-out escalation still exits 0, got $RC: $OUT"
[ "$(jget 'data.escalations[0].status' <<<"$OUT")" = "timed-out" ] && [ "$(jget data.timedOut <<<"$OUT")" = "1" ] && [ "$(jget data.pending <<<"$OUT")" = "0" ] || fail "past its deadline e2 must read timed-out: $OUT"
[ "$(reg '[e["status"] for e in d["escalations"]]')" = '["answered", "pending"]' ] || fail "timed-out must not be stored: $(cat "$PROJ/.rota/workers.json")"
# round status and reconcile list it from the registry (no tmux or herdr on PATH: host unavailable).
RC=0; OUT="$(es_noherdr "$ROTA_BIN" --json round status 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] && [ "$(jget 'data.escalations[0].id' <<<"$OUT")" = "e2" ] && [ "$(jget 'data.escalations[0].status' <<<"$OUT")" = "timed-out" ] || fail "round status must list the open escalation: rc=$RC $OUT"
RC=0; OUT="$(es_noherdr "$ROTA_BIN" --json round reconcile 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] && [ "$(python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]["escalations"]))' <<<"$OUT")" = "1" ] || fail "round reconcile must list the open escalation: rc=$RC $OUT"
es gh api -X POST repos/o/r/issues/2/comments -f body="late but fine" >/dev/null
OUT="$(es "$ROTA_BIN" --json round escalate check 2>/dev/null)"
[ "$(jget 'data.escalations[0].status' <<<"$OUT")" = "answered" ] || fail "a late answer must still land: $OUT"
pass "timed-out is derived on read, round status and reconcile list it, and a late answer still lands"

trap 'rm -rf "$TMP"' EXIT
rm -rf "${TMP_ES:?}"
