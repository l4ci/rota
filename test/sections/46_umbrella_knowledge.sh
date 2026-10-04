# F21 — umbrella-aware KNOWLEDGE.md: scoped writes, hybrid query, tier sidecars, amend guard, glossary, CLAUDE.md blocks, decisions guard
echo "F21: umbrella-aware KNOWLEDGE.md — end-to-end"

# ── Build primary fixture ───────────────────────────────────────────────────
TMP_UK="$(mktemp -d)"
trap 'rm -rf "$TMP_UK"' EXIT
(
  cd "$TMP_UK"
  git init -q .
  git config user.email t@t && git config user.name t
  mkdir -p .rota .rota/contexts/web web api
  ( cd web && git init -q . && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m i )
  ( cd api && git init -q . && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m i )
  printf '{"repos":[{"name":"web","path":"./web"},{"name":"api","path":"./api"}]}' > .rota/repos.json
  printf '{"rota":{"version":"3.0.0"}}' > .rota/config.json
  printf '# Knowledge\n\n## Architecture\n\n- **umbrella rule** — cross-repo body <!-- 2026-05-19 -->\n\n## Glossary\n\n' > .rota/KNOWLEDGE.md
  mkdir -p .rota/knowledge/web .rota/knowledge/api
  printf '# Knowledge\n\n## Architecture\n\n## Glossary\n\n' > .rota/knowledge/web/KNOWLEDGE.md
  printf '# Knowledge\n\n## Architecture\n\n## Glossary\n\n' > .rota/knowledge/api/KNOWLEDGE.md
  printf '# Context\n\n## Widget\n\nA web widget.\n' > .rota/contexts/web/CONTEXT.md
  printf '/web/\n/api/\n' > .gitignore
  git -c user.email=t@t -c user.name=t add -A
  git -c user.email=t@t -c user.name=t commit -qm init
)

# ── 1. Scoped write: from web subdir, merge lands in sub-repo KNOWLEDGE.md ──
echo "F21: scoped write — knowledge add from sub-repo"
OUT=$( cd "$TMP_UK/web" && hvj knowledge add --topic Architecture --title "web rule" --body-file - <<<"web-local" )
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "F21[1]: add from web must report changed: $OUT"
grep -q "web rule" "$TMP_UK/.rota/knowledge/web/KNOWLEDGE.md" \
  || fail "F21[1]: 'web rule' must appear in .rota/knowledge/web/KNOWLEDGE.md"
grep -q "web rule" "$TMP_UK/.rota/KNOWLEDGE.md" \
  && fail "F21[1]: 'web rule' must NOT appear in umbrella .rota/KNOWLEDGE.md"
pass "F21[1]: scoped write lands in sub-repo KNOWLEDGE.md only"

# ── 2. Hybrid query: sub-repo scope shows both files with > from:; umbrella shows only its own ──
echo "F21: hybrid query"
# The `> from:` provenance lines are part of the verb's body (contract: text is the old markdown verbatim).
QUERY_WEB="$( cd "$TMP_UK/web" && hvj knowledge query Architecture | jget data.text )"
grep -q "umbrella rule" <<<"$QUERY_WEB" \
  || fail "F21[2]: hybrid query from web must include 'umbrella rule'"
grep -q "web rule" <<<"$QUERY_WEB" \
  || fail "F21[2]: hybrid query from web must include 'web rule'"
grep -q "> from:" <<<"$QUERY_WEB" \
  || fail "F21[2]: hybrid query from web must include a '> from:' provenance line"

QUERY_UMBRELLA="$( cd "$TMP_UK" && hvj knowledge query Architecture | jget data.text )"
grep -q "umbrella rule" <<<"$QUERY_UMBRELLA" \
  || fail "F21[2]: umbrella-scope query must include 'umbrella rule'"
grep -q "web rule" <<<"$QUERY_UMBRELLA" \
  && fail "F21[2]: umbrella-scope query must NOT include 'web rule'"
