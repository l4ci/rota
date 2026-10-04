echo "glossary import — atomic multi-term import"
TMP_BATCH="$(mktemp -d)"
trap 'rm -rf "$TMP_BATCH"' EXIT
"$ROTA_BIN" -C "$TMP_BATCH" init >/dev/null
git -C "$TMP_BATCH" init -q
git -C "$TMP_BATCH" config user.email t@t
git -C "$TMP_BATCH" config user.name t

# 1. Valid 3-term batch — all written, alphabetical order.
cat > "$TMP_BATCH/manifest.tsv" <<'EOF'
# header comment, skipped
backlog	The canonical project queue.	task list, todo list
decision	A hard project boundary.
session	An active work cycle.	cycle, run
EOF
rc=0; out=$(hvj -C "$TMP_BATCH" glossary import --body-file "$TMP_BATCH/manifest.tsv" 2>/dev/null) || rc=$?
[ "$rc" = 0 ] || fail "valid batch should succeed, got exit $rc"
[ "$(jget data.imported <<<"$out")" = "3" ] || fail "imported count wrong: $out"
[ "$(jget data.terms <<<"$out")" = '["backlog","decision","session"]' ] || fail "imported terms wrong: $out"
[ "$(jget data.changed <<<"$out")" = "true" ] || fail "valid batch should report changed: $out"
ORDER=$(grep -E '^- \*\*' "$TMP_BATCH/.rota/KNOWLEDGE.md" | sed -E 's/^- \*\*([^*]+)\*\*.*/\1/')
EXPECTED=$'backlog\ndecision\nsession'
[ "$ORDER" = "$EXPECTED" ] || fail "batch order wrong: got '$ORDER'"
grep -q "^  - \*\*Aliases:\*\* task list, todo list$" "$TMP_BATCH/.rota/KNOWLEDGE.md" || fail "backlog aliases missing"
grep -q "^  - \*\*Aliases:\*\* _none_$" "$TMP_BATCH/.rota/KNOWLEDGE.md" || fail "decision _none_ missing"
pass "glossary import — valid batch writes alphabetically"

# 2. Intra-batch alias collision — refusal with conflict line; KNOWLEDGE.md unchanged.
SNAPSHOT_BEFORE=$(cat "$TMP_BATCH/.rota/KNOWLEDGE.md")
cat > "$TMP_BATCH/manifest_intra.tsv" <<'EOF'
foo	Foo def.	shared
bar	Bar def.	shared
EOF
rc=0; out=$(hvj -C "$TMP_BATCH" glossary import --body-file "$TMP_BATCH/manifest_intra.tsv" 2>/dev/null) || rc=$?
[ "$rc" = 4 ] || fail "intra-batch collision should exit 4, got $rc"
[ "$(jget data.changed <<<"$out")" = "false" ] || fail "refused batch should report changed=false: $out"
[ "$(jget data.blockedBy <<<"$out")" = "alias-collision" ] || fail "refused batch should report blockedBy=alias-collision: $out"
grep -q "^- \*\*foo\*\*" "$TMP_BATCH/.rota/KNOWLEDGE.md" && fail "foo should NOT be written"
grep -q "^- \*\*bar\*\*" "$TMP_BATCH/.rota/KNOWLEDGE.md" && fail "bar should NOT be written"
SNAPSHOT_AFTER=$(cat "$TMP_BATCH/.rota/KNOWLEDGE.md")
[ "$SNAPSHOT_BEFORE" = "$SNAPSHOT_AFTER" ] || fail "KNOWLEDGE.md changed despite refusal"
pass "glossary import — intra-batch collision refuses all"

# 3. Collision with pre-batch existing entry — same refusal shape.
cat > "$TMP_BATCH/manifest_existing.tsv" <<'EOF'
inbox	Inbox def.	task list
EOF
rc=0; out=$(hvj -C "$TMP_BATCH" glossary import --body-file "$TMP_BATCH/manifest_existing.tsv" 2>/dev/null) || rc=$?
[ "$rc" = 4 ] || fail "pre-batch collision should exit 4, got $rc"
[ "$(jget data.changed <<<"$out")" = "false" ] || fail "refused batch should report changed=false: $out"
[ "$(jget data.blockedBy <<<"$out")" = "alias-collision" ] || fail "refused batch should report blockedBy=alias-collision: $out"
grep -q "^- \*\*inbox\*\*" "$TMP_BATCH/.rota/KNOWLEDGE.md" && fail "inbox should NOT be written"
pass "glossary import — pre-batch collision refuses all"

echo "knowledge tier — Glossary topic is skipped (terms aren't tier-eligible)"
# `tier set` on Glossary is a silent no-op (the old --init case): exit 0, nothing recorded
rc=0; out=$(hvj -C "$TMP_BATCH" knowledge tier set --topic "Glossary" --title "backlog" --tier provisional 2>/dev/null) || rc=$?
[ "$rc" = 0 ] || fail "Glossary tier set should exit 0, got $rc"
[ "$(jget data.changed <<<"$out")" = "false" ] || fail "Glossary tier set should report changed=false: $out"
# tier get on Glossary is never found
rc=0; out=$(hvj -C "$TMP_BATCH" knowledge tier get --topic "Glossary" --title "backlog" 2>/dev/null) || rc=$?
[ "$rc" = 0 ] || fail "Glossary tier get should exit 0, got $rc"
[ "$(jget data.found <<<"$out")" = "false" ] || fail "Glossary tier get should be found=false: $out"
# Even if we force-set a Glossary entry into the sidecar by hand, tier list filters it out
mkdir -p "$TMP_BATCH/.rota"
cat > "$TMP_BATCH/.rota/knowledge-tier.json" <<'EOF'
{"version": 1, "entries": {"Glossary::leaked": {"tier": "provisional", "hits": 5, "lastSeen": "2026-05-10"}, "Architecture::real": {"tier": "confirmed", "hits": 3, "lastSeen": "2026-05-10"}}}
EOF
rc=0; LIST_OUT=$(hvj -C "$TMP_BATCH" knowledge tier list 2>/dev/null) || rc=$?
[ "$rc" = 0 ] || fail "tier list should exit 0, got $rc"
[ "$(jget 'data.entries[0].topic' <<<"$LIST_OUT")" = "Architecture" ] || fail "non-Glossary topic should appear in tier list: $LIST_OUT"
[ -z "$(jget 'data.entries[1]' <<<"$LIST_OUT" || true)" ] || fail "tier list should filter Glossary entries: $LIST_OUT"
pass "knowledge tier — Glossary skip enforced on tier set/get/list"

trap 'rm -rf "$TMP"' EXIT
