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

# ── the behavioural eval case files are well-formed (#279) ──────────────────
# Offline only: no model call. Live runs are `python3 test/evals/run.py triggers|scenarios`.
OUT="$(python3 "$TESTDIR/evals/run.py" --check 2>&1)" || fail "eval case files are malformed: $OUT"
pass "eval case files cover every skill's triggers and 3+ scenarios for work, ship and capture"

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
rc=0; OUT="$(python3 "$LINT" "$ROTA_BIN" "$REPO/skills/rota-orchestrate/$SK" "$REPO/skills/rota-orchestrate/solo-and-autopilot.md" "$REPO/docs/usage/parallel-rounds.md" 2>&1)" || rc=$?
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

echo "All doclint checks passed."
