#!/usr/bin/env bash
# Doc lints: checks on repository files, not on rota's behaviour. They need no
# smoke state, so test/gate.sh runs this beside validate-skills and the Go
# tests instead of inside the sequential smoke run (#79). Each check works in
# its own temp dir.
#
# Usage: bash test/doclint.sh
# Env:   ROTA_BIN=<abs path> lints verbs against that binary instead of building one.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
TESTDIR="$REPO/test"
. "$TESTDIR/lib.sh"

TMP="$(mktemp -d)" || exit 1
trap 'rm -rf "$TMP"' EXIT
SK='SKILL''.md'

echo "doclint: skills and references pass the validator; the prose lint catches drift (#173)"

# ── skills and references pass the validator ────────────────────────────────
OUT="$(cd "$REPO" && python3 "$TESTDIR/validate-skills.py" 2>&1)" || fail "validate-skills fails on the repo: $OUT"
pass "skills and references pass the doclint"

# ── skill lists are generated from docs/skills ──────────────────────────────
OUT="$(python3 "$REPO/scripts/skills-docs.py" --check 2>&1)" || fail "skill lists are stale or the skill pages are invalid: $OUT"
pass "skill lists are generated from docs/skills"

SG="$TMP/skillsgen"; mkdir -p "$SG"
cp -R "$REPO/docs" "$REPO/skills" "$REPO/scripts" "$REPO/README.md" "$SG/"
sed -i 's/^summary: Build the next item.*/summary: Build something else entirely./' "$SG/docs/skills/rota-work.md"
RC=0; OUT="$(python3 "$SG/scripts/skills-docs.py" --check --root "$SG" 2>&1)" || RC=$?
[ "$RC" = 1 ] && grep -qF "README.md" <<<"$OUT" \
  || fail "skills-docs --check missed an edited summary (rc $RC): $OUT"
pass "skills-docs --check fails when a skill page changes without regenerating"

# ── the chain lint can fail (#1897) ─────────────────────────────────────────
# A line that tells a model to run a manual-only skill must fail the validator, even
# with "ask the user" or "never" nearby and in a nested reference file; a line that
# tells the user to type it passes.
CL="$TMP/chain"; mkdir -p "$CL"
cp -R "$REPO/skills" "$REPO/docs" "$REPO/README.md" "$REPO/CHANGELOG.md" "$CL/"
mkdir -p "$CL/skills/rota-plan/nested"
printf 'Ask the user, then invoke `rota-decide` and never skip it.\n' > "$CL/skills/rota-plan/nested/chain.md"
RC=0; OUT="$(cd "$CL" && python3 "$TESTDIR/validate-skills.py" 2>&1)" || RC=$?
[ "$RC" = 1 ] && grep -qF "skills/rota-plan/nested/chain.md:1: chains into flagged skill rota-decide" <<<"$OUT" \
  || fail "chain lint missed a chaining line (rc $RC): $OUT"
printf 'When a rule needs recording, tell the user to type `/rota-decide`.\n' > "$CL/skills/rota-plan/nested/chain.md"
RC=0; OUT="$(cd "$CL" && python3 "$TESTDIR/validate-skills.py" 2>&1)" || RC=$?
[ "$RC" = 0 ] || fail "chain lint rejected an explicit offer (rc $RC): $OUT"
pass "the chain lint fails a line that runs a flagged skill, in nested files too, and passes an explicit offer"

# A minimal skill tree has no authoring-conventions.md: the flagged-list check skips it
# (section 94 lints such a fixture). It stays strict when the file exists (below).
rm -f "$CL/skills/rota-plan/nested/chain.md" "$CL/skills/references/authoring-conventions.md"
RC=0; OUT="$(cd "$CL" && ROTA_DOCLINT_PROSE=off python3 "$TESTDIR/validate-skills.py" 2>&1)" || RC=$?
grep -qF "authoring-conventions.md: no 'The set is" <<<"$OUT" \
  && fail "flagged-list check failed a tree without authoring-conventions.md (rc $RC): $OUT"
