echo "init pins ## Glossary in KNOWLEDGE.md"
TMP_BOOT="$(mktemp -d)"
trap 'rm -rf "$TMP_BOOT"' EXIT
"$ROTA_BIN" -C "$TMP_BOOT" init >/dev/null
[ -f "$TMP_BOOT/.rota/KNOWLEDGE.md" ] || fail "init: missing .rota/KNOWLEDGE.md"
grep -q "^## Glossary$" "$TMP_BOOT/.rota/KNOWLEDGE.md" || fail "KNOWLEDGE.md missing pinned Glossary topic"
grep -q "no terms yet" "$TMP_BOOT/.rota/KNOWLEDGE.md" || fail "KNOWLEDGE.md Glossary missing placeholder"
# Idempotency: re-running doesn't overwrite existing content
echo "manual marker" >> "$TMP_BOOT/.rota/KNOWLEDGE.md"
"$ROTA_BIN" -C "$TMP_BOOT" init >/dev/null
grep -q "manual marker" "$TMP_BOOT/.rota/KNOWLEDGE.md" || fail "init clobbered existing KNOWLEDGE.md"
trap 'rm -rf "$TMP"' EXIT
pass "init pins Glossary + is idempotent"

echo "glossary read"
mkdir -p "$TMP/.rota"
cat > "$TMP/.rota/KNOWLEDGE.md" <<'EOF'
# Knowledge

## Glossary

- **backlog** — The canonical project queue (.rota/BACKLOG.md).
  - **Aliases:** task list
  <!-- 2026-05-10 -->

- **decision** — A hard project boundary captured in DECISIONS.md.
  - **Aliases:** _none_
  <!-- 2026-05-10 -->
EOF
gread() { hvj glossary read "$@" | jget data.text; }
OUT=$(gread backlog)
grep -q "^- \*\*backlog\*\*" <<<"$OUT" || fail "glossary read missing entry header"
grep -q "canonical project queue" <<<"$OUT" || fail "glossary read missing body"
# Case-insensitive
OUT2=$(gread Backlog)
grep -q "canonical project queue" <<<"$OUT2" || fail "glossary read case-insensitive"
# Unknown term returns empty + exit 0
OUT3=$(gread nonexistent)
[ -z "$OUT3" ] || fail "glossary read unknown term should be empty"
# `> from:` prefix is always present and names KNOWLEDGE.md + ## Glossary
OUT4=$(gread backlog)
grep -q "^> from: " <<<"$OUT4" || fail "glossary read missing > from: prefix"
grep -q "^> from: .rota/KNOWLEDGE.md (## Glossary)$" <<<"$OUT4" || fail "glossary read prefix should name KNOWLEDGE.md (## Glossary)"
# No terms is a usage error
rc=0; hvj glossary read >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "glossary read with no term should exit 2, got $rc"

# Document order preserved when querying multiple terms (decision is later in the file than backlog)
OUT5=$(gread decision backlog)
B_LINE=$(grep -n "^- \*\*backlog\*\*" <<<"$OUT5" | head -1 | cut -d: -f1)
D_LINE=$(grep -n "^- \*\*decision\*\*" <<<"$OUT5" | head -1 | cut -d: -f1)
[ -n "$B_LINE" ] && [ -n "$D_LINE" ] && [ "$B_LINE" -lt "$D_LINE" ] || fail "glossary read document order not preserved"
pass "glossary read"

echo "glossary write — new term"
TMP_ADD="$(mktemp -d)"
trap 'rm -rf "$TMP_ADD"' EXIT
"$ROTA_BIN" -C "$TMP_ADD" init >/dev/null
out=$(hvj -C "$TMP_ADD" glossary write backlog \
    --def "The canonical project queue (.rota/BACKLOG.md). Items are zero-padded IDs." \
    --alias "task list,todo list") || vfail
[ "$(jget data.term <<<"$out")" = "backlog" ] || fail "glossary write term wrong: $out"
[ "$(jget data.changed <<<"$out")" = "true" ] || fail "glossary write new term should report changed: $out"
grep -q "^- \*\*backlog\*\* — " "$TMP_ADD/.rota/KNOWLEDGE.md" || fail "missing backlog entry"
grep -q "^  - \*\*Aliases:\*\* task list, todo list$" "$TMP_ADD/.rota/KNOWLEDGE.md" || fail "aliases line wrong"
grep -q "^  <!-- $(date +%Y-%m-%d) -->$" "$TMP_ADD/.rota/KNOWLEDGE.md" || fail "date stamp missing"
grep -q "no terms yet" "$TMP_ADD/.rota/KNOWLEDGE.md" && fail "placeholder should be stripped after first term added"
grep -q "<!-- rota-knowledge-start -->" "$TMP_ADD/AGENTS.md" || fail "knowledge block missing"
grep -q "^- Glossary$" "$TMP_ADD/AGENTS.md" || fail "Glossary topic not surfaced in AGENTS.md"
pass "glossary write — new term inserts + indexes"

