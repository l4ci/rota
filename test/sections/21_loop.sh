echo "F32: loop-mode auto-planning helpers"

# (f) status loop: start writes ISO timestamp; idempotent first-write; clear removes; show is null when unset.
F32_TMP="$(mktemp -d)"
trap 'rm -rf "$F32_TMP"' EXIT
(
  cd "$F32_TMP" && mkdir .rota
  echo '{"active": []}' > .rota/status.json
  OUT=$(hvj status loop show)
  [ "$(jget data.loopStartedAt <<<"$OUT")" = "null" ] \
    || fail "F32(f): status loop show on unset must be null, got '$OUT'"
  OUT=$(hvj status loop start)
  T1=$(jget data.loopStartedAt <<<"$OUT")
  grep -qE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' <<<"$T1" \
    || fail "F32(f): status loop start must write ISO timestamp, got '$T1'"
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "F32(f): first start must report changed: $OUT"
  [ "$(jget data.loopStartedAt <<<"$(hvj status loop show)")" = "$T1" ] \
    || fail "F32(f): status loop show must return the stamp start wrote"
  # idempotent first-write: a second start must not overwrite
  sleep 1
  OUT=$(hvj status loop start)
  [ "$(jget data.loopStartedAt <<<"$OUT")" = "$T1" ] \
    || fail "F32(f): status loop start must be idempotent first-write (T1='$T1' got: $OUT)"
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "F32(f): repeat start must report changed=false: $OUT"
  # active array preserved
  python3 -c 'import json; d = json.load(open(".rota/status.json")); assert d["active"] == [] and d["loopStartedAt"]' \
    || fail "F32(f): status loop must preserve the active array"
  # clear removes
  OUT=$(hvj status loop clear)
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "F32(f): clear of a set stamp must report changed: $OUT"
  [ "$(jget data.loopStartedAt <<<"$(hvj status loop show)")" = "null" ] \
    || fail "F32(f): status loop clear must remove loopStartedAt"
  [ "$(jget data.changed <<<"$(hvj status loop clear)")" = "false" ] \
    || fail "F32(f): clear of an unset stamp must report changed=false"
)
trap 'rm -rf "$TMP"' EXIT
rm -rf "$F32_TMP"
pass "F32(f): status loop start/clear/show"

# (g) decisions auto-log: writes placeholder template + footer; idempotent on (topic, rule-title).
F32_TMP="$(mktemp -d)"
trap 'rm -rf "$F32_TMP"' EXIT
(
  cd "$F32_TMP" && mkdir .rota
  echo "# Decisions" > .rota/DECISIONS.md
  echo "" >> .rota/DECISIONS.md
  OUT=$(hvj decisions auto-log --topic "Test Topic" --title "Test rule" --why "Because reasons" \
    --plan-key "M04-F32" --date "2026-05-09")
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "F32(g): first auto-log must report changed: $OUT"
  [ "$(jget data.topic <<<"$OUT")" = "Test Topic" ] || fail "F32(g): data.topic missing: $OUT"
  [ "$(jget data.title <<<"$OUT")" = "Test rule" ] || fail "F32(g): data.title missing: $OUT"
  grep -q '## Test Topic' .rota/DECISIONS.md \
    || fail "F32(g): topic header missing"
  grep -q '### Test rule' .rota/DECISIONS.md \
    || fail "F32(g): rule heading missing"
  grep -q '_(Unresolved — user must articulate)_' .rota/DECISIONS.md \
    || fail "F32(g): placeholder Forbids/Permits missing"
  grep -q '\[Auto:Loop\] M04-F32 2026-05-09' .rota/DECISIONS.md \
    || fail "F32(g): provenance footer missing or malformed"
  # idempotent — second run must not duplicate the entry
  OUT=$(hvj decisions auto-log --topic "Test Topic" --title "Test rule" --why "Because reasons" \
    --plan-key "M04-F32" --date "2026-05-09")
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "F32(g): repeat auto-log must report changed=false: $OUT"
  COUNT=$(grep -c '### Test rule' .rota/DECISIONS.md)
  [ "$COUNT" = "1" ] || fail "F32(g): decisions auto-log must be idempotent on (topic, rule-title), got $COUNT entries"
  # required flags: missing --why is a usage error
  rc=0; hvj decisions auto-log --topic "T" --title "R" >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || fail "F32(g): auto-log without --why must exit 2, got $rc"
)
trap 'rm -rf "$TMP"' EXIT
rm -rf "$F32_TMP"
pass "F32(g): decisions auto-log placeholder template + idempotent"