pass "the flagged-list check skips a tree without authoring-conventions.md"

# ── the behavioural eval case files are well-formed (#279) ──────────────────
# Offline only: no model call. Live runs are `python3 test/evals/run.py triggers|scenarios`.
OUT="$(python3 "$TESTDIR/evals/run.py" --check 2>&1)" || fail "eval case files are malformed: $OUT"
pass "eval case files cover every skill's triggers and 3+ scenarios for work, ship and capture"
OUT="$(python3 -m unittest discover -s "$TESTDIR/evals" -p 'test_*.py' 2>&1)" || fail "offline eval runner tests failed: $OUT"
pass "eval runner rejects failed calls and invalid routes without aborting the batch"

# ── the prose lint can fail ─────────────────────────────────────────────────
# Drop a pinned phrase from a copy of the real skills and the validator names
# the file; a deleted target file is reported, not skipped.
PL="$TMP/prose"; mkdir -p "$PL"
cp -R "$REPO/skills" "$REPO/docs" "$REPO/README.md" "$REPO/CHANGELOG.md" "$PL/"
OUT="$(cd "$PL" && python3 "$TESTDIR/validate-skills.py" 2>&1)" || fail "prose lint fails on a copy of the repo: $OUT"
sed -i 's/rota status handoff/rota status hand-off/' "$PL/skills/rota-work/no-argument-mode.md"
RC=0; OUT="$(cd "$PL" && python3 "$TESTDIR/validate-skills.py" 2>&1)" || RC=$?
[ "$RC" = 1 ] && grep -qF "skills/rota-work/no-argument-mode.md: must call rota status handoff" <<<"$OUT" \
  || fail "prose lint missed a dropped phrase (rc $RC): $OUT"
rm -f "$PL/skills/references/manual-gates.md"
RC=0; OUT="$(cd "$PL" && python3 "$TESTDIR/validate-skills.py" 2>&1)" || RC=$?
[ "$RC" = 1 ] && grep -qF "skills/references/manual-gates.md: prose rule target is missing" <<<"$OUT" \
  || fail "prose lint skipped a missing target file (rc $RC): $OUT"
pass "the prose lint names a skill that lost a pinned phrase and a missing target file"

# ── a decoy under .worktrees/ is invisible to validate-skills (#79) ─────────
VS="$TMP/vs"
mkdir -p "$VS"
cp -R "$REPO/skills" "$REPO/docs" "$REPO/README.md" "$REPO/CHANGELOG.md" "$VS/"
mkdir -p "$VS/test"; cp "$TESTDIR/validate-skills.py" "$VS/test/"
BASE_OUT="$(cd "$VS" && python3 test/validate-skills.py 2>&1)" || fail "validate-skills fixture does not pass on its own: $BASE_OUT"
# A decoy skill that would fail every check, and a duplicate of a real one.
mkdir -p "$VS/.worktrees/x/skills/rota-decoy" "$VS/.worktrees/x/skills/rota-plan"
printf 'no frontmatter, banner or references\n' > "$VS/.worktrees/x/skills/rota-decoy/$SK"
cp "$VS/skills/rota-plan/$SK" "$VS/.worktrees/x/skills/rota-plan/$SK"
DECOY_OUT="$(cd "$VS" && python3 test/validate-skills.py 2>&1)" || fail "validate-skills picked up .worktrees/: $DECOY_OUT"
[ "$BASE_OUT" = "$DECOY_OUT" ] || fail "validate-skills output changed with a decoy: '$BASE_OUT' vs '$DECOY_OUT'"
pass "a decoy SKILL.md/CLAUDE.md under .worktrees/ is invisible to validate-skills"

