echo "## plan validate-docs (F76 — doc-by-path mismatch check)"

PVD_TMP="$(mktemp -d)"
trap 'rm -rf "$PVD_TMP"' EXIT
(
  cd "$PVD_TMP"
  mkdir -p .rota/plans
  echo '{"active":[]}' > .rota/status.json
  echo '{"docs":{"path":"docs"}}' > .rota/config.json

  # 1. Single-repo: doc home missing → warns, exit 0
  cat > .rota/plans/M01-B07.md <<'PLAN'
---
key: M01-B07
milestone: M01
unit: B07
unitKind: item
title: t
status: planned
created: 2026-05-20
---

## Tasks

- **T1** — drop doc
  - Files: src/x.ts, docs/api/auth.md
  - Verify: build
PLAN
  OUT=$(hvj plan validate-docs M01-B07) || { echo "FAIL: exit non-zero"; exit 1; }
  [ "$(jget data.valid <<<"$OUT")" = "false" ] || { echo "FAIL: expected valid false: $OUT"; exit 1; }
  [ "$(jget 'data.mismatches[0].path' <<<"$OUT")" = "docs/api/auth.md" ] || { echo "FAIL: missing doc path in mismatches: $OUT"; exit 1; }
  FIELD=$(jget 'data.mismatches[0].issue' <<<"$OUT")
  grep -q "expected doc home" <<<"$FIELD" || { echo "FAIL: missing 'expected doc home' phrase: $OUT"; exit 1; }
  # Non-doc paths are silent.
  [ "$(jget data.mismatches <<<"$OUT" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')" = "1" ] || { echo "FAIL: non-doc path leaked into mismatches: $OUT"; exit 1; }
  pass "T1: single-repo plan with missing doc home → warning"

  # 2. Single-repo: doc home exists → silent exit 0
  mkdir -p docs/api
  OUT=$(hvj plan validate-docs M01-B07) || { echo "FAIL: exit non-zero with docs/ present"; exit 1; }
  [ "$(jget data.valid <<<"$OUT")" = "true" ] || { echo "FAIL: expected valid true, got: $OUT"; exit 1; }
  [ "$(jget data.mismatches <<<"$OUT")" = "[]" ] || { echo "FAIL: expected no mismatches, got: $OUT"; exit 1; }
  pass "T2: single-repo plan with existing doc home → silent"

  # 3. Indented Files: bullet form is parsed
  rm -rf "$PVD_TMP/docs"
  cat > .rota/plans/M01-B08.md <<'PLAN'
---
key: M01-B08
milestone: M01
unit: B08
unitKind: item
title: t
status: planned
created: 2026-05-20
---

## Tasks

- **T1** — indented form
  - Files:
    - src/web/index.ts
    - docs/howto/run.md
  - Verify: open
PLAN
  OUT=$(hvj plan validate-docs M01-B08) || { echo "FAIL: exit non-zero on indented form"; exit 1; }
  [ "$(jget 'data.mismatches[0].path' <<<"$OUT")" = "docs/howto/run.md" ] || { echo "FAIL: indented Files: bullet not parsed: $OUT"; exit 1; }
  pass "T3: indented 'Files:' bullet form is parsed"

  # 4. Stub placeholder is ignored (no false positive)
  cat > .rota/plans/M01-B09.md <<'PLAN'
---
key: M01-B09
milestone: M01
unit: B09
unitKind: item
title: t
status: planned
created: 2026-05-20
---

## Tasks

- **T1** — unfilled stub
  - Files: _(paths the orchestrator will touch or create)_
  - Verify: _(command or manual check that proves T1 done)_
PLAN
  OUT=$(hvj plan validate-docs M01-B09) || { echo "FAIL: exit non-zero on stub plan"; exit 1; }
  [ "$(jget data.valid <<<"$OUT")" = "true" ] || { echo "FAIL: stub placeholder triggered a false positive: $OUT"; exit 1; }
  pass "T4: stub '_(...)_' Files: placeholder ignored"

  # 5. Missing plan → exit 3 (resolution); a malformed key → exit 2
  rc=0; hvj plan validate-docs M09-B99 >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: missing plan should exit 3, got $rc"; exit 1; }
  rc=0; hvj plan validate-docs NOPE-X >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: malformed key should exit 2, got $rc"; exit 1; }
  pass "T5: missing plan exits 3, malformed key exits 2"
)
trap 'rm -rf "$TMP"' EXIT
rm -rf "$PVD_TMP"
pass "## plan validate-docs single-repo cases"

