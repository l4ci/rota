echo "#2037: release doctor and poll ignore smoke fixture variables"
TMP_RFI="$(mktemp -d)"
trap 'rm -rf "$TMP_RFI"' EXIT

# The runner itself builds with rotatest. Build the shipped variant as well so
# moving a fixture back into the production dependency path cannot pass here.
(cd "$REPO" && go build -tags '' -o "$TMP_RFI/rota" ./cmd/rota)
mkdir -p "$TMP_RFI/proj/.rota" "$TMP_RFI/empty" "$TMP_RFI/bin"
printf '{"round":{"dispatch":"solo"}}\n' > "$TMP_RFI/proj/.rota/config.json"
git -C "$TMP_RFI/proj" init -q
printf '.worktrees/\n' > "$TMP_RFI/proj/.gitignore"
ln -s "$(command -v git)" "$TMP_RFI/bin/git"
printf '#!/bin/sh\nexit 0\n' > "$TMP_RFI/bin/jq"
chmod +x "$TMP_RFI/bin/jq"
printf 'ROTA-BLOCKED sam: fixture only\n' > "$TMP_RFI/pane.txt"

rc=0
OUT=$(cd "$TMP_RFI/proj" && ROTA_TEST_POLL_FIXTURE="$TMP_RFI/pane.txt" \
  ROTA_TEST_POLL_KIND=codex ROTA_TEST_POLL_STATUS=working \
  "$TMP_RFI/rota" --json worker poll sam 2>/dev/null) || rc=$?
[ "$rc" -ne 0 ] || fail "release poll classified the test fixture: $OUT"
case "$OUT" in *'fixture only'*) fail "release poll read the test fixture: $OUT" ;; esac
pass "release poll follows the real environment instead of a fixture"

rc=0
OUT=$(cd "$TMP_RFI/proj" && ROTA_TEST_DOCTOR_PATH="$TMP_RFI/empty" \
  ROTA_TEST_DOCTOR_DISK=1:123 PATH="$TMP_RFI/bin" "$TMP_RFI/rota" --json doctor 2>/dev/null) || rc=$?
printf '%s' "$OUT" | python3 -c '
import json,sys
checks={c["name"]:c for c in json.load(sys.stdin)["data"]["checks"]}
assert checks["git"]["status"] == "pass", checks["git"]
assert "has 1 B free" not in checks.get("disk", {}).get("detail", ""), checks.get("disk")
' || fail "release doctor used the fixture PATH or disk: $OUT"
pass "release doctor uses PATH and the real disk"

# Names alone must not enter the release artifact: all five reads belong only
# to the rotatest dependency wiring.
for key in ROTA_TEST_DOCTOR_PATH ROTA_TEST_DOCTOR_DISK ROTA_TEST_POLL_FIXTURE ROTA_TEST_POLL_KIND ROTA_TEST_POLL_STATUS; do
  if LC_ALL=C grep -aF "$key" "$TMP_RFI/rota" >/dev/null; then
    fail "release binary contains $key"
  fi
done
pass "release artifact contains no doctor or poll fixture hooks"
# The smoke binary reaches the classifier through the injected poll adapter;
# it needs no pane or registry and must retain the fixture-only defaults.
OUT=$(cd "$TMP_RFI/proj" && ROTA_TEST_POLL_FIXTURE="$TMP_RFI/pane.txt" \
  "$ROTA_BIN" --json worker poll --lines 0 2>/dev/null) || vfail
printf '%s' "$OUT" | python3 -c '
import json,sys
data=json.load(sys.stdin)["data"]
assert data["slots"] == [{"name":"fixture", "state":"blocked", "evidence":"fixture only"}], data
assert data["changed"] is False, data
' || fail "smoke poll adapter lost fixture defaults: $OUT"
[ ! -e "$TMP_RFI/proj/.rota/workers.json" ] || fail "fixture poll wrote a registry"
pass "smoke poll injects a static adapter without registry writes"

rm -rf "$TMP_RFI"
trap 'rm -rf "$TMP"' EXIT