# (h) decisions auto-since: filters by loopStartedAt date; empty when no loop.
F32_TMP="$(mktemp -d)"
trap 'rm -rf "$F32_TMP"' EXIT
(
  cd "$F32_TMP" && mkdir .rota
  cat > .rota/status.json <<'EOFJ'
{"active": [], "loopStartedAt": "2026-05-09T00:00:00Z"}
EOFJ
  cat > .rota/DECISIONS.md <<'EOFD'
# Decisions

## Topic A

### Pre-loop rule

*Why.* Decided yesterday.

**Forbids.**
- Specific thing.

**Permits.**
- Other thing.

<!-- [Auto:Loop] M04-F32 2026-05-08 — review and articulate Forbids/Permits -->

### In-loop rule

*Why.* Decided today.

**Forbids.**
- _(Unresolved — user must articulate)_

**Permits.**
- _(Unresolved — user must articulate)_

<!-- [Auto:Loop] M04-F32 2026-05-09 — review and articulate Forbids/Permits -->
EOFD
  OUT=$(hvj decisions auto-since)
  [ "$(jget data.since <<<"$OUT")" = "2026-05-09T00:00:00Z" ] || fail "F32(h): data.since missing: $OUT"
  [ "$(jget data.decisions <<<"$OUT" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')" = "1" ] \
    || fail "F32(h): only the post-loopStart entry must remain: $OUT"
  [ "$(jget 'data.decisions[0].title' <<<"$OUT")" = "In-loop rule" ] \
    || fail "F32(h): post-loopStart entry missing from output: $OUT"
  [ "$(jget 'data.decisions[0].topic' <<<"$OUT")" = "Topic A" ] || fail "F32(h): topic wrong: $OUT"
  [ "$(jget 'data.decisions[0].date' <<<"$OUT")" = "2026-05-09" ] || fail "F32(h): date wrong: $OUT"
  [ "$(jget 'data.decisions[0].status' <<<"$OUT")" = "unresolved" ] \
    || fail "F32(h): unresolved status missing: $OUT"
  # no loop: decisions is empty and since is absent
  echo '{"active": []}' > .rota/status.json
  OUT=$(hvj decisions auto-since)
  [ "$(jget data.decisions <<<"$OUT")" = "[]" ] || fail "F32(h): empty when loopStartedAt unset, got '$OUT'"
  jget data.since <<<"$OUT" >/dev/null && fail "F32(h): since must be absent without a loop: $OUT"
  true
)
trap 'rm -rf "$TMP"' EXIT
rm -rf "$F32_TMP"
pass "F32(h): decisions auto-since filter + lookup-empty"

# --- map frontmatter & map entries -------------------
mkdir -p .rota/map
cat > .rota/map/capture.md <<'EOF'
---
subsystem: capture
summary: Captures items into BACKLOG.md
touched: 2026-05-09
related-topics: [Skill Authoring]
---

## Purpose
One paragraph.
EOF
cat > .rota/map/plan.md <<'EOF'
---
subsystem: plan
summary: Plans before execution
touched: 2026-04-01
---
body
EOF
# malformed: no frontmatter
echo "no frontmatter here" > .rota/map/broken.md