# Umbrella mode: sibling <repo>-docs suggestion
PVDU_TMP="$(mktemp -d)"
trap 'rm -rf "$PVDU_TMP"' EXIT
(
  cd "$PVDU_TMP"
  mkdir -p .rota/plans runlog runlog-docs
  echo '{"active":[]}' > .rota/status.json
  echo '{"docs":{"path":"docs"}}' > .rota/config.json
  cat > .rota/repos.json <<EOF
{"repos":[{"name":"runlog","path":"runlog"},{"name":"runlog-docs","path":"runlog-docs"}]}
EOF
  (cd runlog && git init -q && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m init)
  (cd runlog-docs && git init -q && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m init)

  # Plan tagged with sub-repo that has no docs/, sibling -docs is registered
  cat > .rota/plans/M01-B07.md <<'PLAN'
---
key: M01-B07
milestone: M01
unit: B07
unitKind: item
repo: runlog
title: t
status: planned
created: 2026-05-20
---

## Tasks

- **T1** — cross-repo doc
  - Files: docs/NN-architecture.md, src/api.ts
  - Verify: tests
PLAN
  OUT=$(hvj plan validate-docs M01-B07) || { echo "FAIL: exit non-zero"; exit 1; }
  [ "$(jget 'data.mismatches[0].targetRepo' <<<"$OUT")" = "runlog" ] || { echo "FAIL: missing targetRepo runlog: $OUT"; exit 1; }
  FIELD=$(jget 'data.mismatches[0].suggestion' <<<"$OUT")
  grep -q "sibling sub-repo 'runlog-docs'" <<<"$FIELD" || {
    echo "FAIL: missing sibling -docs suggestion: $OUT"; exit 1;
  }
  pass "U1: umbrella sub-repo missing docs/ → suggests sibling <repo>-docs"

  # Umbrella-relative form (path begins with sub-repo name) is normalized.
  cat > .rota/plans/M01-B08.md <<'PLAN'
---
key: M01-B08
milestone: M01
unit: B08
unitKind: item
repo: runlog
title: t
status: planned
created: 2026-05-20
---

## Tasks

- **T1** — umbrella-relative path form
  - Files: runlog/docs/NN-compliance.md
  - Verify: tests
PLAN
  OUT=$(hvj plan validate-docs M01-B08)
  [ "$(jget 'data.mismatches[0].path' <<<"$OUT")" = "runlog/docs/NN-compliance.md" ] || {
    echo "FAIL: umbrella-relative path not recognized: $OUT"; exit 1;
  }
  FIELD=$(jget 'data.mismatches[0].suggestion' <<<"$OUT")
  grep -q "sibling sub-repo 'runlog-docs'" <<<"$FIELD" || {
    echo "FAIL: sibling suggestion missing on umbrella-relative form"; exit 1;
  }
  pass "U2: umbrella-relative path '<repo>/docs/x.md' is normalized and flagged"

  # Multi-repo plan: validates against each named repo
  mkdir -p runlog-docs/docs
  cat > .rota/plans/M01-F09.md <<'PLAN'
---
key: M01-F09
milestone: M01
unit: F09
unitKind: item
repo: runlog, runlog-docs
title: t
status: planned
created: 2026-05-20
---

## Tasks

- **T1** — multi-repo touch
  - Files: docs/x.md
  - Verify: build
PLAN
  OUT=$(hvj plan validate-docs M01-F09)
  [ "$(jget 'data.mismatches[0].targetRepo' <<<"$OUT")" = "runlog" ] || {
    echo "FAIL: should warn runlog (no docs/): $OUT"; exit 1;
  }
  if [ -n "$(jget 'data.mismatches[1]' <<<"$OUT" 2>/dev/null)" ]; then
    echo "FAIL: should NOT warn runlog-docs (has docs/): $OUT"; exit 1;
  fi
  pass "U3: multi-repo plan validates each repo independently"

  # Unregistered repo tag → issue-level warning
  cat > .rota/plans/M01-T01.md <<'PLAN'
---
key: M01-T01
milestone: M01
unit: T01
unitKind: item
repo: ghost
title: t
status: planned
created: 2026-05-20
---

## Tasks

- **T1** — unregistered
  - Files: docs/x.md
  - Verify: nope
PLAN
  OUT=$(hvj plan validate-docs M01-T01)
  FIELD=$(jget 'data.mismatches[0].issue' <<<"$OUT")
  grep -q "sub-repo 'ghost' is not registered" <<<"$FIELD" || {
    echo "FAIL: unregistered repo should be flagged: $OUT"; exit 1;
  }
  pass "U4: plan tagged with unregistered sub-repo is flagged"
)
trap 'rm -rf "$TMP"' EXIT
rm -rf "$PVDU_TMP"
pass "## plan validate-docs umbrella cases"
