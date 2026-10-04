echo "worker dispatch — the tmux host sees a booted Claude Code on the current UI"
# Covers #102. Claude Code 2.1.289 dropped the "? for shortcuts" hint and the
# box frame, so the tmux boot check timed out on a healthy session. The fake
# tmux serves pane text captured from the real UI; a folder-trust dialog must
# still read as not booted. The pattern list itself is unit-tested in
# internal/host (TestLooksBooted). Codex workers are herdr-only (smoke 93); the
# codex pattern is exercised through the tmux operator window, same check.

TMP_BD="$(mktemp -d)"
trap 'rm -rf "$TMP_BD"' EXIT
BF="$TMP_BD/fake"
mkdir -p "$BF/state" "$BF/bin" "$TMP_BD/repo/.rota"
cp "$TESTDIR/fakes/tmux" "$BF/bin/tmux"
chmod +x "$BF/bin/tmux"
(
  cd "$TMP_BD/repo"
  git init -q -b main .
  git config user.email t@t
  git config user.name t
  echo seed > seed.txt
  git add seed.txt
  git commit -q -m seed
) || fail "boot-detect fixture repo setup failed"
printf '{"work":{"dispatch":"tmux"}}\n' > "$TMP_BD/repo/.rota/config.json"
echo brief > "$TMP_BD/brief.md"

bd() { ( cd "$TMP_BD/repo" && env -u TMUX -u HERDR_ENV PATH="$BF/bin:$PATH" FAKE_TMUX="$BF/state" ROTA_HOST_KILL_WAIT=1 "$@" ); }
bd "$ROTA_BIN" worker pool init --slots 1 --base main >/dev/null 2>&1 || fail "pool init failed"

cp "$REPO/internal/host/testdata/claude-booted-2.1.289.txt" "$BF/state/pane"
RC=0; OUT="$(bd "$ROTA_BIN" --json worker dispatch w1 --body-file "$TMP_BD/brief.md" --task T1 --boot-timeout 2 2>&1)" || RC=$?
[ "$RC" = "0" ] || fail "a Claude Code 2.1.289 pane must read as booted (rc $RC): $OUT"
pass "tmux dispatch accepts the pane text of Claude Code 2.1.289"

rm -f "$BF/state/windows" "$BF/state/n"
cp "$REPO/internal/host/testdata/trust-dialog-2.1.288.txt" "$BF/state/pane"
RC=0; OUT="$(bd "$ROTA_BIN" --json worker dispatch w1 --body-file "$TMP_BD/brief.md" --task T2 --boot-timeout 2 2>&1)" || RC=$?
[ "$RC" != "0" ] || fail "a folder-trust dialog must not read as booted: $OUT"
case "$OUT" in *"did not come up"*) ;; *) fail "expected the boot-timeout message, got: $OUT" ;; esac
pass "tmux dispatch still times out on a pane stuck at the trust dialog"

trap 'rm -rf "$TMP"' EXIT