grep -q "> from:" <<<"$QUERY_UMBRELLA" \
  && fail "F21[2]: umbrella-scope query must NOT include a '> from:' line"
pass "F21[2]: hybrid query shows correct provenance per scope"

# ── 3. Per-file tier sidecar ────────────────────────────────────────────────
echo "F21: per-file tier sidecar"
OUT=$( cd "$TMP_UK/web" && hvj knowledge tier set --topic Architecture --title "web rule" --tier confirmed )
[ "$(jget data.tier <<<"$OUT")" = "confirmed" ] || fail "F21[3]: tier set must report the new tier: $OUT"
[ "$(cd "$TMP_UK/web" && hvj knowledge tier get --topic Architecture --title "web rule" | jget data.tier)" = "confirmed" ] \
  || fail "F21[3]: tier get from web must read back confirmed"
[ -f "$TMP_UK/.rota/knowledge/web/knowledge-tier.json" ] \
  || fail "F21[3]: .rota/knowledge/web/knowledge-tier.json must exist after tier set"
grep -q "web rule" "$TMP_UK/.rota/knowledge/web/knowledge-tier.json" \
  || fail "F21[3]: sub-repo tier sidecar must contain 'web rule'"
# Umbrella sidecar must NOT have "web rule"
if [ -f "$TMP_UK/.rota/knowledge-tier.json" ]; then
  grep -q "web rule" "$TMP_UK/.rota/knowledge-tier.json" \
    && fail "F21[3]: umbrella tier sidecar must NOT contain 'web rule'"
fi
pass "F21[3]: tier sidecar is scoped per sub-repo"

# ── 4. Cross-file amend guard ───────────────────────────────────────────────
echo "F21: cross-file amend guard"
# Seed "shared rule" in BOTH files: umbrella scope is the umbrella root, web scope is --repo web.
( cd "$TMP_UK" && hvj knowledge add --topic Architecture --title "shared rule" --body-file - <<<"in umbrella" >/dev/null )
( cd "$TMP_UK" && hvj knowledge add --topic Architecture --title "shared rule" --body-file - --repo web <<<"in web" >/dev/null )

# Without --repo from the sub-repo: ambiguous → exit 2 (a missing decision), hint names --repo
AMEND_RC=0
AMEND_OUT="$( cd "$TMP_UK/web" && hvj knowledge amend --topic Architecture --fragment "shared rule" --mode append --body-file - <<<"(x)" 2>&1 )" || AMEND_RC=$?
[ "$AMEND_RC" -eq 2 ] \
  || fail "F21[4]: amend without --repo must exit 2 when fragment matches in multiple files, got $AMEND_RC"
grep -q -- "--repo" <<<"$AMEND_OUT" \
  || fail "F21[4]: amend error must hint at --repo; got: $AMEND_OUT"

# With --repo web: unambiguous → exit 0, amends only web file
OUT=$( cd "$TMP_UK/web" && hvj knowledge amend --topic Architecture --fragment "shared rule" --mode append --body-file - --repo web <<<"(x)" )
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "F21[4]: --repo web amend must report changed: $OUT"
grep -q "(x)" "$TMP_UK/.rota/knowledge/web/KNOWLEDGE.md" \
  || fail "F21[4]: --repo web amend must append to web file"
grep -q "(x)" "$TMP_UK/.rota/KNOWLEDGE.md" \
  && fail "F21[4]: --repo web amend must NOT touch umbrella file"
pass "F21[4]: cross-file amend guard blocks ambiguous; --repo web resolves it"

# ── 5. Glossary parity ──────────────────────────────────────────────────────
echo "F21: glossary parity"
( cd "$TMP_UK/web" && hvj glossary write webterm --def "a web term" >/dev/null )
grep -q "webterm" "$TMP_UK/.rota/knowledge/web/KNOWLEDGE.md" \
  || fail "F21[5]: webterm must land in .rota/knowledge/web/KNOWLEDGE.md Glossary"
