echo "tracker suggest-upstream manual fallback when gh unavailable"
HI_TMP="$(mktemp -d)"
trap 'rm -rf "$HI_TMP"' EXIT
(
  cd "$HI_TMP"
  mkdir -p .rota stub-bin
  # Stub `gh` to a script that always fails so the verb takes the unavailable path,
  # even on a host where the real gh is installed and authed.
  cat > stub-bin/gh <<'EOS'
#!/bin/sh
exit 7
EOS
  chmod +x stub-bin/gh
  rc=0
  OUT=$(PATH="$HI_TMP/stub-bin:$PATH" hvj tracker suggest-upstream --title "test title" --body-file - --confirm --confirm-note "yes, file it" <<<"test body" 2>/dev/null) || rc=$?
  [ "$rc" = "5" ] || fail "expected exit 5 when gh fails: rc=$rc"
  [ "$(echo "$OUT" | jget ok)" = "false" ] || fail "expected ok:false envelope: $OUT"
  echo "$OUT" | jget error.hint | grep "github.com/l4ci/rota/issues/new" >/dev/null || fail "unavailable hint missing repo URL: $OUT"
  pass "tracker suggest-upstream exits 5 with the manual issue URL when gh unavailable"
)
rm -rf "$HI_TMP"

echo "tracker suggest-upstream --upstream-repo override"
HI2_TMP="$(mktemp -d)"
trap 'rm -rf "$HI2_TMP"' EXIT
(
  cd "$HI2_TMP"
  mkdir -p .rota stub-bin
  cat > stub-bin/gh <<'EOS'
#!/bin/sh
exit 7
EOS
  chmod +x stub-bin/gh
  rc=0; OUT=$(PATH="$HI2_TMP/stub-bin:$PATH" hvj tracker suggest-upstream --title "x" --upstream-repo "fork/repo" --body-file - --confirm --confirm-note "yes, file it" <<<"y" 2>/dev/null) || rc=$?
  [ "$rc" = 5 ] || fail "suggest-upstream with a failing gh should exit 5 (got $rc): $OUT"
  echo "$OUT" | jget error.hint | grep "github.com/fork/repo" >/dev/null || fail "--upstream-repo override ignored: $OUT"
  pass "tracker suggest-upstream --upstream-repo override flows through to the hint URL"
)
rm -rf "$HI2_TMP"

