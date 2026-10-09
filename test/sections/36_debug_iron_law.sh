echo "F02: debug counter Iron Law gate"

# ── standalone fallbacks (runner.sh sets these; define here for direct bash invocation) ──
_SECTION_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
_REPO_ROOT="$(cd "$_SECTION_DIR/../.." && pwd)"
: "${TMP:=$(mktemp -d)}"
# Define pass/fail if not sourced from lib.sh
if ! declare -f pass >/dev/null 2>&1; then
  pass() { printf '  \033[32mOK\033[0m  %s\n' "$1"; }
  fail() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; exit 1; }
fi

# ── fixture ─────────────────────────────────────────────────────────────────
TMP_DEBUG="$(mktemp -d)"
trap 'rm -rf "$TMP_DEBUG"' EXIT

(
  cd "$TMP_DEBUG"
  git init -q
  git config user.email "test@test.com"
  git config user.name "Test"
  git commit -q --allow-empty -m "seed"
  git checkout -q -b rota/F02-smoke

  mkdir -p .rota

  SF=".rota/debug/rota-F02-smoke.json"
  # sget <field>: one field of the state file, via the contract-shaped `show` data
  sget() { hvj debug counter show | jget "data.$1"; }

  # ── (a) init creates the file with expected schema ───────────────────────
  OUT=$(hvj debug counter init F02) || { echo "FAIL: init exited non-zero"; exit 1; }
  [ "$(jget data.session <<<"$OUT")" = "rota-F02-smoke" ] || { echo "FAIL: init session wrong: $OUT"; exit 1; }
  [ "$(jget data.bugId <<<"$OUT")" = "F02" ] || { echo "FAIL: init bugId wrong: $OUT"; exit 1; }
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || { echo "FAIL: first init should report changed: $OUT"; exit 1; }
  [ -f "$SF" ] || { echo "FAIL: session file missing"; exit 1; }
  python3 -c "
import json, sys
d = json.load(open('$SF'))
assert d['session'] == 'rota-F02-smoke', f\"session={d['session']}\"
assert d['bug_id'] == 'F02', f\"bug_id={d['bug_id']}\"
assert d['failed_fixes'] == 0, f\"failed_fixes={d['failed_fixes']}\"
assert d['attempts'] == [], f\"attempts={d['attempts']}\"
" || { echo "FAIL: schema check"; exit 1; }
  [ "$(sget session)" = "rota-F02-smoke" ] || { echo "FAIL: show session wrong"; exit 1; }
  [ "$(sget failedFixes)" = "0" ] || { echo "FAIL: show failedFixes wrong"; exit 1; }
  [ "$(sget attempts)" = "[]" ] || { echo "FAIL: show attempts wrong"; exit 1; }

  # ── (b) init is idempotent ────────────────────────────────────────────────
  started_before="$(sget startedAt)"
  OUT=$(hvj debug counter init F02) || { echo "FAIL: repeat init exited non-zero"; exit 1; }
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || { echo "FAIL: repeat init should report changed=false: $OUT"; exit 1; }
  started_after="$(sget startedAt)"
  [ "$started_before" = "$started_after" ] || { echo "FAIL: init not idempotent (timestamp changed)"; exit 1; }

  # ── (c) record-attempt reports the attempt number and (d) fail marks outcome ───
  # Pattern: record → fail → record → fail → record → fail (one pending at a time)
  n1="$(hvj debug counter record-attempt --hypothesis "hyp-one" --commit "abc1111" | jget data.attempt)" || vfail
  [ "$n1" = "1" ] || { echo "FAIL: first attempt number expected 1, got $n1"; exit 1; }
  f1="$(hvj debug counter fail | jget data.failedFixes)" || vfail
  [ "$f1" = "1" ] || { echo "FAIL: fail#1 expected 1, got $f1"; exit 1; }
  [ "$(sget attempts[0].outcome)" = "failed" ] || { echo "FAIL: attempt[0] not marked failed"; exit 1; }
  [ -n "$(sget attempts[0].endedAt)" ] || { echo "FAIL: attempt[0] has no endedAt"; exit 1; }

  n2="$(hvj debug counter record-attempt --hypothesis "hyp-two" --commit "abc2222" | jget data.attempt)" || vfail
  [ "$n2" = "2" ] || { echo "FAIL: second attempt number expected 2, got $n2"; exit 1; }
  f2="$(hvj debug counter fail | jget data.failedFixes)" || vfail
  [ "$f2" = "2" ] || { echo "FAIL: fail#2 expected 2, got $f2"; exit 1; }

  n3="$(hvj debug counter record-attempt --hypothesis "hyp-three" --commit "abc3333" | jget data.attempt)" || vfail
  [ "$n3" = "3" ] || { echo "FAIL: third attempt number expected 3, got $n3"; exit 1; }
  f3="$(hvj debug counter fail | jget data.failedFixes)" || vfail
  [ "$f3" = "3" ] || { echo "FAIL: fail#3 expected 3, got $f3"; exit 1; }

  # verify attempts array has 3 entries all failed
  hvj debug counter show | python3 -c "
import json, sys
d = json.load(sys.stdin)['data']
assert len(d['attempts']) == 3, f\"expected 3 attempts, got {len(d['attempts'])}\"
assert all(a['outcome'] == 'failed' for a in d['attempts']), d['attempts']
assert d['failedFixes'] == 3, f\"failedFixes={d['failedFixes']}\"
" || { echo "FAIL: attempts array or failedFixes wrong"; exit 1; }

  # fail and pass need a pending attempt — exit 4, nothing changes
  rc=0; OUT=$(hvj debug counter fail 2>/dev/null) || rc=$?
  [ "$rc" = "4" ] || { echo "FAIL: fail with no pending attempt should exit 4, got $rc"; exit 1; }
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || { echo "FAIL: refused fail should report changed=false: $OUT"; exit 1; }
  rc=0; hvj debug counter pass >/dev/null 2>&1 || rc=$?
  [ "$rc" = "4" ] || { echo "FAIL: pass with no pending attempt should exit 4, got $rc"; exit 1; }
  [ "$(sget failedFixes)" = "3" ] || { echo "FAIL: refused fail changed failedFixes"; exit 1; }

  # record-attempt needs both flags — exit 2
  rc=0; hvj debug counter record-attempt --hypothesis "no-commit" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: record-attempt without --commit should exit 2, got $rc"; exit 1; }

  # ── (e) summary renders Iron Law markdown ─────────────────────────────────
  SUMMARY="$(hvj debug counter summary | jget data.markdown)" || vfail
  grep -q "Iron Law triggered for \[F02\]" <<<"$SUMMARY" || { echo "FAIL: summary missing Iron Law header"; exit 1; }
  grep -q "abc1111" <<<"$SUMMARY" || { echo "FAIL: summary missing commit abc1111"; exit 1; }
  grep -q "abc2222" <<<"$SUMMARY" || { echo "FAIL: summary missing commit abc2222"; exit 1; }
  grep -q "abc3333" <<<"$SUMMARY" || { echo "FAIL: summary missing commit abc3333"; exit 1; }
  grep -q "hyp-one" <<<"$SUMMARY" || { echo "FAIL: summary missing hypothesis hyp-one"; exit 1; }
  grep -q "hyp-two" <<<"$SUMMARY" || { echo "FAIL: summary missing hypothesis hyp-two"; exit 1; }
  grep -q "hyp-three" <<<"$SUMMARY" || { echo "FAIL: summary missing hypothesis hyp-three"; exit 1; }
  grep -q "Next steps" <<<"$SUMMARY" || { echo "FAIL: summary missing Next steps section"; exit 1; }

  # ── (f) pass flow (clear + re-init to start fresh) ────────────────────────
  hvj debug counter clear >/dev/null || { echo "FAIL: clear exited non-zero"; exit 1; }
  [ ! -f "$SF" ] || { echo "FAIL: clear did not remove file"; exit 1; }

  hvj debug counter init F02 >/dev/null || { echo "FAIL: re-init exited non-zero"; exit 1; }
  # summary with no attempts is a read-only "no" — exit 1, empty markdown
  rc=0; OUT=$(hvj debug counter summary 2>/dev/null) || rc=$?
  [ "$rc" = "1" ] || { echo "FAIL: summary with no attempts should exit 1, got $rc"; exit 1; }
  [ -z "$(jget data.markdown <<<"$OUT")" ] || { echo "FAIL: summary with no attempts should have empty markdown: $OUT"; exit 1; }
  hvj debug counter record-attempt --hypothesis "clean-hyp" --commit "cccc000" >/dev/null || { echo "FAIL: record-attempt failed"; exit 1; }
  [ "$(hvj debug counter pass | jget data.attempt)" = "1" ] || { echo "FAIL: pass should report attempt 1"; exit 1; }
  [ "$(sget attempts[0].outcome)" = "passed" ] || { echo "FAIL: pass outcome wrong"; exit 1; }
  [ "$(sget failedFixes)" = "0" ] || { echo "FAIL: failedFixes should be 0 for pass flow, got $(sget failedFixes)"; exit 1; }

  # ── (g) clear removes file and is idempotent ──────────────────────────────
  OUT=$(hvj debug counter clear) || { echo "FAIL: clear exited non-zero"; exit 1; }
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || { echo "FAIL: clear of an existing session should report changed: $OUT"; exit 1; }
  [ ! -f "$SF" ] || { echo "FAIL: second clear left file"; exit 1; }
  OUT=$(hvj debug counter clear) || { echo "FAIL: clear must be idempotent (exit 0)"; exit 1; }
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || { echo "FAIL: idempotent clear should report changed=false: $OUT"; exit 1; }

  # ── (h) show exits 3 when no session exists ───────────────────────────────
  rc=0; hvj debug counter show >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: show should exit 3 when no session, got $rc"; exit 1; }
  rc=0; hvj debug counter inc-cycle >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: inc-cycle should exit 3 when no session, got $rc"; exit 1; }

  # ── (i) inc-cycle is separate from failed_fixes ───────────────────────────
  hvj debug counter init F02 >/dev/null || { echo "FAIL: init before inc-cycle failed"; exit 1; }
  hvj debug counter inc-cycle >/dev/null
  c2="$(hvj debug counter inc-cycle | jget data.hypothesisCycles)" || vfail
  [ "$c2" = "2" ] || { echo "FAIL: inc-cycle second call expected 2, got $c2"; exit 1; }
  python3 -c "
import json
d = json.load(open('$SF'))
assert d['hypothesis_cycles'] == 2, f\"hypothesis_cycles={d['hypothesis_cycles']}\"
assert d['failed_fixes'] == 0, f\"failed_fixes should be 0, got {d['failed_fixes']}\"
" || { echo "FAIL: inc-cycle counter or failed_fixes wrong"; exit 1; }
) || exit 1

trap 'rm -rf "$TMP"' EXIT
rm -rf "$TMP_DEBUG"
pass "debug counter: init/idempotent/record-attempt/fail/pass/summary/clear/show/inc-cycle all correct"
