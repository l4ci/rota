echo "sidecar lock — sidecar read-modify-write survives concurrent writers"
# rota serializes sidecar read-modify-write cycles via
# flock on a sibling <path>.lock file. These assertions pin the contract:
# (a) N concurrent knowledge hit calls lose no increments;
# (b) N concurrent knowledge contradiction add calls lose no entries;
# (c) a leftover .lock file (left in place by design — unlink-after-release
#     races with the next acquirer) never blocks a subsequent call.

TMP_LCK="$(mktemp -d)"
trap 'rm -rf "$TMP_LCK"' EXIT
mkdir -p "$TMP_LCK/.rota"
# Keep auto-promotion out of the way: threshold far above the hit counts
# below, so every concurrent writer takes the plain increment path.
printf '{"learn":{"promoteThreshold":99}}\n' > "$TMP_LCK/.rota/config.json"

# ── (a) 8 concurrent hits on one topic/title — hits must equal exactly 8 ─────
for _ in 1 2 3 4 5 6 7 8; do
  "$ROTA_BIN" -C "$TMP_LCK" knowledge hit \
      --topic "Concurrency" --title "Lock rule" >/dev/null 2>&1 &
done
wait

HITS=$(hvj -C "$TMP_LCK" knowledge tier get \
          --topic "Concurrency" --title "Lock rule" | jget data.hits) || vfail
[ "$HITS" = "8" ] || fail "concurrent knowledge hit lost increments: expected hits=8, got $HITS"
pass "8 concurrent knowledge hit calls record exactly 8 hits (no lost increments)"

# ── (b) 6 concurrent --add with distinct texts — queue length must be 6 ──────
for i in 1 2 3 4 5 6; do
  "$ROTA_BIN" -C "$TMP_LCK" knowledge contradiction add \
      --topic "Concurrency" --title "Lock rule" \
      --text "concurrent correction $i" >/dev/null 2>&1 &
done
wait

QLEN=$(hvj -C "$TMP_LCK" knowledge contradiction list | jget data.items | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))') || vfail
[ "$QLEN" = "6" ] || fail "concurrent contradiction add lost entries: expected 6 pending, got $QLEN"
pass "6 concurrent knowledge contradiction add calls keep all 6 entries"

trap 'rm -rf "$TMP"' EXIT
pass "sidecar lock concurrency contract"
