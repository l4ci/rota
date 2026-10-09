echo "release version"
# Case 1: .claude-plugin/plugin.json (priority candidate)
DV1="$(mktemp -d)"
TMP_DV="$DV1"
trap 'rm -rf "$TMP_DV"' EXIT
(
  cd "$DV1"
  mkdir -p .rota .claude-plugin
  printf '{"version":"1.0.0"}\n' > .claude-plugin/plugin.json
  OUT=$(hvj release version) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.version)" = "1.0.0" ] || { echo "FAIL: version wrong: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.kind)" = "plugin-json" ] || { echo "FAIL: kind wrong: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.file)" = ".claude-plugin/plugin.json" ] || { echo "FAIL: file wrong: $OUT"; exit 1; }
  # --level and --to compute next read-only
  OUT=$(hvj release version --level minor) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.next)" = "1.1.0" ] || { echo "FAIL: next for --level minor wrong: $OUT"; exit 1; }
  OUT=$(hvj release version --to 3.0.0) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.next)" = "3.0.0" ] || { echo "FAIL: next for --to wrong: $OUT"; exit 1; }
  grep -q '"1.0.0"' .claude-plugin/plugin.json || { echo "FAIL: version --level wrote the file"; exit 1; }
  rc=0; hvj release version --to 1.0.0 >/dev/null 2>&1 || rc=$?
  [ "$rc" = "1" ] || { echo "FAIL: --to not greater expected exit 1, got $rc"; exit 1; }
  rc=0; hvj release version --level huge >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: bad --level expected exit 2, got $rc"; exit 1; }
)
pass "release version detects plugin.json and computes next read-only"

# Case 2: config override via .rota/config.json
(
  cd "$DV1"
  printf '{"release":{"versionFile":"package.json"}}\n' > .rota/config.json
  printf '{"version":"2.5.0"}\n' > package.json
  OUT=$(hvj release version) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.file)" = "package.json" ] || { echo "FAIL: file wrong: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.version)" = "2.5.0" ] || { echo "FAIL: version wrong: $OUT"; exit 1; }
)
pass "release version respects release.versionFile config override"
rm -rf "$DV1"

# Case 3: pyproject.toml
DV2="$(mktemp -d)"
TMP_DV="$DV2"
(
  cd "$DV2"
  mkdir -p .rota
  printf '[project]\nversion = "0.1.2"\n' > pyproject.toml
  OUT=$(hvj release version) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.kind)" = "pyproject" ] || { echo "FAIL: kind wrong: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.version)" = "0.1.2" ] || { echo "FAIL: version wrong: $OUT"; exit 1; }
)
rm -rf "$DV2"
pass "release version detects pyproject.toml"

# Case 4: Cargo.toml
DV3="$(mktemp -d)"
TMP_DV="$DV3"
(
  cd "$DV3"
  mkdir -p .rota
  printf '[package]\nversion = "3.4.5"\n' > Cargo.toml
  OUT=$(hvj release version) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.kind)" = "cargo" ] || { echo "FAIL: kind wrong: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.version)" = "3.4.5" ] || { echo "FAIL: version wrong: $OUT"; exit 1; }
)
rm -rf "$DV3"
pass "release version detects Cargo.toml"

# Case 5: plain VERSION file
DV4="$(mktemp -d)"
TMP_DV="$DV4"
(
  cd "$DV4"
  mkdir -p .rota
  printf '9.9.9\n' > VERSION
  OUT=$(hvj release version) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.kind)" = "plain" ] || { echo "FAIL: kind wrong: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.version)" = "9.9.9" ] || { echo "FAIL: version wrong: $OUT"; exit 1; }
)
rm -rf "$DV4"
pass "release version detects plain VERSION file"

# Case 6: no version files -> resolution failure (exit 3)
DV5="$(mktemp -d)"
TMP_DV="$DV5"
(
  cd "$DV5"
  mkdir -p .rota
  rc=0
  OUT=$(hvj release version 2>/dev/null) || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: expected exit 3, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget ok)" = "false" ] || { echo "FAIL: expected ok:false envelope: $OUT"; exit 1; }
  echo "$OUT" | jget error.message | grep "no version file detected" >/dev/null || { echo "FAIL: error message missing: $OUT"; exit 1; }
)
rm -rf "$DV5"
pass "release version exits 3 with no version files"

