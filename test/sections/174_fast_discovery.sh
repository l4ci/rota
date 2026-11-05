echo "fast.sh rejects incomplete dependency discovery (#2712)"

TMP_FAST_DISCOVERY=$(mktemp -d)
trap 'rm -rf "$TMP_FAST_DISCOVERY"' EXIT
mkdir -p "$TMP_FAST_DISCOVERY/test" "$TMP_FAST_DISCOVERY/leaf" "$TMP_FAST_DISCOVERY/consumer"
cp "$TESTDIR/fast.sh" "$TESTDIR/gofmt.sh" "$TMP_FAST_DISCOVERY/test/"
printf 'module example.com/fast\n\ngo 1.22\n' > "$TMP_FAST_DISCOVERY/go.mod"
printf 'package leaf\n' > "$TMP_FAST_DISCOVERY/leaf/leaf.go"
printf 'package consumer\n' > "$TMP_FAST_DISCOVERY/consumer/consumer.go"
cat > "$TMP_FAST_DISCOVERY/consumer/consumer_test.go" <<'EOF'
package consumer_test
import _ "example.invalid/missing"
EOF

# The changed leaf is valid; only an unchanged package's test imports break
# discovery. Neither interface may turn that incomplete graph into success.
for dry_run in 0 1; do
  rc=0
  OUT=$(GOWORK=off GOPROXY=off GOTOOLCHAIN=local FAST_DRY_RUN="$dry_run" \
    bash "$TMP_FAST_DISCOVERY/test/fast.sh" leaf/leaf.go 2>&1) || rc=$?
  [ "$rc" -ne 0 ] || fail "broken test dependency must fail fast (dry-run=$dry_run): $OUT"
  grep -q 'example.invalid/missing' <<<"$OUT" || fail "missing dependency diagnostic was lost: $OUT"
  grep -q 'fast: dependency discovery failed' <<<"$OUT" || fail "fast must explain discovery failed: $OUT"
  if grep -Eq '^(plan:|fast: gofmt|fast: go vet|fast: go test)' <<<"$OUT"; then
    fail "incomplete discovery must stop before reporting or executing selection: $OUT"
  fi
done
pass "fast.sh rejects a real broken test dependency in normal and dry-run modes"

# Use the Go executable boundary to deterministically deliver valid records
# before a producer failure. The downstream mapper and sort both succeed.
rm "$TMP_FAST_DISCOVERY/consumer/consumer_test.go"
mkdir "$TMP_FAST_DISCOVERY/fake-bin"
cat > "$TMP_FAST_DISCOVERY/fake-bin/go" <<'EOF'
#!/usr/bin/env bash
if [ "$1" = list ] && [ "${2:-}" = -test ]; then
  echo 'example.com/fast/consumer example.com/fast/leaf'
  echo 'controlled graph failure after partial output' >&2
  exit 19
fi
exec "$FAST_REAL_GO" "$@"
EOF
chmod +x "$TMP_FAST_DISCOVERY/fake-bin/go"
FAST_DISCOVERY_GO=$(command -v go)
for dry_run in 0 1; do
  rc=0
  OUT=$(PATH="$TMP_FAST_DISCOVERY/fake-bin:$PATH" FAST_REAL_GO="$FAST_DISCOVERY_GO" \
    GOWORK=off GOPROXY=off GOTOOLCHAIN=local FAST_DRY_RUN="$dry_run" \
    bash "$TMP_FAST_DISCOVERY/test/fast.sh" leaf/leaf.go 2>&1) || rc=$?
  [ "$rc" -ne 0 ] || fail "partial graph output must fail fast (dry-run=$dry_run): $OUT"
  grep -q 'controlled graph failure after partial output' <<<"$OUT" || fail "producer diagnostic was lost: $OUT"
  grep -q 'fast: dependency discovery failed' <<<"$OUT" || fail "fast must explain partial discovery failed: $OUT"
  if grep -Eq '^(plan:|fast: gofmt|fast: go vet|fast: go test)' <<<"$OUT"; then
    fail "partial output must not become a reported or executed selection: $OUT"
  fi
done
pass "fast.sh discards partial graph output in normal and dry-run modes"

# No dependents is a successful discovery too: do not queue an empty package.
OUT=$(GOWORK=off GOPROXY=off GOTOOLCHAIN=local FAST_DRY_RUN=1 \
  bash "$TMP_FAST_DISCOVERY/test/fast.sh" leaf/leaf.go) || fail "valid graph must succeed: $OUT"
[ "$OUT" = $'plan: go ./leaf\nplan: tree-scans' ] || fail "a graph without dependents should select the leaf and tree scans: $OUT"

rm -rf "$TMP_FAST_DISCOVERY"
trap 'rm -rf "$TMP"' EXIT
pass "fast.sh retains the changed package when discovery finds no dependents"