# ── census: the validator does not walk the project tree recursively ────────
# validate-skills globs `skills/rota-*/SKILL.md` (one level). A recursive walk of the
# project root would find nested checkouts; fail on one. No exemptions.
WALK='rglob\(|os\.walk\(|os\.scandir\(|recursive ?= ?True|glob\([^)]*\*\*|find +(\.|\./|"\$PWD"|\$PWD|"\$\(pwd\)"|\$\(pwd\))( |$)'
# The pattern must bite: each of these walks has to trip it.
for SAMPLE in 'Path(".").rglob("SKILL.md")' 'os.walk(".")' 'os.scandir(root)' 'glob.glob("**/SKILL.md", recursive=True)' \
              'glob.glob(f"{d}/**/x")' 'find . -name SKILL.md' 'find "$PWD" -type f' 'find $PWD -type f'; do
  grep -qE "$WALK" <<<"$SAMPLE" || fail "census pattern does not catch: $SAMPLE"
done
for SAMPLE in 'find "$root/cmd" -newer "$bin"' 'sorted(Path(".").glob("skills/rota-*/SKILL.md"))'; do
  if grep -qE "$WALK" <<<"$SAMPLE"; then fail "census pattern flags an anchored lookup: $SAMPLE"; fi
done
HITS="$(cd "$REPO" && grep -nE "$WALK" test/validate-skills.py 2>/dev/null || true)"
[ -z "$HITS" ] || fail "recursive tree walk found, it would pick up .worktrees/ checkouts; prune them or anchor the walk: $HITS"
pass "the validator does not walk the project tree recursively"

# ── every `rota <group> <verb>` the skill and the docs name exists ──────────
# test/lint-verbs.py resolves a verb when `rota <words> --help` descends the
# tree (see its header). The lint must also be able to fail.
if [ -z "${ROTA_BIN:-}" ]; then
  ROTA_VERSION="$(tr -d '[:space:]' < "$REPO/VERSION")"
  (cd "$REPO" && go build -ldflags "-X github.com/l4ci/rota/internal/version.Version=$ROTA_VERSION" \
    -o "$TMP/rota" ./cmd/rota) || fail "go build ./cmd/rota failed"
  ROTA_BIN="$TMP/rota"
fi
LINT="$TESTDIR/lint-verbs.py"
rc=0; OUT="$(python3 "$LINT" "$ROTA_BIN" "$REPO/skills/rota-orchestrate/$SK" "$REPO/skills/rota-orchestrate/solo-and-autopilot.md" "$REPO"/skills/rota-orchestrate/{architecture-review,reading-failures,escalations-and-provenance,merge-train-and-approval,bounce-cap}.md "$REPO/docs/usage/parallel-rounds.md" 2>&1)" || rc=$?
[ "$rc" = "0" ] || fail "verbs named in the skill or docs that do not exist: $OUT"
case "$OUT" in *"RESOLVED "*) ;; *) fail "the verb lint resolved nothing: $OUT" ;; esac
# A doc naming a verb that is not there is caught.
printf 'Run `rota round waitt` and `rota worker gate <slot>`.\n' > "$TMP/bad.md"
rc=0; BAD="$(python3 "$LINT" "$ROTA_BIN" "$TMP/bad.md" 2>&1)" || rc=$?
[ "$rc" = "1" ] && grep -q 'MISSING .*rota round waitt' <<<"$BAD" || fail "the lint should reject a made-up verb: rc=$rc $BAD"
# The C10 verbs resolve for real, and a made-up neighbour is still caught.
printf 'Run `rota round reclaim ben`, `rota round return ben` and `rota round transfer 5 --to dana`.\n' > "$TMP/c10.md"
rc=0; GOOD="$(python3 "$LINT" "$ROTA_BIN" "$TMP/c10.md" 2>&1)" || rc=$?
[ "$rc" = "0" ] && grep -q 'RESOLVED 3' <<<"$GOOD" || fail "the C10 verbs should resolve: rc=$rc $GOOD"
printf 'Run `rota round reclaim ben` and `rota round bouncer`.\n' > "$TMP/c10b.md"
rc=0; BAD="$(python3 "$LINT" "$ROTA_BIN" "$TMP/c10b.md" 2>&1)" || rc=$?
[ "$rc" = "1" ] && grep -q 'MISSING .*rota round bouncer' <<<"$BAD" || fail "a made-up round verb should fail: rc=$rc $BAD"
pass "every rota verb in skills/rota-orchestrate/SKILL.md and docs/usage/parallel-rounds.md resolves ($(grep -o 'RESOLVED [0-9]*' <<<"$OUT"))"