echo "release bump"
# Case 1: package.json patch bump
BV1="$(mktemp -d)"
TMP_DV="$BV1"
(
  cd "$BV1"
  mkdir -p .rota
  printf '{"version":"1.0.0"}\n' > package.json
  OUT=$(hvj release bump --level patch) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.to)" = "1.0.1" ] || { echo "FAIL: expected 1.0.1: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.from)" = "1.0.0" ] || { echo "FAIL: from wrong: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.kind)" = "package-json" ] || { echo "FAIL: kind wrong: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "true" ] || { echo "FAIL: changed not true: $OUT"; exit 1; }
  grep -q '"version": "1.0.1"' package.json || { echo "FAIL: file not updated"; exit 1; }
)
pass "release bump bumps package.json patch"

# Case 2: minor and major bumps
(
  cd "$BV1"
  NEW=$(hvj release bump --level minor | jget data.to) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$NEW" = "1.1.0" ] || { echo "FAIL: expected 1.1.0, got $NEW"; exit 1; }
  NEW=$(hvj release bump --level major | jget data.to) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$NEW" = "2.0.0" ] || { echo "FAIL: expected 2.0.0, got $NEW"; exit 1; }
)
pass "release bump bumps minor and major"

# Case 3: explicit version bump; a lower version is refused (exit 4)
(
  cd "$BV1"
  NEW=$(hvj release bump --to 5.0.0 | jget data.to) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$NEW" = "5.0.0" ] || { echo "FAIL: expected 5.0.0, got $NEW"; exit 1; }
  rc=0
  OUT=$(hvj release bump --to 1.0.0 2>/dev/null) || rc=$?
  [ "$rc" = "4" ] || { echo "FAIL: expected exit 4 for lower version, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] || { echo "FAIL: refusal data wrong: $OUT"; exit 1; }
  grep -q '"version": "5.0.0"' package.json || { echo "FAIL: refused bump touched the file"; exit 1; }
  rc=0; hvj release bump >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: bump without --level/--to expected exit 2, got $rc"; exit 1; }
  rc=0; hvj release bump --level patch --to 9.9.9 >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: bump with both flags expected exit 2, got $rc"; exit 1; }
)
rm -rf "$BV1"
pass "release bump explicit version: allows higher, refuses lower"

# Case 4: pyproject.toml, only bumps [project] version, not [tool.foo]
BV2="$(mktemp -d)"
TMP_DV="$BV2"
(
  cd "$BV2"
  mkdir -p .rota
  printf '[project]\nversion = "0.1.0"\nname = "x"\n\n[tool.foo]\nversion = "9.9.9"\n' > pyproject.toml
  NEW=$(hvj release bump --level patch --file pyproject.toml | jget data.to) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$NEW" = "0.1.1" ] || { echo "FAIL: expected 0.1.1, got $NEW"; exit 1; }
  grep -q 'version = "0.1.1"' pyproject.toml || { echo "FAIL: [project] version not updated"; exit 1; }
  grep -q 'version = "9.9.9"' pyproject.toml || { echo "FAIL: [tool.foo] version was modified"; exit 1; }
)
rm -rf "$BV2"
pass "release bump bumps only [project] in pyproject.toml"

# Case 5: Cargo.toml major bump
BV3="$(mktemp -d)"
TMP_DV="$BV3"
(
  cd "$BV3"
  mkdir -p .rota
  printf '[package]\nversion = "1.2.3"\n' > Cargo.toml
  OUT=$(hvj release bump --level major --file Cargo.toml) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.to)" = "2.0.0" ] || { echo "FAIL: expected 2.0.0: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.kind)" = "cargo" ] || { echo "FAIL: kind wrong: $OUT"; exit 1; }
)
rm -rf "$BV3"
pass "release bump bumps Cargo.toml major"

# Case 6: plain VERSION file minor bump
BV4="$(mktemp -d)"
TMP_DV="$BV4"
(
  cd "$BV4"
  mkdir -p .rota
  printf '0.0.1\n' > VERSION
  NEW=$(hvj release bump --level minor --file VERSION | jget data.to) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$NEW" = "0.1.0" ] || { echo "FAIL: expected 0.1.0, got $NEW"; exit 1; }
  CONTENT=$(cat VERSION)
  [ "$CONTENT" = "0.1.0" ] || { echo "FAIL: plain file content wrong: '$CONTENT'"; exit 1; }
)
rm -rf "$BV4"
pass "release bump bumps plain VERSION file"

