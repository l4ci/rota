echo "F24: item reopen + ship undo"
# Behaviour guard for `item reopen` and `ship undo`.
# See [F24] — `/rota-ship --undo` guided rollback of the last /rota-work cycle.

UNDO_TMP="$(mktemp -d)"
trap 'rm -rf "$UNDO_TMP"' EXIT

# ── fixture builder ──────────────────────────────────────────────────────────
# Builds a fresh miniature project at $root with:
#   - .rota/BACKLOG.md   one staged Bug, one staged Feature, empty Completed/Tasks
#   - .rota/counters.json with since_refactor.{features,bugs} = 0
#   - .rota/status.json  empty active list
#   - git init on main with a seed commit so future commits have a parent
build_undo_fixture() {
  local root="$1"
  rm -rf "$root/.rota" "$root/.git" "$root"/*.txt 2>/dev/null || true
  mkdir -p "$root/.rota"

  cat > "$root/.rota/BACKLOG.md" <<'FIXEOF'
# TODO

## Bugs
- **[B01] [P1] Sample bug.** Body.

## Features
- **[F03] [Minor] Sample feature.** Body.

## Tasks

## Completed
FIXEOF

  cat > "$root/.rota/counters.json" <<'FIXEOF'
{
  "bugs": 1,
  "features": 3,
  "tasks": 0,
  "milestones": 0,
  "since_refactor": {"features": 0, "bugs": 0, "tasks": 0}
}
FIXEOF
  echo '{"active":[]}' > "$root/.rota/status.json"

  (
    cd "$root"
    git init -q
    git config user.email t@t && git config user.name t
    git checkout -q -b main 2>/dev/null || git branch -m main
    echo seed > seed.txt
    git add -A
    git commit -q -m "seed"
  )
}

# Build a complete /rota-work cycle on $root: a feature branch rota/F03-test with
# two commits, then a no-ff merge into main with subject `merge: F03 …`.
# Writes a Completed line for F03 referencing the real implementation commit's
# short hash so ship undo's done-line lookup succeeds.
build_undo_cycle() {
  local root="$1"
  build_undo_fixture "$root"
  (
    cd "$root"
    git checkout -q -b rota/F03-test
    echo prep > prep.txt
    git add prep.txt
    git commit -q -m "chore: prep for F03"
    echo impl > impl.txt
    git add impl.txt
    git commit -q -m "feat: implement F03"
    local SHORT
    SHORT=$(git rev-parse --short HEAD)
    git checkout -q main
    git merge --no-ff rota/F03-test -q -m "merge: F03 — test cycle"
    git branch -q -d rota/F03-test
    # F03 is now in Completed referencing the impl commit short hash.
    # Remove it from the active ## Features section and append to Completed.
    python3 - "$SHORT" <<'PYEOF'
import sys, pathlib
short = sys.argv[1]
p = pathlib.Path(".rota/BACKLOG.md")
text = p.read_text()
active = "- **[F03] [Minor] Sample feature.** Body.\n"
text = text.replace(active, "")
done = f"- ~~**[F03] [Minor] Sample feature.** Body.~~ Done 2026-01-15 [`{short}`]\n"
# Append after the ## Completed header.
text = text.replace("## Completed\n", "## Completed\n" + done)
p.write_text(text)
PYEOF
    git add .rota/BACKLOG.md
    git commit -q --amend --no-edit
  )
}

# ── (a) item reopen: restore from BACKLOG.md ## Completed, decrement counter ─
(
  cd "$UNDO_TMP"
  build_undo_fixture "$UNDO_TMP"
  # Create a real non-refactor commit so the helper can read its subject.
  echo bugfix > fix.txt
  git add fix.txt
  git commit -q -m "feat: bug fix"
  SHORT=$(git rev-parse --short HEAD)
  # Move B01 from ## Bugs into ## Completed referencing the real short hash.
  python3 - "$SHORT" <<'PYEOF'
import sys, pathlib
short = sys.argv[1]
p = pathlib.Path(".rota/BACKLOG.md")
text = p.read_text()
text = text.replace("- **[B01] [P1] Sample bug.** Body.\n", "")
done = f"- ~~**[B01] [P1] Sample bug.** Body.~~ Done 2026-01-15 [`{short}`]\n"
text = text.replace("## Completed\n", "## Completed\n" + done)
p.write_text(text)
PYEOF
  # Bump since_refactor.bugs to 1 (what completing an item does).
  python3 - <<'PYEOF'
import json, pathlib
p = pathlib.Path(".rota/counters.json")
d = json.loads(p.read_text())
d["since_refactor"]["bugs"] = 1
p.write_text(json.dumps(d, indent=2) + "\n")
PYEOF

  RC=0; OUT=$(hvj item reopen B01 2>/dev/null) || RC=$?
  [ "$RC" = "0" ] || { echo "FAIL F24(a): expected exit 0, got $RC"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "true" ] || { echo "FAIL F24(a): changed should be true: $OUT"; exit 1; }

  # Active line restored under ## Bugs.
  grep -qE '^- \*\*\[B01\] \[P1\] Sample bug\.\*\* Body\.$' .rota/BACKLOG.md \
    || { echo "FAIL F24(a): [B01] active line not restored"; exit 1; }
  # ## Completed no longer references B01.
  if awk '/^## Completed/{f=1;next} /^## /{f=0} f' .rota/BACKLOG.md | grep '\[B01\]' >/dev/null; then
    echo "FAIL F24(a): [B01] still present under ## Completed"; exit 1
  fi
  # since_refactor.bugs decremented 1 -> 0.
  NEW=$(python3 -c "import json; print(json.load(open('.rota/counters.json'))['since_refactor']['bugs'])")
  [ "$NEW" = "0" ] || { echo "FAIL F24(a): since_refactor.bugs expected 0, got $NEW"; exit 1; }
) || exit 1

# ── (b) item reopen: restore from .rota/ARCHIVE.md ───────────────────────────
(
  cd "$UNDO_TMP"
  build_undo_fixture "$UNDO_TMP"
  # Clear the staged Feature so we can prove the active line is appended, not pre-existing.
  python3 - <<'PYEOF'
import pathlib
p = pathlib.Path(".rota/BACKLOG.md")
text = p.read_text()
text = text.replace("- **[F03] [Minor] Sample feature.** Body.\n", "")
p.write_text(text)
PYEOF
  cat > .rota/ARCHIVE.md <<'ARCHEOF'
# Archive

## Features
- ~~**[F02] [Minor] Feature title.** Detail.~~ Done 2026-01-10 [`def5678`]
ARCHEOF

  RC=0; hvj item reopen F02 >/dev/null 2>&1 || RC=$?
  [ "$RC" = "0" ] || { echo "FAIL F24(b): expected exit 0, got $RC"; exit 1; }

  grep -qE '^- \*\*\[F02\] \[Minor\] Feature title\.\*\* Detail\.$' .rota/BACKLOG.md \
    || { echo "FAIL F24(b): [F02] active line not restored under ## Features"; exit 1; }
  grep -q '\[F02\]' .rota/ARCHIVE.md \
    && { echo "FAIL F24(b): [F02] still present in ARCHIVE.md"; exit 1; } \
    || true
) || exit 1

# ── (c) item reopen idempotent no-op: active already, no writes ────────────
(
  cd "$UNDO_TMP"
  build_undo_fixture "$UNDO_TMP"
  # Add B07 as already-active so uncomplete is a no-op.
  python3 - <<'PYEOF'
import pathlib
p = pathlib.Path(".rota/BACKLOG.md")
text = p.read_text()
text = text.replace("## Bugs\n- **[B01]", "## Bugs\n- **[B07] [P2] Already active.**\n- **[B01]")
p.write_text(text)
PYEOF
  BL_BEFORE=$(cat .rota/BACKLOG.md)
  CT_BEFORE=$(cat .rota/counters.json)

  RC=0; OUT=$(hvj item reopen B07 2>/dev/null) || RC=$?
  [ "$RC" = "0" ] || { echo "FAIL F24(c): expected exit 0, got $RC"; exit 1; }

  [ "$BL_BEFORE" = "$(cat .rota/BACKLOG.md)" ] \
    || { echo "FAIL F24(c): BACKLOG.md changed on no-op restore"; exit 1; }
  [ "$CT_BEFORE" = "$(cat .rota/counters.json)" ] \
    || { echo "FAIL F24(c): counters.json changed on no-op restore"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
    || { echo "FAIL F24(c): no-op restore should report changed=false: $OUT"; exit 1; }
) || exit 1

# ── (d) item reopen: ID not found anywhere → exit 3 ──────────────────────────
(
  cd "$UNDO_TMP"
  build_undo_fixture "$UNDO_TMP"
  RC=0; hvj item reopen B99 >/dev/null 2>&1 || RC=$?
  [ "$RC" = "3" ] || { echo "FAIL F24(d): expected exit 3, got $RC"; exit 1; }
) || exit 1

# ── (e) item reopen: refactor commit subject → counter NOT decremented ────
(
  cd "$UNDO_TMP"
  build_undo_fixture "$UNDO_TMP"
  # Real refactor commit so the helper's git lookup succeeds.
  echo refac > refac.txt
  git add refac.txt
  git commit -q -m "refactor: simplify foo"
  SHORT=$(git rev-parse --short HEAD)
  # Seed Completed with F08 (NOT pre-staged in fixture) referencing the refactor short hash.
  python3 - "$SHORT" <<'PYEOF'
import sys, pathlib
short = sys.argv[1]
p = pathlib.Path(".rota/BACKLOG.md")
text = p.read_text()
done = f"- ~~**[F08] [Minor] Refactor feature.** Body.~~ Done 2026-01-15 [`{short}`]\n"
text = text.replace("## Completed\n", "## Completed\n" + done)
p.write_text(text)
PYEOF
  # Ensure since_refactor.features starts at 0 (default — refactor commits do
  # not bump on the way in, so the inverse path must skip the decrement, not
  # take features negative).
  python3 - <<'PYEOF'
import json, pathlib
p = pathlib.Path(".rota/counters.json")
d = json.loads(p.read_text())
d["since_refactor"]["features"] = 0
p.write_text(json.dumps(d, indent=2) + "\n")
PYEOF

  RC=0; hvj item reopen F08 >/dev/null 2>&1 || RC=$?
  [ "$RC" = "0" ] || { echo "FAIL F24(e): expected exit 0, got $RC"; exit 1; }
  NEW=$(python3 -c "import json; print(json.load(open('.rota/counters.json'))['since_refactor']['features'])")
  [ "$NEW" = "0" ] \
    || { echo "FAIL F24(e): since_refactor.features expected 0 (decrement skipped on refactor:), got $NEW"; exit 1; }
) || exit 1

# ── (f) ship undo preview: plan printed, BACKLOG + HEAD untouched ──────────────
(
  cd "$UNDO_TMP"
  build_undo_cycle "$UNDO_TMP"
  MERGE_SHORT=$(git rev-parse --short HEAD)
  BL_BEFORE=$(cat .rota/BACKLOG.md)
  HEAD_BEFORE=$(git rev-parse HEAD)

  RC=0; OUT=$(hvj ship undo 2>/dev/null) || RC=$?
  [ "$RC" = "0" ] || { echo "FAIL F24(f): preview expected exit 0, got $RC"; exit 1; }

  [ "$(echo "$OUT" | jget data.applied)" = "false" ] \
    || { echo "FAIL F24(f): preview should report applied=false: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
    || { echo "FAIL F24(f): preview should report changed=false: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.cycle)" = "$MERGE_SHORT" ] \
    || { echo "FAIL F24(f): cycle should be merge short hash $MERGE_SHORT: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.items)" = '["F03"]' ] \
    || { echo "FAIL F24(f): items should be [F03]: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.base)" = "main" ] \
    || { echo "FAIL F24(f): base should be main: $OUT"; exit 1; }
  echo "$OUT" | jget data.subject | grep '^merge: F03' >/dev/null \
    || { echo "FAIL F24(f): subject should start 'merge: F03': $OUT"; exit 1; }
  echo "$OUT" | jget warnings | grep 'pass --apply' >/dev/null \
    || { echo "FAIL F24(f): preview should warn 'pass --apply': $OUT"; exit 1; }

  [ "$BL_BEFORE" = "$(cat .rota/BACKLOG.md)" ] \
    || { echo "FAIL F24(f): dry-run modified BACKLOG.md"; exit 1; }
  [ "$(git rev-parse HEAD)" = "$HEAD_BEFORE" ] \
    || { echo "FAIL F24(f): dry-run moved HEAD"; exit 1; }
) || exit 1

# ── (f2) ship undo --cycle: unknown hash → exit 3, non-merge commit → exit 4 ──
(
  cd "$UNDO_TMP"
  build_undo_cycle "$UNDO_TMP"
  RC=0; hvj ship undo --cycle deadbeef >/dev/null 2>&1 || RC=$?
  [ "$RC" = "3" ] || { echo "FAIL F24(f2): unknown --cycle expected exit 3, got $RC"; exit 1; }
  RC=0; hvj ship undo --cycle "$(git rev-list --max-parents=0 HEAD)" >/dev/null 2>&1 || RC=$?
  [ "$RC" = "4" ] || { echo "FAIL F24(f2): non-merge --cycle expected exit 4, got $RC"; exit 1; }
  OUT=$(hvj ship undo --cycle "$(git rev-parse HEAD)" 2>/dev/null) || { echo "FAIL F24(f2): --cycle HEAD preview failed"; exit 1; }
  [ "$(echo "$OUT" | jget data.cycle)" = "$(git rev-parse --short HEAD)" ] \
    || { echo "FAIL F24(f2): --cycle preview should name the merge: $OUT"; exit 1; }
) || exit 1

# ── (g) ship undo: orphan HEAD (no parents) → exit 4 ─────────────────────────
# The fixture builds a single seed commit (no parents). The HEAD-shape check
# catches this before the merge lookup, so it is "HEAD is not a merge" (4),
# not "no cycle to undo" (3).
(
  cd "$UNDO_TMP"
  build_undo_fixture "$UNDO_TMP"
  RC=0; OUT=$(hvj ship undo 2>/dev/null) || RC=$?
  [ "$RC" = "4" ] || { echo "FAIL F24(g): expected exit 4, got $RC"; exit 1; }
  [ "$(echo "$OUT" | jget data.blockedBy)" = "not a merge" ] \
    || { echo "FAIL F24(g): blockedBy should be 'not a merge': $OUT"; exit 1; }
) || exit 1

# ── (h) ship undo: dirty tree → exit 4 ───────────────────────────────────────
(
  cd "$UNDO_TMP"
  build_undo_cycle "$UNDO_TMP"
  echo dirty >> seed.txt
  RC=0; OUT=$(hvj ship undo 2>/dev/null) || RC=$?
  [ "$RC" = "4" ] || { echo "FAIL F24(h): dirty tree expected exit 4, got $RC"; exit 1; }
  [ "$(echo "$OUT" | jget data.blockedBy)" = "dirty tree" ] \
    || { echo "FAIL F24(h): blockedBy should be 'dirty tree': $OUT"; exit 1; }
) || exit 1

# ── (i) ship undo --apply round-trip: HEAD rolled back, BACKLOG restored ──────
# ── (j) ship undo after apply: HEAD is no merge → exit 4 ─────────────────────
(
  cd "$UNDO_TMP"
  build_undo_cycle "$UNDO_TMP"
  BEFORE_HEAD=$(git rev-parse HEAD)
  EXPECTED_HEAD=$(git rev-parse HEAD^1)

  RC=0; OUT=$(hvj ship undo --apply 2>/dev/null) || RC=$?
  [ "$RC" = "0" ] || { echo "FAIL F24(i): --apply expected exit 0, got $RC"; exit 1; }
  [ "$(echo "$OUT" | jget data.applied)" = "true" ] && [ "$(echo "$OUT" | jget data.changed)" = "true" ] \
    || { echo "FAIL F24(i): --apply should report applied and changed: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.restored)" = '["F03"]' ] \
    || { echo "FAIL F24(i): restored should be [F03]: $OUT"; exit 1; }
  [ "$(git rev-parse --short "$EXPECTED_HEAD")" = "$(echo "$OUT" | jget data.restoredTo)" ] \
    || { echo "FAIL F24(i): restoredTo should be the pre-merge short hash: $OUT"; exit 1; }

  AFTER=$(git rev-parse HEAD)
  [ "$AFTER" = "$EXPECTED_HEAD" ] \
    || { echo "FAIL F24(i): HEAD expected $EXPECTED_HEAD (pre-merge), got $AFTER (was $BEFORE_HEAD)"; exit 1; }

  grep -qE '^- \*\*\[F03\]' .rota/BACKLOG.md \
    || { echo "FAIL F24(i): [F03] active line not restored under ## Features"; exit 1; }
  if awk '/^## Completed/{f=1;next} /^## /{f=0} f' .rota/BACKLOG.md | grep '\[F03\]' >/dev/null; then
    echo "FAIL F24(i): [F03] still present under ## Completed after rollback"; exit 1
  fi

  # (j) Re-run on the now-rolled-back tree: HEAD is a 1-parent commit (seed
  # parent), so the HEAD-shape check refuses ("HEAD is not a merge") before
  # the merge lookup.
  RC=0; OUT=$(hvj ship undo 2>/dev/null) || RC=$?
  [ "$RC" = "4" ] || { echo "FAIL F24(j): post-apply expected exit 4, got $RC"; exit 1; }
  [ "$(echo "$OUT" | jget data.blockedBy)" = "not a merge" ] \
    || { echo "FAIL F24(j): blockedBy should be 'not a merge': $OUT"; exit 1; }
) || exit 1

# ── (k) ship undo: non-`merge:` subject refused → exit 4 ─────────────────────
(
  cd "$UNDO_TMP"
  build_undo_fixture "$UNDO_TMP"
  git checkout -q -b rota/F99-other
  echo other > other.txt
  git add other.txt
  git commit -q -m "feat: other branch"
  git checkout -q main
  # GitHub-style merge commit subject — NOT 'merge: '.
  git merge --no-ff rota/F99-other -q -m "Merge pull request #1 from foo"
  git branch -q -d rota/F99-other

  RC=0; OUT=$(hvj ship undo 2>/dev/null) || RC=$?
  [ "$RC" = "4" ] || { echo "FAIL F24(k): non-hv merge expected exit 4, got $RC"; exit 1; }
  [ "$(echo "$OUT" | jget data.blockedBy)" = "merge subject" ] \
    || { echo "FAIL F24(k): blockedBy should be 'merge subject': $OUT"; exit 1; }
) || exit 1

# ── (l) ship undo --apply: restore fails after the reset → exit 5 saying so ──
# F03 exists only in the merge commit's Completed line, so after the reset to
# the pre-merge commit there is nothing for the restore to reopen.
(
  cd "$UNDO_TMP"
  build_undo_fixture "$UNDO_TMP"
  python3 -c 'import pathlib; p = pathlib.Path(".rota/BACKLOG.md"); p.write_text("".join(l for l in p.read_text().splitlines(True) if "[F03]" not in l))'
  git commit -q -am "drop F03 from the base"
  git checkout -q -b rota/F03-test
  echo impl > impl.txt && git add impl.txt && git commit -q -m "feat: implement F03"
  SHORT=$(git rev-parse --short HEAD)
  git checkout -q main
  git merge --no-ff rota/F03-test -q -m "merge: F03 — test cycle"
  git branch -q -d rota/F03-test
  python3 - "$SHORT" <<'PYEOF'
import sys, pathlib
p = pathlib.Path(".rota/BACKLOG.md")
done = f"- ~~**[F03] [Minor] Sample feature.** Body.~~ Done 2026-01-15 [`{sys.argv[1]}`]\n"
p.write_text(p.read_text().replace("## Completed\n", "## Completed\n" + done))
PYEOF
  git add .rota/BACKLOG.md && git commit -q --amend --no-edit
  EXPECTED_HEAD=$(git rev-parse HEAD^1)

  RC=0; OUT=$(hvj ship undo --apply 2>/dev/null) || RC=$?
  [ "$RC" = "5" ] || { echo "FAIL F24(l): failed restore expected exit 5, got $RC: $OUT"; exit 1; }
  echo "$OUT" | jget error.message | grep "reset already happened" >/dev/null \
    || { echo "FAIL F24(l): exit 5 message must say the reset already happened: $OUT"; exit 1; }
  [ "$(git rev-parse HEAD)" = "$EXPECTED_HEAD" ] \
    || { echo "FAIL F24(l): the reset should have happened before the restore failed"; exit 1; }
) || exit 1

trap 'rm -rf "$TMP"' EXIT
rm -rf "$UNDO_TMP"
pass "F24 item reopen + ship undo smoke"