# ── contract docs carry refs, STAMPS carries each doc's verified sha, no ref drifted since (#392, #1294) ──
# test/check-doc-stamps.py: `refs:` frontmatter on every docs/contributing/contract/*.md and one
# `<doc> <sha>` line per doc in docs/contributing/contract/STAMPS; a ref changed after the sha is
# drift: a WARN by default, a failure under --strict / ROTA_DOC_STAMPS=strict. A broken stamp
# always fails. All outcomes run against a throwaway repo so the real history never has to drift.
OUT="$(python3 "$TESTDIR/check-doc-stamps.py" 2>&1)" || fail "contract docs have a broken stamp: $OUT"
unset ROTA_DOC_STAMPS # the fixture cases below pin their own mode; the caller's must not leak in
SG="$TMP/stamps"; mkdir -p "$SG/docs/contributing/contract" "$SG/src"
git -C "$SG" init -q
git -C "$SG" config user.email t@t; git -C "$SG" config user.name t
echo one > "$SG/src/a.go"; echo one > "$SG/src/b.go"
git -C "$SG" add src; git -C "$SG" commit -qm base
SSHA="$(git -C "$SG" rev-parse HEAD)"
SDOC="$SG/docs/contributing/contract/ok.md"; SSTAMPS="$SG/docs/contributing/contract/STAMPS"
printf -- '---\nrefs:\n  - src/a.go\n---\n\n# doc\n' > "$SDOC"
printf 'ok.md %s\n' "$SSHA" > "$SSTAMPS"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 0 ] || fail "a stamped, unchanged doc should pass (rc $rc): $OUT"
echo two > "$SG/src/b.go"; echo two > "$SG/src/a.go"
git -C "$SG" add -A; git -C "$SG" commit -qm drift
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 0 ] && grep -qF "WARN docs/contributing/contract/ok.md: refs changed since verified-sha $SSHA: src/a.go" <<<"$OUT" && ! grep -qF "src/b.go" <<<"$OUT" \
  || fail "drift should warn about only the changed path and exit 0 (rc $rc): $OUT"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" --strict 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -qF "ok.md: refs changed since verified-sha $SSHA: src/a.go" <<<"$OUT" && ! grep -qF "WARN" <<<"$OUT" \
  || fail "--strict should fail on drift (rc $rc): $OUT"