GLOSS_READ="$( cd "$TMP_UK/web" && hvj glossary read webterm | jget data.text )"
grep -q "a web term" <<<"$GLOSS_READ" \
  || fail "F21[5]: glossary read must print the term definition"
grep -q "> from: .rota/knowledge/web/KNOWLEDGE.md (## Glossary)" <<<"$GLOSS_READ" \
  || fail "F21[5]: glossary read must include provenance line for web scope; got: $GLOSS_READ"
pass "F21[5]: glossary write scoped to sub-repo; read shows provenance"

# ── 6. Per-sub-repo CLAUDE.md block ─────────────────────────────────────────
echo "F21: per-sub-repo CLAUDE.md block"
( cd "$TMP_UK" && hvj block knowledge --repo web >/dev/null )
[ -f "$TMP_UK/web/CLAUDE.md" ] \
  || fail "F21[6]: web/CLAUDE.md must exist after block knowledge --repo web"
grep -q "Architecture" "$TMP_UK/web/CLAUDE.md" \
  || fail "F21[6]: web/CLAUDE.md knowledge block must list 'Architecture'"
# Umbrella scope is the umbrella root with no --repo (`--repo umbrella` is not a registered sub-repo: exit 3).
UMBRELLA_BLOCK_RC=0
( cd "$TMP_UK" && hvj block knowledge --repo umbrella >/dev/null 2>&1 ) || UMBRELLA_BLOCK_RC=$?
[ "$UMBRELLA_BLOCK_RC" -eq 3 ] \
  || fail "F21[6]: block knowledge --repo umbrella must exit 3 (not a registered sub-repo), got $UMBRELLA_BLOCK_RC"
UMBRELLA_BLOCK_RC=0
( cd "$TMP_UK" && hvj block knowledge >/dev/null 2>&1 ) || UMBRELLA_BLOCK_RC=$?
[ "$UMBRELLA_BLOCK_RC" -eq 0 ] \
  || fail "F21[6]: block knowledge at the umbrella root must exit 0"
[ -f "$TMP_UK/CLAUDE.md" ] \
  || fail "F21[6]: umbrella CLAUDE.md must exist after block knowledge at the umbrella root"
# Umbrella CLAUDE.md must not be the same file as web/CLAUDE.md
[ "$(realpath "$TMP_UK/CLAUDE.md")" != "$(realpath "$TMP_UK/web/CLAUDE.md")" ] \
  || fail "F21[6]: umbrella CLAUDE.md and web CLAUDE.md must be different files"
pass "F21[6]: per-sub-repo CLAUDE.md block written; umbrella block succeeds"

# ── 8. Decisions stay umbrella-only ─────────────────────────────────────────
echo "F21: decisions umbrella-only guard"
DECISIONS_RC=0
( cd "$TMP_UK" && hvj block decisions --repo web >/dev/null 2>&1 ) || DECISIONS_RC=$?
[ "$DECISIONS_RC" -eq 2 ] \
  || fail "F21[8]: block decisions --repo web must exit 2 (Persistence-trio scoping boundary), got $DECISIONS_RC"
DECISIONS_RC=0
( cd "$TMP_UK" && hvj block decisions --repo nosuch >/dev/null 2>&1 ) || DECISIONS_RC=$?
[ "$DECISIONS_RC" -eq 3 ] \
  || fail "F21[8]: an unregistered --repo must exit 3 (resolution) before the verb's own check, got $DECISIONS_RC"
pass "F21[8]: decisions block rejects non-umbrella --repo (Persistence-trio boundary)"

# ── Restore global trap and terminal pass ────────────────────────────────────
trap 'rm -rf "$TMP"' EXIT
pass "F21: umbrella-aware KNOWLEDGE.md end-to-end"