# --- map query -----------------------------------------------------
out="$(hvj map query capture | jget data.text)"
[[ "$out" == *"## Purpose"* ]] || { echo "FAIL: map query body missing"; exit 1; }
out="$(hvj map query capture plan | jget data.text)"
[[ "$out" == *"## Purpose"* && "$out" == *"body"* ]] || { echo "FAIL: map query multi"; exit 1; }
out="$(hvj map query nonexistent | jget data.text)"
[[ -z "$out" ]] || { echo "FAIL: map query missing should be empty, got: $out"; exit 1; }
rc=0; hvj map query >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || { echo "FAIL: map query with no name must exit 2, got $rc"; exit 1; }
echo "ok map query"

# --- map stats -----------------------------------------------------
# Add an entry-point referencing this very file to test the file:line check
mkdir -p src
echo "line1" > src/sample.txt
echo "line2" >> src/sample.txt
cat > .rota/map/work.md <<'EOF'
---
subsystem: work
summary: Orchestrator-driven execution
touched: 2026-05-09
---

## Entry points
- src/sample.txt:2 — second line
- src/missing.txt:42 — broken ref
EOF
hvj map stats | python3 -c '
import json, sys
data = json.load(sys.stdin)["data"]
names = [s["name"] for s in data["subsystems"]]
assert "capture" in names, names
assert data["count"] == len(data["subsystems"]), data
work = next(s for s in data["subsystems"] if s["name"] == "work")
# work has 1 broken ref out of 2 entry points
assert work["brokenRefs"] == 1, work
assert work["entryPoints"] == 2, work
assert work["touched"] == "2026-05-09", work
assert "cap" not in data, data
' || { echo "FAIL: map stats shape"; exit 1; }
# --cap adds the advisory fields and never fails
hvj map stats --cap | python3 -c '
import json, sys
data = json.load(sys.stdin)["data"]
assert data["cap"] == 20 and data["overCap"] is False, data
' || { echo "FAIL: map stats --cap shape"; exit 1; }
echo "ok map stats"

# --- map index -----------------------------------------------------
[ -f CLAUDE.md ] || : > CLAUDE.md
OUT="$(hvj map index)"
[ "$(jget data.key <<<"$OUT")" = "map" ] || { echo "FAIL: map index data.key: $OUT"; exit 1; }
[ "$(jget data.changed <<<"$OUT")" = "true" ] || { echo "FAIL: first map index must report changed: $OUT"; exit 1; }
grep -q '<!-- rota-map-start -->' CLAUDE.md || { echo "FAIL: map block not in CLAUDE.md"; exit 1; }
grep -q '## Project Map' CLAUDE.md || { echo "FAIL: heading missing"; exit 1; }
grep -q '\*\*capture\*\* — Captures items into BACKLOG.md' CLAUDE.md || { echo "FAIL: capture summary missing"; exit 1; }
# Idempotence
sha1=$(sha1sum CLAUDE.md | cut -d' ' -f1)
OUT="$(hvj map index)"
sha2=$(sha1sum CLAUDE.md | cut -d' ' -f1)
[ "$sha1" = "$sha2" ] || { echo "FAIL: map index not idempotent"; exit 1; }
[ "$(jget data.status <<<"$OUT")" = "unchanged" ] || { echo "FAIL: repeat map index must be unchanged: $OUT"; exit 1; }
[ "$(jget data.changed <<<"$OUT")" = "false" ] || { echo "FAIL: repeat map index must report changed=false: $OUT"; exit 1; }
# Empty case: hide the block when .rota/map/ has no valid entries
mv .rota/map .rota/map.bak
mkdir .rota/map
hvj map index >/dev/null
grep -q '_(no subsystems yet' CLAUDE.md || { echo "FAIL: empty placeholder missing"; exit 1; }
mv .rota/map .rota/map.empty
mv .rota/map.bak .rota/map
echo "ok map index"

