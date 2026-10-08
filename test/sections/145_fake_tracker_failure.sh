#!/usr/bin/env bash
# #646: argv is diagnostic data, not a forge error message. A short SHA
# containing 404 must not turn an injected HTTP 500 into a missing issue.
echo "Section 145: fake tracker failures with 404 in the close comment"
TMP_FFAIL="$(mktemp -d)"
trap 'rm -rf "$TMP_FFAIL"' EXIT

(
  cd "$TMP_FFAIL"
  git init -q --object-format=sha1
  mkdir .rota
  echo '{"issues":{"retryWaitSeconds":0}}' > .rota/config.json
  git mktree </dev/null >/dev/null
  # Fixed raw commit: e404b45..., independent of clock, load or git identity.
  sha=$(git hash-object -t commit -w --stdin <<'EOF'
tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904
author t <t@t> 1700000000 +0000
committer t <t@t> 1700000000 +0000

fixture 226
EOF
  )
  git update-ref HEAD "$sha"
  [ "$(git rev-parse --short HEAD)" = e404b45 ] || fail "fixture must have 404 in its short SHA"
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$TMP_FFAIL/db.json"
  export FAKE_TRACKER_LOG="$TMP_FFAIL/argv.log" FAKE_TRACKER_FAIL=issue
  failed=0
  for provider in github gitlab; do
    git remote remove origin 2>/dev/null || true
    git remote add origin "https://$provider.com/o/r.git"
    for status in 500 404; do
      if [ "$status" = 500 ]; then
        message="HTTP 500: server error"; expected=5
      else
        message="HTTP 404: Not Found"; expected=3
      fi
      : > "$FAKE_TRACKER_LOG"
      rc=0
      FAKE_TRACKER_FAIL_MSG="$message" "$ROTA_BIN" --json issues close 1 --commit HEAD \
        >"$TMP_FFAIL/out" 2>"$TMP_FFAIL/err" || rc=$?
      grep -q 'Closed by rota: shipped in e404b45' "$FAKE_TRACKER_LOG" \
        || fail "$provider close comment must reach the fake's argv log"
      if [ "$rc" != "$expected" ]; then
        printf '%s HTTP %s: expected exit %s, got %s\n' "$provider" "$status" "$expected" "$rc"
        cat "$TMP_FFAIL/out" "$TMP_FFAIL/err" "$FAKE_TRACKER_LOG"
        failed=1
      fi
    done
  done
  [ "$failed" = 0 ] || fail "fake argv must not change the simulated failure classification"
)

rm -rf "$TMP_FFAIL"
trap 'rm -rf "$TMP"' EXIT
pass "HTTP 500 stays exit 5 and HTTP 404 stays exit 3 with a 404-containing SHA on both forges"
