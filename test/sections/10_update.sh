echo "update"
# The binary under test is stamped with VERSION (see runner.sh), so current is
# known; ROTA_TEST_LATEST_VERSION skips the network. A binary in $TMP is a
# script-style install.
CUR="$(tr -d '[:space:]' < "$REPO/VERSION")"
OUT=$(env ROTA_TEST_LATEST_VERSION=99.0.0 "$ROTA_BIN" --json update) || fail "update exited non-zero"
[ "$(echo "$OUT" | jget data.currentVersion)" = "$CUR" ] || fail "update didn't report the binary's version ($CUR): $OUT"
[ "$(echo "$OUT" | jget data.latestVersion)" = "99.0.0" ] || fail "update didn't use override latest: $OUT"
[ "$(echo "$OUT" | jget data.status)" = "behind" ] || fail "update didn't mark behind: $OUT"
[ "$(echo "$OUT" | jget data.installType)" = "script" ] || fail "update didn't report a script install: $OUT"
case $(echo "$OUT" | jget data.updateCommand) in *install.sh*"rota skills update") ;; *) fail "update command should rerun install.sh then refresh skills: $OUT" ;; esac
pass "update reports behind and names the install.sh command"

OUT=$(env ROTA_TEST_LATEST_VERSION="$CUR" "$ROTA_BIN" --json update) || fail "update exited non-zero"
[ "$(echo "$OUT" | jget data.status)" = "current" ] || fail "update didn't mark current: $OUT"
pass "update reports current when equal"

OUT=$(env ROTA_TEST_LATEST_VERSION=0.0.1 "$ROTA_BIN" --json update) || fail "update exited non-zero"
[ "$(echo "$OUT" | jget data.status)" = "ahead" ] || fail "update didn't mark ahead: $OUT"
pass "update reports ahead when current > latest"

# A binary under a Homebrew prefix is a brew install.
mkdir -p "$TMP/Cellar/rota/9.9.9/bin"
cp "$ROTA_BIN" "$TMP/Cellar/rota/9.9.9/bin/rota"
OUT=$(env ROTA_TEST_LATEST_VERSION=99.0.0 "$TMP/Cellar/rota/9.9.9/bin/rota" --json update) || fail "update exited non-zero"
[ "$(echo "$OUT" | jget data.installType)" = "brew" ] || fail "update didn't report a brew install: $OUT"
case $(echo "$OUT" | jget data.updateCommand) in "brew update && brew upgrade rota"*) ;; *) fail "brew install should name brew upgrade: $OUT" ;; esac
rm -rf "$TMP/Cellar"
pass "update reports a Homebrew binary as brew"

echo "version"
# Plain `version` needs no project: it prints the version stamped into the binary.
EXPECTED="$CUR"
OUT=$(cd / && "$ROTA_BIN" --json version) || fail "version exited non-zero outside a project"
[ "$(echo "$OUT" | jget data.version)" = "$EXPECTED" ] || fail "version != VERSION ($EXPECTED): $OUT"
pass "version prints the binary version without a project"

echo "version --drift"
XX_TMP="$(mktemp -d)"
trap 'rm -rf "$XX_TMP"' EXIT
(
  cd "$XX_TMP"
  mkdir -p .rota

  # Test 1: no .rota/config.json → nothing stamped, so the status is unknown.
  rc=0; OUT=$("$ROTA_BIN" --json version --drift) || rc=$?
  [ "$rc" = 0 ] || fail "version --drift exited $rc with no config: $OUT"
  [ "$(echo "$OUT" | jget data.status)" = "unknown" ] || fail "no config: expected status unknown: $OUT"
  [ "$(echo "$OUT" | jget data.drift)" = "false" ] || fail "no config: expected drift false: $OUT"
  pass "version --drift reports unknown when .rota/config.json is missing"

  # Test 2: drift between stamped 1.0.0 and the running binary ($EXPECTED, the
  # version the section above read back; `installed` is the binary's, not a
  # plugin cache's, in 5.0).
  cat > .rota/config.json <<'EOF2'
{"hv":{"version":"1.0.0"}}
EOF2
  OUT=$("$ROTA_BIN" --json version --drift)
  [ "$(echo "$OUT" | jget data.stamped)" = "1.0.0" ] || fail "drift: wrong stamped: $OUT"
  [ "$(echo "$OUT" | jget data.installed)" = "$EXPECTED" ] || fail "drift: wrong installed: $OUT"
  [ "$(echo "$OUT" | jget data.version)" = "$EXPECTED" ] || fail "drift: version != installed: $OUT"
  [ "$(echo "$OUT" | jget data.status)" = "drift" ] || fail "drift: expected status drift: $OUT"
  [ "$(echo "$OUT" | jget data.drift)" = "true" ] || fail "drift: expected drift true: $OUT"
  pass "version --drift reports drift when stamped != installed"

  # (Test 2 stamps the pre-rename hv.version key: the legacy fallback.)

  # Test 3: match when the stamp is the binary's own version.
  printf '{"rota":{"version":"%s"}}\n' "$EXPECTED" > .rota/config.json
  OUT=$("$ROTA_BIN" --json version --drift)
  [ "$(echo "$OUT" | jget data.status)" = "match" ] || fail "match: expected status match: $OUT"
  [ "$(echo "$OUT" | jget data.drift)" = "false" ] || fail "match: expected drift false: $OUT"
  pass "version --drift reports match when stamped == installed"

  # Test 4: the envelope always carries the full key set.
  for k in version stamped installed status drift; do
    echo "$OUT" | jget "data.$k" >/dev/null || fail "--drift: missing $k: $OUT"
  done
  pass "version --drift carries version/stamped/installed/status/drift"
)
trap 'rm -rf "$TMP"' EXIT
rm -rf "$XX_TMP"

# Without a project root, --drift has nothing to compare: exit 3 (resolution).
XX_TMP="$(mktemp -d)"
trap 'rm -rf "$XX_TMP"' EXIT
rc=0; (cd "$XX_TMP" && "$ROTA_BIN" --json version --drift >/dev/null 2>&1) || rc=$?
[ "$rc" = 3 ] || fail "version --drift outside a project should exit 3, got $rc"
trap 'rm -rf "$TMP"' EXIT
rm -rf "$XX_TMP"
pass "version --drift exits 3 outside a project"