# --- backlog stale -------------------------------------------------
# Plan (touched 2026-04-01) is older than 30 days from "today=2026-05-09";
# work is touched 2026-05-09 and should not be flagged at days=30.
out="$(ROTA_TEST_TODAY=2026-05-09 hvj backlog stale --kind map --days 30 | jget data.entries)"
grep -q '"name":"plan"' <<<"$out" || { echo "FAIL: plan should be stale"; exit 1; }
if grep -q '"name":"work"' <<<"$out"; then echo "FAIL: work should NOT be stale"; exit 1; fi
# days=0 lists all
ROTA_TEST_TODAY=2026-05-09 hvj backlog stale --kind map --days 0 | jget 'data.entries[1].name' >/dev/null \
  || { echo "FAIL: days=0 should list all"; exit 1; }
# Nothing is stale when the window is huge (silence, not an empty report)
out="$(ROTA_TEST_TODAY=2026-05-09 hvj backlog stale --kind map --days 999999 | jget data.entries)"
[ "$out" = "[]" ] || { echo "FAIL: backlog stale should be empty when nothing is stale (got: $out)"; exit 1; }
# Knowledge: KNOWLEDGE.md exists from bootstrap-style fixture; should not error
hvj backlog stale --kind knowledge --days 0 >/dev/null
echo "ok backlog stale"

# --- init seeds map ------------------------------------------------
TMP2=$(mktemp -d)
trap 'rm -rf "$TMP" "$TMP2"' EXIT
(
  cd "$TMP2" && git init -q
  "$ROTA_BIN" init >/dev/null
  [ -d .rota/map ] || { echo "FAIL: .rota/map not created"; exit 1; }
  [ -f .rota/MAP.md ] || { echo "FAIL: .rota/MAP.md not seeded"; exit 1; }
  grep -q "Project map" .rota/MAP.md || { echo "FAIL: .rota/MAP.md content missing"; exit 1; }
)
echo "ok init seeds map"

# --- end-to-end: scaffold + after-work bump + consolidate prep ----
TMP3=$(mktemp -d)
trap 'rm -rf "$TMP3" "$TMP" "$TMP2"' EXIT
(
  cd "$TMP3" && git init -q
  git config user.email test@example.com
  git config user.name Test
  "$ROTA_BIN" init >/dev/null
  : > CLAUDE.md
  cat > .rota/map/capture.md <<'EOF'
---
subsystem: capture
summary: Captures items into BACKLOG.md
touched: 2026-05-09
created: 2026-05-09
---

## Purpose
Capture flow.

## Entry points
- scripts/bootstrap:1 — broken ref (file does not exist in fixture)
EOF
  cat > .rota/map/work.md <<'EOF'
---
subsystem: work
summary: Captures items into BACKLOG.md  # near-duplicate summary
touched: 2025-12-01
created: 2025-12-01
---

## Purpose
Work flow.
EOF
  hvj map index >/dev/null

  python3 - <<'PY'
from pathlib import Path
p = Path(".rota/map/capture.md")
text = p.read_text().replace("touched: 2026-05-09", "touched: 2026-05-10")
p.write_text(text)
PY
  grep -q "touched: 2026-05-10" .rota/map/capture.md || { echo "FAIL: after-work bump"; exit 1; }

  out="$(ROTA_TEST_TODAY=2026-05-10 hvj backlog stale --kind map --days 30 | jget data.entries)"
  grep -q '"name":"work"' <<<"$out" || { echo "FAIL: work should be stale at days=30"; exit 1; }

  count=$(hvj map stats | jget data.count)
  [ "$count" = "2" ] || { echo "FAIL: stats count $count != 2"; exit 1; }

  hvj map index >/dev/null
  sha1=$(sha1sum CLAUDE.md | cut -d' ' -f1)
  hvj map index >/dev/null
  sha2=$(sha1sum CLAUDE.md | cut -d' ' -f1)
  [ "$sha1" = "$sha2" ] || { echo "FAIL: integration idempotence"; exit 1; }
)
trap 'rm -rf "$TMP"' EXIT
rm -rf "$TMP2" "$TMP3"
echo "ok end-to-end map flow"

# --- parse_todo_fields handles Subsystem ---------------------------
echo "B28: /rota-brainstorm --auto-loop dispatch chain"
