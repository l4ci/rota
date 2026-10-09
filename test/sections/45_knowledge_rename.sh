# T03 — knowledge rename-topic: atomic heading move + tier sidecar re-key
echo "T03: knowledge rename-topic — atomic re-key on heading move"

TMP_KR="$(mktemp -d)"
trap 'rm -rf "$TMP_KR"' EXIT
mkdir -p "$TMP_KR/.rota"

# Seed KNOWLEDGE.md with two topics, three titled bullets, and prime the
# tier sidecar with confirmed/hits state — exactly what auto-split would
# orphan today.
cat > "$TMP_KR/.rota/KNOWLEDGE.md" <<'EOF'
# Knowledge

## Architecture

- **Foo rule** — body of foo. <!-- 2026-05-15 -->
- **Bar rule** — body of bar. <!-- 2026-05-15 -->

## Build & Tooling

- **Baz rule** — body of baz. <!-- 2026-05-15 -->
EOF

# Prime sidecar with non-default state so re-key wins/losses are visible.
hvj -C "$TMP_KR" knowledge tier set --topic "Architecture" --title "Foo rule" --tier confirmed >/dev/null
hvj -C "$TMP_KR" knowledge hit --topic "Architecture" --title "Foo rule" >/dev/null
hvj -C "$TMP_KR" knowledge hit --topic "Architecture" --title "Foo rule" >/dev/null
hvj -C "$TMP_KR" knowledge tier set --topic "Architecture" --title "Bar rule" --tier deprecated >/dev/null
hvj -C "$TMP_KR" knowledge tier set --topic "Build & Tooling" --title "Baz rule" --tier provisional >/dev/null

# ---------- Per-bullet move ----------
# Append the target heading first (matches Step 8 auto-split step 3).
cat >> "$TMP_KR/.rota/KNOWLEDGE.md" <<'EOF'

## Architecture: Foundations

EOF

OUT=$(hvj -C "$TMP_KR" knowledge rename-topic \
    --from "Architecture" \
    --to "Architecture: Foundations" \
    --title "Foo rule") || fail "per-bullet rename returned non-zero: $OUT"
[ "$(jget data.mode <<<"$OUT")" = "bullet" ] || fail "per-bullet rename should report mode bullet: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "per-bullet rename should report changed: $OUT"

# Foo rule moved out of Architecture, into Foundations.
grep -A2 "^## Architecture$" "$TMP_KR/.rota/KNOWLEDGE.md" | grep "Foo rule" >/dev/null \
  && fail "Foo rule still under Architecture after per-bullet move"
grep -A2 "^## Architecture: Foundations$" "$TMP_KR/.rota/KNOWLEDGE.md" | grep "Foo rule" >/dev/null \
  || fail "Foo rule not under Architecture: Foundations after per-bullet move"
grep -A2 "^## Architecture$" "$TMP_KR/.rota/KNOWLEDGE.md" | grep "Bar rule" >/dev/null \
  || fail "Bar rule lost from Architecture after Foo move"
pass "per-bullet move: bullet relocated, siblings untouched"

# Sidecar re-keyed: Foundations carries confirmed/2-hits state, old key gone.
OUT=$(hvj -C "$TMP_KR" knowledge tier get --topic "Architecture: Foundations" --title "Foo rule") || vfail
TIER="$(jget data.tier <<<"$OUT")/$(jget data.hits <<<"$OUT")"
[ "$TIER" = "confirmed/2" ] || fail "Foo rule sidecar lost tier/hits after move: $TIER"
pass "per-bullet move: sidecar tier+hits follow to new key"

OLD=$(hvj -C "$TMP_KR" knowledge tier get --topic "Architecture" --title "Foo rule") || vfail
[ "$(jget data.found <<<"$OLD")" = "false" ] || fail "old sidecar key 'Architecture::Foo rule' not cleared: $OLD"
pass "per-bullet move: old sidecar key removed"

# ---------- Per-bullet error: missing target heading ----------
RC=0; hvj -C "$TMP_KR" knowledge rename-topic \
    --from "Architecture" --to "Architecture: Nonexistent" --title "Bar rule" >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "per-bullet rename should reject missing target heading with exit 3; got $RC"
pass "per-bullet move rejects missing target heading"

# ---------- Per-bullet error: bullet title not found ----------
RC=0; hvj -C "$TMP_KR" knowledge rename-topic \
    --from "Architecture" --to "Architecture: Foundations" --title "Quux rule" >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "per-bullet rename should reject unknown title with exit 3; got $RC"
pass "per-bullet move rejects unknown bullet title"

# ---------- Whole-topic rename ----------
OUT=$(hvj -C "$TMP_KR" knowledge rename-topic \
    --from "Build & Tooling" \
    --to "Tooling & Build") || fail "whole-topic rename returned non-zero: $OUT"
[ "$(jget data.mode <<<"$OUT")" = "topic" ] || fail "whole-topic rename should report mode topic: $OUT"

grep -q "^## Tooling & Build$" "$TMP_KR/.rota/KNOWLEDGE.md" \
  || fail "renamed heading '## Tooling & Build' not present"
grep -q "^## Build & Tooling$" "$TMP_KR/.rota/KNOWLEDGE.md" \
  && fail "old heading '## Build & Tooling' still present after rename"
grep -A2 "^## Tooling & Build$" "$TMP_KR/.rota/KNOWLEDGE.md" | grep "Baz rule" >/dev/null \
  || fail "Baz rule did not follow whole-topic rename"
