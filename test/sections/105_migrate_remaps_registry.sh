echo "migrate issues mid-round: slot and queued-PR IDs follow issue-map.json (#27)"

TMP_MR="$(mktemp -d)"
trap 'rm -rf "$TMP_MR"' EXIT

P="$TMP_MR/proj"
mkdir -p "$P/.rota"
printf '{"issues":{"provider":"github","retryWaitSeconds":0,"bulkPaceMs":0}}\n' > "$P/.rota/config.json"
cat > "$P/.rota/BACKLOG.md" <<'BL'
# TODO

## Bugs

- **[B1] [P1] Crash on save.** Saving a large file crashes.
- **[B2] [P2] Typo in help.** Fix the help text.

## Tasks

- **[T1] Clean up scripts.** Remove dead scripts.
BL
# A round is running: ben holds B2, a PR for T1 waits in review.
printf '{"slots":[{"name":"ben","task":"B2","claimId":"ben@1","state":"busy"},{"name":"dana","task":null,"state":"idle"}],"prs":[{"issue":"T1","pr":"#9"}]}\n' > "$P/.rota/workers.json"
(
  cd "$P"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
  "$ROTA_BIN" migrate issues --apply >/dev/null 2>"$P/err" || fail "migrate apply failed: $(cat "$P/err")"
  WJ() { python3 -c 'import json,sys;print(eval(sys.argv[1], {"w": json.load(open(".rota/workers.json"))}))' "$1"; }
  MAPN() { python3 -c 'import json,sys;print(json.load(open(".rota/issue-map.json"))[sys.argv[1]]["number"])' "$1"; }
  [ "$(WJ 'w["slots"][0]["task"]')" = "$(MAPN B2)" ] || fail "slot task not remapped: $(WJ 'w["slots"][0]')"
  [ "$(WJ 'w["prs"][0]["issue"]')" = "$(MAPN T1)" ] || fail "queued PR issue not remapped: $(WJ 'w["prs"][0]')"
  [ "$(WJ 'w["slots"][0]["claimId"]')" = "ben@1" ] || fail "claimId must stay: $(WJ 'w["slots"][0]')"
  [ "$(WJ 'w["slots"][1]["task"]')" = "None" ] || fail "idle slot touched: $(WJ 'w["slots"][1]')"
)

trap 'rm -rf "$TMP"' EXIT
pass "migrate issues mid-round: slot task and queued-PR issue remapped, claimId kept"