echo "tracker suggest-upstream creates the issue upstream"
HI3_TMP="$(mktemp -d)"
trap 'rm -rf "$HI3_TMP"' EXIT
(
  cd "$HI3_TMP"
  mkdir -p .rota
  echo '{"issues":{"provider":"github","retryWaitSeconds":0}}' > .rota/config.json
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$HI3_TMP/db.json" FAKE_TRACKER_LOG="$HI3_TMP/log"
  [ "$(command -v gh)" = "$TESTDIR/fakes/gh" ] || fail "fake gh not first on PATH"
  # The fake store is the forge's own state: no verb reads back the repo an issue landed in.
  DBQ() { python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
print(len(d["issues"]), d["issues"][-1]["title"], d["issues"][-1].get("repo", "-"), d["issues"][-1]["body"].strip())' "$HI3_TMP/db.json"; }

  # Default target: l4ci/rota (ROTA_UPSTREAM_REPO unset)
  OUT=$(env -u ROTA_UPSTREAM_REPO "$ROTA_BIN" --json tracker suggest-upstream --title "learned a thing" --body-file - --confirm --confirm-note "yes, file it" <<<"the body") || fail "suggest-upstream failed: $OUT"
  [ "$(echo "$OUT" | jget data.number)" = "1" ] || fail "suggest-upstream number: $OUT"
  [ "$(echo "$OUT" | jget data.upstreamRepo)" = "l4ci/rota" ] || fail "suggest-upstream default repo: $OUT"
  [ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "suggest-upstream changed: $OUT"
  [ "$(echo "$OUT" | jget data.url)" = "https://github.com/l4ci/rota/issues/1" ] || fail "suggest-upstream url: $OUT"
  [ "$(DBQ)" = "1 learned a thing l4ci/rota the body" ] || fail "issue not filed upstream as asked: $(DBQ)"

  # --upstream-repo wins over ROTA_UPSTREAM_REPO; the issue lands in that repo
  OUT=$(ROTA_UPSTREAM_REPO=env/repo "$ROTA_BIN" --json tracker suggest-upstream --title "second" --upstream-repo fork/repo --body-file - --confirm --confirm-note "yes, file it" <<<"b2") || fail "suggest-upstream --upstream-repo failed: $OUT"
  [ "$(echo "$OUT" | jget data.number)" = "2" ] || fail "second issue number: $OUT"
  [ "$(echo "$OUT" | jget data.upstreamRepo)" = "fork/repo" ] || fail "--upstream-repo not reported: $OUT"
  [ "$(echo "$OUT" | jget data.url)" = "https://github.com/fork/repo/issues/2" ] || fail "--upstream-repo url: $OUT"
  [ "$(DBQ)" = "2 second fork/repo b2" ] || fail "--upstream-repo issue landed elsewhere: $(DBQ)"

  # ROTA_UPSTREAM_REPO alone is the target when the flag is absent
  OUT=$(ROTA_UPSTREAM_REPO=env/repo "$ROTA_BIN" --json tracker suggest-upstream --title "third" --body-file - --confirm --confirm-note "yes, file it" <<<"b3") || fail "suggest-upstream env repo failed: $OUT"
  [ "$(echo "$OUT" | jget data.upstreamRepo)" = "env/repo" ] || fail "ROTA_UPSTREAM_REPO ignored: $OUT"
  [ "$(DBQ)" = "3 third env/repo b3" ] || fail "env-repo issue landed elsewhere: $(DBQ)"

  # missing --title is a usage error (2), nothing filed
  rc=0; "$ROTA_BIN" --json tracker suggest-upstream --body-file - <<<"x" >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || fail "suggest-upstream without --title should exit 2, got $rc"
  [ "$(DBQ | cut -d' ' -f1)" = "3" ] || fail "usage error still filed an issue"

  # without --confirm the public-filing gate refuses (4), nothing filed (B1)
  rc=0; "$ROTA_BIN" --json tracker suggest-upstream --title "nope" --body-file - <<<"x" >/dev/null 2>&1 || rc=$?
  [ "$rc" = 4 ] || fail "suggest-upstream without --confirm should exit 4, got $rc"
  [ "$(DBQ | cut -d' ' -f1)" = "3" ] || fail "a refused filing still filed an issue"
)
rm -rf "$HI3_TMP"
pass "tracker suggest-upstream files the issue in the default, env and --upstream-repo targets"

echo "release pending"
RP_TMP="$(mktemp -d)"
trap 'rm -rf "$RP_TMP"' EXIT

# Case 1: no tags -> no nudge, lastTag empty.
(
  cd "$RP_TMP"
  mkdir no-tag && cd no-tag
  mkdir -p .rota
  git init -q && git config user.email t@t && git config user.name t
  git commit -q --allow-empty -m "seed"
  OUT=$(hvj release pending) || vfail
  [ "$(echo "$OUT" | jget data.lastTag)" = "" ] || fail "no-tag case lastTag: $OUT"
  [ "$(echo "$OUT" | jget data.commits)" = "0" ] || fail "no-tag case commits: $OUT"
  [ "$(echo "$OUT" | jget data.shouldNudge)" = "false" ] || fail "no-tag case shouldNudge: $OUT"
  [ "$(echo "$OUT" | jget data.reason)" = "no-tag" ] || fail "no-tag case reason: $OUT"
  [ "$(echo "$OUT" | jget data.message)" = "" ] || fail "no-tag case message: $OUT"
)
pass "release pending: no tag -> no nudge"

# Case 2: tag + 3 commits, default thresholds -> no nudge.
(
  cd "$RP_TMP"
  mkdir below && cd below
  mkdir -p .rota
  git init -q && git config user.email t@t && git config user.name t
  git commit -q --allow-empty -m "seed"
  git tag v0.0.1
  for i in 1 2 3; do git commit -q --allow-empty -m "c$i"; done
  OUT=$(hvj release pending) || vfail
  [ "$(echo "$OUT" | jget data.lastTag)" = "v0.0.1" ] || fail "below-threshold lastTag: $OUT"
  [ "$(echo "$OUT" | jget data.commits)" = "3" ] || fail "below-threshold commits: $OUT"
  [ "$(echo "$OUT" | jget data.shouldNudge)" = "false" ] || fail "below-threshold shouldNudge: $OUT"
  [ "$(echo "$OUT" | jget data.reason)" = "" ] || fail "below-threshold reason: $OUT"
  [ "$(echo "$OUT" | jget data.message)" = "" ] || fail "below-threshold message: $OUT"
)
pass "release pending: 3 commits past tag -> no nudge"

# Case 3: tag + 11 commits -> nudge, reason=commits.
(
  cd "$RP_TMP"
  mkdir above && cd above
  mkdir -p .rota
  git init -q && git config user.email t@t && git config user.name t
  git commit -q --allow-empty -m "seed"
  git tag v0.0.1
  for i in $(seq 1 11); do git commit -q --allow-empty -m "c$i"; done
  OUT=$(hvj release pending) || vfail
  [ "$(echo "$OUT" | jget data.lastTag)" = "v0.0.1" ] || fail "above-threshold lastTag: $OUT"
  [ "$(echo "$OUT" | jget data.commits)" = "11" ] || fail "above-threshold commits: $OUT"
  [ "$(echo "$OUT" | jget data.shouldNudge)" = "true" ] || fail "above-threshold shouldNudge: $OUT"
  [ "$(echo "$OUT" | jget data.reason)" = "commits" ] || fail "above-threshold reason: $OUT"
  [ "$(echo "$OUT" | jget data.message)" = "11 commits since v0.0.1; consider /rota-release." ] || fail "above-threshold message: $OUT"
)
pass "release pending: 11 commits past tag -> nudge (reason=commits)"

# Case 4: custom commit threshold via .rota/config.json.
(
  cd "$RP_TMP"
  mkdir custom && cd custom
  git init -q && git config user.email t@t && git config user.name t
  git commit -q --allow-empty -m "seed"
  git tag v0.0.1
  for i in $(seq 1 6); do git commit -q --allow-empty -m "c$i"; done
  mkdir -p .rota
  echo '{"release":{"nudgeAfterCommits":5}}' > .rota/config.json
  OUT=$(hvj release pending) || vfail
  [ "$(echo "$OUT" | jget data.thresholdCommits)" = "5" ] || fail "custom-threshold thresholdCommits: $OUT"
  [ "$(echo "$OUT" | jget data.commits)" = "6" ] || fail "custom-threshold commits: $OUT"
  [ "$(echo "$OUT" | jget data.shouldNudge)" = "true" ] || fail "custom-threshold shouldNudge: $OUT"
  [ "$(echo "$OUT" | jget data.reason)" = "commits" ] || fail "custom-threshold reason: $OUT"
  [ "$(echo "$OUT" | jget data.message)" = "6 commits since v0.0.1; consider /rota-release." ] || fail "custom-threshold message: $OUT"
)
pass "release pending: custom nudgeAfterCommits=5 honored"

rm -rf "$RP_TMP"
trap 'rm -rf "$TMP"' EXIT


