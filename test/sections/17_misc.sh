echo "release bump"
mkdir bv-dry && cd bv-dry

# bump <fixture> <args…>: bumps a fresh copy of the fixture, so each case starts at 1.2.3
echo '{"name": "foo", "version": "1.2.3"}' > plugin.json.orig
cp plugin.json.orig plugin.json
bump() { cp plugin.json.orig plugin.json; hvj release bump --file plugin.json "$@"; }
file_version() { python3 -c "import json; print(json.loads(open('$1').read())['version'])"; }

# release version previews the bump read-only: data.next, file byte-unchanged
# (own project dir: detection runs from the project root, so it needs a root of its own)
mkdir -p prev/.rota && cd prev
echo '{"name": "foo", "version": "1.2.3"}' > package.json
ORIG_HASH=$(sha256sum package.json | cut -c1-16)
preview() { hvj release version "$@" | jget data.next; }
[ "$(preview --level patch)" = "1.2.4" ] || fail "preview --level patch: expected 1.2.4"
[ "$(preview --level minor)" = "1.3.0" ] || fail "preview --level minor: expected 1.3.0"
[ "$(preview --level major)" = "2.0.0" ] || fail "preview --level major: expected 2.0.0"
[ "$(preview --to 1.5.0)" = "1.5.0" ] || fail "preview --to 1.5.0: expected 1.5.0"
[ "$(sha256sum package.json | cut -c1-16)" = "$ORIG_HASH" ] || fail "release version preview should not modify the file"
pass "release version --level/--to previews patch, minor, major and explicit, leaving the file unchanged"

rc=0; OUT=$(hvj release version --to 1.0.0 2>/dev/null) || rc=$?
[ "$rc" = 1 ] || fail "preview --to backwards should exit 1 (got $rc): $OUT"
[ "$(sha256sum package.json | cut -c1-16)" = "$ORIG_HASH" ] || fail "rejected preview should not modify the file"
pass "release version rejects a backwards --to with exit 1"
cd ..

# release.versionFile points the preview at a pyproject.toml (own project dir again)
mkdir -p prev-py/.rota && cd prev-py
echo '{"release":{"versionFile":"pyproject.toml"}}' > .rota/config.json
printf '[project]\nname = "foo"\nversion = "0.5.0"\n' > pyproject.toml
ORIG_HASH=$(sha256sum pyproject.toml | cut -c1-16)
OUT=$(hvj release version --level patch) || vfail
[ "$(echo "$OUT" | jget data.next)" = "0.5.1" ] && [ "$(echo "$OUT" | jget data.version)" = "0.5.0" ] && [ "$(echo "$OUT" | jget data.kind)" = "pyproject" ] \
  || fail "pyproject preview: expected 0.5.0 -> 0.5.1, kind pyproject: $OUT"
[ "$(sha256sum pyproject.toml | cut -c1-16)" = "$ORIG_HASH" ] || fail "pyproject preview should not modify the file"
pass "release version previews a pyproject.toml chosen by release.versionFile, leaving it unchanged"
cd ..

# --level patch
OUT=$(bump --level patch)
[ "$(echo "$OUT" | jget data.from)" = "1.2.3" ] && [ "$(echo "$OUT" | jget data.to)" = "1.2.4" ] || fail "--level patch: expected 1.2.3 -> 1.2.4: $OUT"
[ "$(echo "$OUT" | jget data.kind)" = "plugin-json" ] && [ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "--level patch: expected kind plugin-json, changed true: $OUT"
[ "$(file_version plugin.json)" = "1.2.4" ] || fail "--level patch should write 1.2.4 to the file, got '$(file_version plugin.json)'"
pass "release bump --level patch writes 1.2.4 and reports from/to"

# --level minor
[ "$(bump --level minor | jget data.to)" = "1.3.0" ] && [ "$(file_version plugin.json)" = "1.3.0" ] || fail "--level minor: expected 1.3.0"
pass "release bump --level minor writes 1.3.0"

# --level major
[ "$(bump --level major | jget data.to)" = "2.0.0" ] && [ "$(file_version plugin.json)" = "2.0.0" ] || fail "--level major: expected 2.0.0"
pass "release bump --level major writes 2.0.0"

# --to explicit semver
[ "$(bump --to 1.5.0 | jget data.to)" = "1.5.0" ] && [ "$(file_version plugin.json)" = "1.5.0" ] || fail "--to 1.5.0: expected 1.5.0"
pass "release bump --to writes the explicit version"

# --to backwards is refused (exit 4) and leaves the file alone
rc=0; OUT=$(bump --to 1.0.0 2>/dev/null) || rc=$?
[ "$rc" = 4 ] || fail "--to backwards should exit 4 (got $rc): $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "refusal should report changed false: $OUT"
[ "$(file_version plugin.json)" = "1.2.3" ] || fail "refused bump should not modify the file"
pass "release bump refuses a backwards --to and leaves the file unchanged"

# usage: level and --to are exclusive, one is required
rc=0; bump >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "bump without --level or --to should exit 2 (got $rc)"
rc=0; bump --level patch --to 1.5.0 >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "bump with both --level and --to should exit 2 (got $rc)"
pass "release bump needs exactly one of --level and --to"

# pyproject.toml
cat > pyproject.toml <<'TOML'
[project]
name = "foo"
version = "0.5.0"
TOML
OUT=$(hvj release bump --file pyproject.toml --kind pyproject --level minor) || vfail
[ "$(echo "$OUT" | jget data.from)" = "0.5.0" ] && [ "$(echo "$OUT" | jget data.to)" = "0.6.0" ] || fail "pyproject minor: expected 0.5.0 -> 0.6.0: $OUT"
grep -q '"0.6.0"' pyproject.toml || fail "pyproject bump should write 0.6.0"
pass "release bump reads and writes pyproject.toml"

cd ..

echo "spike add no orphan branch on file-collision"
mkdir spike-orphan && cd spike-orphan
git init -q
git config user.email t@t && git config user.name t
git checkout -q -b main 2>/dev/null || git branch -m main
echo "x" > f && git add f && git commit -q -m "seed"
mkdir -p .rota/spikes

# Create the spike file BUT NO branch (simulating partial-state retry)
echo "leftover" > .rota/spikes/foo.md

# spike add should refuse at the file-existence check, NOT create the branch
rc=0; OUT=$(hvj spike add foo --question "?" 2>/dev/null) || rc=$?
[ "$rc" = 4 ] || fail "spike add should exit 4 when .rota/spikes/foo.md already exists (got $rc): $OUT"
[ "$(echo "$OUT" | jget data.blockedBy)" = "exists" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "spike add refusal should report blockedBy exists, changed false: $OUT"

# CRITICAL: branch must NOT exist (orphan-branch regression test)
if git rev-parse --verify spike/foo >/dev/null 2>&1; then
  fail "spike add created branch despite file-collision — orphan branch regression"
fi
pass "spike add does not create branch when spike file already exists"
cd ..