echo "glossary write — no aliases writes _none_"
"$ROTA_BIN" -C "$TMP_ADD" glossary write session --def "An active rota work cycle." >/dev/null
SESSION_ENTRY=$(grep -A2 "^- \*\*session\*\*" "$TMP_ADD/.rota/KNOWLEDGE.md")
grep -q "^  - \*\*Aliases:\*\* _none_$" <<<"$SESSION_ENTRY" || fail "missing _none_"
pass "glossary write — empty aliases produce _none_"

echo "glossary write — alphabetical insertion"
"$ROTA_BIN" -C "$TMP_ADD" glossary write alpha --def "First alphabetically." >/dev/null
ORDER=$(grep -E '^- \*\*' "$TMP_ADD/.rota/KNOWLEDGE.md" | sed -E 's/^- \*\*([^*]+)\*\*.*/\1/')
EXPECTED=$'alpha\nbacklog\nsession'
[ "$ORDER" = "$EXPECTED" ] || fail "alphabetical insertion failed: got '$ORDER'"
pass "glossary write — alphabetical insertion"

echo "glossary write — update existing (def replace, alias union, date preserved)"
ORIG_DATE=$(grep -A3 "^- \*\*backlog\*\*" "$TMP_ADD/.rota/KNOWLEDGE.md" | grep -oE '<!-- [0-9-]+ -->' | head -1)
"$ROTA_BIN" -C "$TMP_ADD" glossary write backlog \
    --def "The canonical project queue, refined." \
    --alias "queue" >/dev/null
ENTRY_BACKLOG=$(grep -A3 "^- \*\*backlog\*\*" "$TMP_ADD/.rota/KNOWLEDGE.md")
grep -q "queue, refined" <<<"$ENTRY_BACKLOG" || fail "def not replaced"
grep -q "\*\*Aliases:\*\* task list, todo list, queue" <<<"$ENTRY_BACKLOG" || fail "aliases not unioned"
grep -q "$ORIG_DATE" <<<"$ENTRY_BACKLOG" || fail "date should be preserved without --touch"
pass "glossary write — update preserves date, unions aliases"

echo "glossary write — --touch updates the date"
TODAY=$(date +%Y-%m-%d)
"$ROTA_BIN" -C "$TMP_ADD" glossary write backlog --def "X." --touch >/dev/null
ENTRY_BACKLOG=$(grep -A3 "^- \*\*backlog\*\*" "$TMP_ADD/.rota/KNOWLEDGE.md")
grep -q "<!-- $TODAY -->" <<<"$ENTRY_BACKLOG" || fail "--touch didn't bump date"
pass "glossary write --touch"

echo "glossary write — --not field"
"$ROTA_BIN" -C "$TMP_ADD" glossary write zterm --def "Z thing." --not "X, Y" >/dev/null
ENTRY_ZTERM=$(grep -A3 "^- \*\*zterm\*\*" "$TMP_ADD/.rota/KNOWLEDGE.md")
grep -q "^  - \*\*Not:\*\* X, Y$" <<<"$ENTRY_ZTERM" || fail "Not line missing"
pass "glossary write --not"

trap 'rm -rf "$TMP"' EXIT

echo "glossary write — alias collision refused"
TMP_CC="$(mktemp -d)"
trap 'rm -rf "$TMP_CC"' EXIT
"$ROTA_BIN" -C "$TMP_CC" init >/dev/null
"$ROTA_BIN" -C "$TMP_CC" glossary write backlog --def "Q." --alias "task list" >/dev/null
rc=0; out=$(hvj -C "$TMP_CC" glossary write inbox --def "I." --alias "task list" 2>/dev/null) || rc=$?
[ "$rc" = 4 ] || fail "alias collision should exit 4, got $rc"
[ "$(jget data.changed <<<"$out")" = "false" ] || fail "refused write should report changed=false: $out"
[ "$(jget data.blockedBy <<<"$out")" = "alias-collision" ] || fail "refused write should report blockedBy=alias-collision: $out"
# A missing --def is a usage error
rc=0; hvj -C "$TMP_CC" glossary write inbox >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "glossary write without --def should exit 2, got $rc"
# Source file should NOT have been mutated
grep -q "^- \*\*inbox\*\*" "$TMP_CC/.rota/KNOWLEDGE.md" && fail "inbox should not be written on collision"
trap 'rm -rf "$TMP"' EXIT
pass "glossary write — alias collision refused"