echo "release notes --from commits"
CL1="$(mktemp -d)"
TMP_DV="$CL1"
(
  cd "$CL1"
  mkdir -p .rota
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  # Seed an initial empty commit so the --since anchor works reliably
  git commit --allow-empty -q -m "chore: init"
  git tag v_cl1_base
  git commit --allow-empty -q -m "feat: new widget"
  git commit --allow-empty -q -m "fix: null pointer"
  git commit --allow-empty -q -m "refactor: clean up internals"
  git commit --allow-empty -q -m "chore: bump deps"
  git commit --allow-empty -q -m "docs: update readme"
  git commit --allow-empty -q -m "test: add unit test"
  git commit --allow-empty -q -m "perf: speed up parser"
  git commit --allow-empty -q -m "plain commit no prefix"

  OUT=$(hvj release notes --from commits --since v_cl1_base) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  MD=$(echo "$OUT" | jget data.markdown)
  [ "$(echo "$OUT" | jget data.empty)" = "false" ] || { echo "FAIL: empty should be false: $OUT"; exit 1; }
  for h in New Fixed Performance Changed Documentation Other Stats; do
    grep -q "^### $h" <<<"$MD" || { echo "FAIL: ### $h missing"; exit 1; }
  done
  if grep -q "^## " <<<"$MD"; then echo "FAIL: headings must be normalised to ###"; exit 1; fi
  if grep -qx "### Test" <<<"$MD"; then echo "FAIL: ### Test heading should be absent"; exit 1; fi
)
pass "release notes --from commits emits expected sections, skips test commits"

# Breaking change detection
(
  cd "$CL1"
  git commit --allow-empty -q -m "feat: breaking change" -m "BREAKING CHANGE: api removed"
  MD=$(hvj release notes --from commits --since v_cl1_base | jget data.markdown) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  grep -q "^### Breaking" <<<"$MD" || { echo "FAIL: ### Breaking missing"; exit 1; }
)
pass "release notes --from commits emits ### Breaking for BREAKING CHANGE body"

# Empty range -> exit 0, empty markdown
(
  cd "$CL1"
  git tag v_cl1_tip
  rc=0
  OUT=$(hvj release notes --from commits --since v_cl1_tip 2>/dev/null) || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL: expected exit 0 on empty range, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget data.empty)" = "true" ] || { echo "FAIL: expected empty true: $OUT"; exit 1; }
  [ -z "$(echo "$OUT" | jget data.markdown)" ] || { echo "FAIL: expected empty markdown: $OUT"; exit 1; }
  rc=0; hvj release notes --from commits --since no_such_ref >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: unresolvable --since expected exit 3, got $rc"; exit 1; }
  rc=0; hvj release notes >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: missing --from expected exit 2, got $rc"; exit 1; }
)
rm -rf "$CL1"
pass "release notes --from commits exits 0 with empty markdown on empty range"

echo "release changelog"
UC1="$(mktemp -d)"
TMP_DV="$UC1"
TODAY=$(date +%Y-%m-%d)
(
  cd "$UC1"
  mkdir -p .rota
  printf '### Highlights\n\n- thing 1\n' > notes.md
  OUT=$(hvj release changelog 1.0.0 --body-file notes.md) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.changed)" = "true" ] || { echo "FAIL: changed not true: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.path)" = "CHANGELOG.md" ] || { echo "FAIL: path wrong: $OUT"; exit 1; }
  [ -f CHANGELOG.md ] || { echo "FAIL: CHANGELOG.md not created"; exit 1; }
  head -1 CHANGELOG.md | grep "^# Changelog" >/dev/null || { echo "FAIL: missing # Changelog header"; exit 1; }
  grep -q "^## v1.0.0 — $TODAY" CHANGELOG.md || { echo "FAIL: missing v1.0.0 section with today's date"; exit 1; }
)
pass "release changelog creates CHANGELOG.md with correct header and date"