pass "whole-topic rename: heading renamed, bullets follow"

NEW=$(hvj -C "$TMP_KR" knowledge tier get --topic "Tooling & Build" --title "Baz rule" | jget data.tier || echo missing) || vfail
[ "$NEW" = "provisional" ] || fail "Baz rule sidecar entry missing under new topic: $NEW"
OLD=$(hvj -C "$TMP_KR" knowledge tier get --topic "Build & Tooling" --title "Baz rule") || vfail
[ "$(jget data.found <<<"$OLD")" = "false" ] || fail "old 'Build & Tooling::Baz rule' sidecar key not cleared: $OLD"
pass "whole-topic rename: sidecar entries follow to new topic prefix"

# ---------- Whole-topic error: target heading already exists ----------
# Architecture and Architecture: Foundations both exist; renaming X→Y when
# Y exists is a merge, not a rename. Reject.
RC=0; OUT=$(hvj -C "$TMP_KR" knowledge rename-topic \
    --from "Architecture" --to "Architecture: Foundations" 2>/dev/null) || RC=$?
[ "$RC" = "4" ] || fail "whole-topic rename should refuse pre-existing target with exit 4; got $RC"
[ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "refused rename should report changed:false: $OUT"
pass "whole-topic rename rejects pre-existing target"

# ---------- Whole-topic error: source heading missing ----------
RC=0; hvj -C "$TMP_KR" knowledge rename-topic \
    --from "NoSuchTopic" --to "Anything" >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "rename should reject missing source heading with exit 3; got $RC"
pass "rename rejects missing source heading"

# ---------- Same-name no-op ----------
OUT=$(hvj -C "$TMP_KR" knowledge rename-topic \
    --from "Architecture" --to "Architecture") \
  || fail "same-name rename should be a no-op (exit 0)"
[ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "same-name rename should report changed:false: $OUT"
grep -q "^## Architecture$" "$TMP_KR/.rota/KNOWLEDGE.md" \
  || fail "Architecture heading disappeared after same-name no-op"
pass "same-name rename is silent no-op"

# ---------- Argv validation ----------
RC=0; hvj -C "$TMP_KR" knowledge rename-topic --from "Architecture" >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "missing --to should exit 2; got $RC"
pass "missing --to flag rejected"

RC=0; hvj -C "$TMP_KR" knowledge rename-topic --to "X" >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "missing --from should exit 2; got $RC"
pass "missing --from flag rejected"

# ---------- Auto-split end-to-end shape ----------
# Mirror the /rota-learn Step 8 auto-split flow on a fresh fixture: large topic
# with 3 bullets split into 2 facets, each call atomic.
rm -f "$TMP_KR/.rota/knowledge-tier.json"
cat > "$TMP_KR/.rota/KNOWLEDGE.md" <<'EOF'
# Knowledge

## Networking

- **Retry rule** — body. <!-- 2026-05-15 -->
- **Timeout rule** — body. <!-- 2026-05-15 -->
- **TLS rule** — body. <!-- 2026-05-15 -->
EOF
for T in "Retry rule" "Timeout rule" "TLS rule"; do
  hvj -C "$TMP_KR" knowledge tier set --topic "Networking" --title "$T" --tier provisional >/dev/null \
    || fail "could not register $T in the tier sidecar"
done

# Step 3: append facet headings before old topic.
cat >> "$TMP_KR/.rota/KNOWLEDGE.md" <<'EOF'

## Networking: Reliability

## Networking: Security

EOF

# Step 4: per-bullet moves (parallel-safe — each call is atomic per file).
hvj -C "$TMP_KR" knowledge rename-topic --from "Networking" --to "Networking: Reliability" --title "Retry rule" >/dev/null
hvj -C "$TMP_KR" knowledge rename-topic --from "Networking" --to "Networking: Reliability" --title "Timeout rule" >/dev/null
hvj -C "$TMP_KR" knowledge rename-topic --from "Networking" --to "Networking: Security" --title "TLS rule" >/dev/null

# All three sidecar keys re-anchored under the new facets.
T1=$(hvj -C "$TMP_KR" knowledge tier get --topic "Networking: Reliability" --title "Retry rule" | jget data.tier || echo missing) || vfail
T2=$(hvj -C "$TMP_KR" knowledge tier get --topic "Networking: Reliability" --title "Timeout rule" | jget data.tier || echo missing) || vfail
T3=$(hvj -C "$TMP_KR" knowledge tier get --topic "Networking: Security" --title "TLS rule" | jget data.tier || echo missing) || vfail
[ "$T1" = "provisional" ] && [ "$T2" = "provisional" ] && [ "$T3" = "provisional" ] \
  || fail "auto-split flow lost sidecar entries: T1=$T1 T2=$T2 T3=$T3"
pass "auto-split flow: 3 bullets across 2 facets, all sidecar entries follow"

# Old `Networking::*` keys all gone.
COUNT=$(hvj -C "$TMP_KR" knowledge tier list | jget data.entries | python3 -c '
import json, sys
data = json.load(sys.stdin)
print(sum(1 for e in data if e["topic"] == "Networking"))') || vfail
[ "$COUNT" = "0" ] || fail "auto-split flow left $COUNT orphan 'Networking::*' keys"
pass "auto-split flow: no orphan sidecar entries under old topic"

# Restore the global trap before the next section runs.
trap 'rm -rf "$TMP"' EXIT
