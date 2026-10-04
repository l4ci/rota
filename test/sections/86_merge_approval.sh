echo "merge approval: --escalate asks on the PR thread, --approval merges on an allowlisted reply (C5, #61)"
# Everything runs against FAKES: gh is test/fakes/gh (state in a JSON store).
# Nothing here reaches a real forge or a real herdr.

TMP_MA="$(mktemp -d "$TMP/mergeapproval.XXXXXX")"
(
  P="$TMP_MA/gh"; mkdir -p "$P"
  git init -q --bare "$P/origin.git"
  git clone -q "$P/origin.git" "$P/work" 2>/dev/null; mkdir -p "$P/work/.rota"
  printf '{"backlog":{"backend":"issues"},"issues":{"provider":"github","retryWaitSeconds":0},"autonomy":{"level":"auto"},"ship":{"mergeApproval":"all"}}\n' > "$P/work/.rota/config.json"
  cd "$P/work"
  git config user.email t@t; git config user.name t
  git checkout -q -b main && git commit -q --allow-empty -m seed && git push -q origin main
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
  [ "$(command -v gh)" = "$TESTDIR/fakes/gh" ] || fail "gh must resolve to the fake, got $(command -v gh)"
  comments() { gh api --paginate "repos/o/r/issues/$1/comments"; }
  ncomments() { comments "$1" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))'; }

  git checkout -q -b feat/m; git commit -q --allow-empty -m m; git push -q origin feat/m
  PR="$(gh pr create --title "PR m" --body "no linked items" --base main --head feat/m | sed 's:.*/::')"
  git checkout -q main

  # The policy covers every merge: plain pr-merge refuses, flags are exclusive.
  RC=0; OUT="$(hvj ship pr-merge "$PR" 2>/dev/null)" || RC=$?
  [ "$RC" = "4" ] && [ "$(jget data.gate <<<"$OUT")" = "merge-approval" ] || fail "pr-merge under mergeApproval all should hit the gate: rc=$RC $OUT"
  RC=0; hvj ship pr-merge "$PR" --escalate --approval e1 >/dev/null 2>&1 || RC=$?
  [ "$RC" = "2" ] || fail "--escalate with --approval should exit 2, got $RC"

  # --escalate posts one approval request naming the accepted words; a re-run reuses it.
  RC=0; OUT="$(hvj ship pr-merge "$PR" --escalate 2>/dev/null)" || RC=$?
  [ "$RC" = "4" ] && [ "$(jget data.escalation.id <<<"$OUT")" = "e1" ] || fail "--escalate should refuse and name e1: rc=$RC $OUT"
  grep -q 'Reply `approve`, `approved`, `yes`, `lgtm` or `ship it` to merge.' <<<"$(comments "$PR")" \
    || fail "the request should name the accepted words: $(comments "$PR")"
  OUT="$(hvj ship pr-merge "$PR" --escalate 2>/dev/null)" || true
  [ "$(jget data.escalation.id <<<"$OUT")" = "e1" ] && [ "$(ncomments "$PR")" = "1" ] || fail "a pending request should be reused, not reposted: $OUT"
  pass "--escalate posts one approval request on the PR thread and reuses it"

  # No answer yet, then a reply outside the allowlist: both hold the merge.
  RC=0; OUT="$(hvj ship pr-merge "$PR" --approval e1 2>/dev/null)" || RC=$?
  [ "$RC" = "4" ] && [ "$(jget data.blockedBy <<<"$OUT")" = "approval pending" ] || fail "an unanswered request should hold: rc=$RC $OUT"
  gh api -X POST "repos/o/r/issues/$PR/comments" -f body="please wait" >/dev/null
  hvj round escalate check >/dev/null || fail "escalate check failed"
  RC=0; OUT="$(hvj ship pr-merge "$PR" --approval e1 2>/dev/null)" || RC=$?
  [ "$RC" = "4" ] && [ "$(jget data.blockedBy <<<"$OUT")" = "approval declined" ] && [ "$(jget data.answer <<<"$OUT")" = "please wait" ] \
    || fail "a reply outside the allowlist should hold the merge: rc=$RC $OUT"
  [ ! -e .rota/gate-audit.jsonl ] || fail "a held merge wrote an audit line"
  pass "--approval holds the merge until an allowlisted reply"

  # A fresh request answered "LGTM!" merges, and the audit quotes the reply.
  OUT="$(hvj ship pr-merge "$PR" --escalate 2>/dev/null)" || true
  [ "$(jget data.escalation.id <<<"$OUT")" = "e2" ] || fail "an answered request should not block a new one: $OUT"
  gh api -X POST "repos/o/r/issues/$PR/comments" -f body="LGTM!" >/dev/null
  hvj round escalate check >/dev/null || fail "escalate check failed"
  OUT="$(hvj ship pr-merge "$PR" --approval e2)" || fail "an approved request should merge: $OUT"
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "the approved merge should land: $OUT"
  python3 -c '
import json, sys
line = json.loads(open(".rota/gate-audit.jsonl").read().splitlines()[-1])
sys.exit(0 if line["gate"] == "merge-approval" and line["note"] == "LGTM!" and line["escalation"] == "e2" else 1)' \
    || fail "the audit line should quote the reply and name e2: $(cat .rota/gate-audit.jsonl)"
  pass "an allowlisted reply merges and is audited verbatim with its escalation"
) || fail "merge approval section failed"