# Second version appears above first; body read from stdin
(
  cd "$UC1"
  printf '### Notes\n\n- thing 2\n' | hvj release changelog 1.1.0 --body-file - >/dev/null
  LINE110=$(grep -n "^## v1.1.0" CHANGELOG.md | cut -d: -f1)
  LINE100=$(grep -n "^## v1.0.0" CHANGELOG.md | cut -d: -f1)
  [ "$LINE110" -lt "$LINE100" ] || { echo "FAIL: v1.1.0 ($LINE110) not above v1.0.0 ($LINE100)"; exit 1; }
)
pass "release changelog prepends newer version above older one"

# Duplicate version -> exit 4, file untouched
(
  cd "$UC1"
  BEFORE=$(cat CHANGELOG.md)
  rc=0
  OUT=$(hvj release changelog 1.1.0 --body-file notes.md 2>/dev/null) || rc=$?
  [ "$rc" = "4" ] || { echo "FAIL: expected exit 4 for duplicate version, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] || { echo "FAIL: refusal data wrong: $OUT"; exit 1; }
  [ "$(cat CHANGELOG.md)" = "$BEFORE" ] || { echo "FAIL: refused changelog was modified"; exit 1; }
)
pass "release changelog refuses duplicate version"

# --path flag
(
  cd "$UC1"
  mkdir -p docs
  OUT=$(hvj release changelog 0.0.1 --body-file notes.md --path docs/CHANGELOG.md) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$(echo "$OUT" | jget data.path)" = "docs/CHANGELOG.md" ] || { echo "FAIL: path wrong: $OUT"; exit 1; }
  [ -f docs/CHANGELOG.md ] || { echo "FAIL: docs/CHANGELOG.md not created"; exit 1; }
)
pass "release changelog --path writes to custom path"

# Invalid version -> exit 2; missing notes file -> exit 3
(
  cd "$UC1"
  rc=0
  hvj release changelog 1.0 --body-file notes.md >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: expected exit 2 for invalid version '1.0', got $rc"; exit 1; }
  rc=0
  hvj release changelog 7.0.0 --body-file missing.md >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: expected exit 3 for missing notes file, got $rc"; exit 1; }
)
rm -rf "$UC1"
pass "release changelog rejects invalid version format and missing notes"

echo "release host"
DH1="$(mktemp -d)"
TMP_DV="$DH1"
(
  cd "$DH1"
  mkdir -p .rota
  git init -q
  git config user.email t@t && git config user.name t

  # No origin -> none
  OUT=$(hvj release host | jget data.host) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$OUT" = "none" ] || { echo "FAIL: expected 'none' with no origin, got '$OUT'"; exit 1; }
)
pass "release host returns none with no origin"

(
  cd "$DH1"
  # SSH github.com -> github
  git remote add origin git@github.com:foo/bar.git
  OUT=$(hvj release host | jget data.host) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$OUT" = "github" ] || { echo "FAIL: expected 'github' for git@github.com, got '$OUT'"; exit 1; }

  # HTTPS github.com -> github
  git remote set-url origin https://github.com/foo/bar.git
  OUT=$(hvj release host | jget data.host) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$OUT" = "github" ] || { echo "FAIL: expected 'github' for https://github.com, got '$OUT'"; exit 1; }

  # gitlab.com -> gitlab
  git remote set-url origin git@gitlab.com:foo/bar.git
  OUT=$(hvj release host | jget data.host) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$OUT" = "gitlab" ] || { echo "FAIL: expected 'gitlab', got '$OUT'"; exit 1; }

  # self-hosted gitlab -> gitlab-self-hosted
  git remote set-url origin https://gitlab.example.com/foo/bar.git
  OUT=$(hvj release host | jget data.host) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$OUT" = "gitlab-self-hosted" ] || { echo "FAIL: expected 'gitlab-self-hosted', got '$OUT'"; exit 1; }

  # GitHub Enterprise -> github-enterprise
  git remote set-url origin https://github.example.com/foo/bar.git
  OUT=$(hvj release host | jget data.host) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$OUT" = "github-enterprise" ] || { echo "FAIL: expected 'github-enterprise', got '$OUT'"; exit 1; }

  # Bitbucket (unrecognised) -> none
  git remote set-url origin https://bitbucket.org/foo/bar.git
  OUT=$(hvj release host | jget data.host) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$OUT" = "none" ] || { echo "FAIL: expected 'none' for bitbucket.org, got '$OUT'"; exit 1; }
)
rm -rf "$DH1"
trap 'rm -rf "$TMP"' EXIT
pass "release host classifies github/gitlab/github-enterprise/gitlab-self-hosted/none"