rc=0; OUT="$(ROTA_DOC_STAMPS=strict python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 1 ] || fail "ROTA_DOC_STAMPS=strict should fail on drift (rc $rc): $OUT"
# --restamp clears the drift and touches only STAMPS, never the doc
DOC_BEFORE="$(cat "$SDOC")"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" --restamp ok.md 2>&1)" || rc=$?
[ "$rc" = 0 ] && [ "$DOC_BEFORE" = "$(cat "$SDOC")" ] || fail "--restamp should exit 0 and leave the doc alone (rc $rc): $OUT"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" --strict 2>&1)" || rc=$?
[ "$rc" = 0 ] || fail "a restamped doc should pass --strict (rc $rc): $OUT"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" --restamp nope.md 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -qF "nope.md: not a contract doc" <<<"$OUT" || fail "--restamp of an unknown doc should fail (rc $rc): $OUT"
printf 'ok.md nothex\n' > "$SSTAMPS"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -qF "malformed verified-sha" <<<"$OUT" || fail "a malformed sha should fail (rc $rc): $OUT"
printf 'ok.md %s\n' "0000000000000000000000000000000000000000" > "$SSTAMPS"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -qF "not a known commit" <<<"$OUT" || fail "an unknown sha should fail (rc $rc): $OUT"
printf 'ok.md %s\n' "$SSHA" > "$SSTAMPS"
printf -- '---\nrefs:\n  - src/gone.go\n---\n' > "$SDOC"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -qF "ref src/gone.go does not exist" <<<"$OUT" || fail "a missing ref path should fail (rc $rc): $OUT"
printf '# no frontmatter\n' > "$SDOC"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -qF "no frontmatter" <<<"$OUT" || fail "an unstamped doc should fail (rc $rc): $OUT"
printf -- '---\nrefs:\n  - src/a.go\n---\n' > "$SDOC"
: > "$SSTAMPS"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -qF "missing stamp" <<<"$OUT" || fail "a doc with no STAMPS line should fail (rc $rc): $OUT"
printf -- '---\nverified-sha: %s\nrefs:\n  - src/a.go\n---\n' "$SSHA" > "$SDOC"
printf 'ok.md %s\n' "$SSHA" > "$SSTAMPS"
rc=0; OUT="$(python3 "$TESTDIR/check-doc-stamps.py" --root "$SG" 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -qF "belongs in docs/contributing/contract/STAMPS" <<<"$OUT" || fail "a stamp left in the doc should fail (rc $rc): $OUT"
# #1294: two branches editing one doc's body in different places merge with no stamp conflict
printf -- '---\nrefs:\n  - src/a.go\n---\n\n# doc\n\nfirst\n\n'"$(printf 'x\n%.0s' 1 2 3 4 5 6)"'\nlast\n' > "$SDOC"
git -C "$SG" add -A; git -C "$SG" commit -qm "doc body"
BASE="$(git -C "$SG" rev-parse HEAD)"
git -C "$SG" checkout -q -b pr1 "$BASE"; sed -i 's/^first$/first edited/' "$SDOC"; git -C "$SG" commit -qam pr1
git -C "$SG" checkout -q -b pr2 "$BASE"; sed -i 's/^last$/last edited/' "$SDOC"; git -C "$SG" commit -qam pr2
rc=0; OUT="$(git -C "$SG" merge -q pr1 2>&1)" || rc=$?
[ "$rc" = 0 ] || fail "pr1 should merge (rc $rc): $OUT"
rc=0; OUT="$(git -C "$SG" merge -q pr2 2>&1)" || rc=$?
[ "$rc" = 0 ] || fail "two PRs on one contract doc should merge with no stamp conflict (rc $rc): $OUT"
pass "contract doc drift warns by default and fails under strict; --restamp clears it; a bad sha, unknown sha, missing ref, missing stamp and a stamp left in the doc always fail by name; two PRs on one doc merge clean"

# ── the config reference page is generated from the schema (#541) ───────────
# internal/config/keys.go is the source; a hand edit or a stale page fails.
GEN="$TMP/config-options.md"
(cd "$REPO" && go run ./internal/config/genref "$GEN") || fail "go run ./internal/config/genref failed"
diff -u "$REPO/docs/reference/config-options.md" "$GEN" >"$TMP/config-options.diff" \
  || fail "docs/reference/config-options.md differs from the schema; run: go generate ./internal/config
$(head -20 "$TMP/config-options.diff")"
cp "$REPO/docs/reference/config-options.md" "$TMP/config-options.edited"
printf 'hand edit\n' >> "$TMP/config-options.edited"
if diff -q "$TMP/config-options.edited" "$GEN" >/dev/null; then fail "the config page drift check cannot fail"; fi
pass "docs/reference/config-options.md matches what the config schema generates"

echo "All doclint checks passed."
