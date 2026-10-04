echo "tracker call"

TMP_TC="$(mktemp -d)"
trap 'rm -rf "$TMP_TC"' EXIT
mkdir -p "$TMP_TC/proj/.rota" "$TMP_TC/fake"
echo '{"issues":{"provider":"github","retryWaitSeconds":0}}' > "$TMP_TC/proj/.rota/config.json"

# Fake gh/glab: log argv (+ cwd, stdin) per call; behavior from FAKE_MODE.
for cli in gh glab; do
  cat > "$TMP_TC/fake/$cli" <<FAKE
#!/usr/bin/env bash
echo "\$*" >> "\$FAKE_LOG"
echo "cwd:\$(pwd -P)" >> "\$FAKE_LOG.cwd"
[ -t 0 ] || cat > "\$FAKE_LOG.stdin"
case "\${FAKE_MODE:-ok}" in
  ok) python3 -c "import json,os; print(json.dumps([{'n':i} for i in range(int(os.environ.get('FAKE_N','2')))]))" ;;
  primary) echo "API rate limit exceeded for user" >&2; exit 1 ;;
  primary-once)
    n=\$(wc -l < "\$FAKE_LOG")
    if [ "\$n" -le 1 ]; then echo "HTTP 429: too many" >&2; exit 1; fi
    echo '[]' ;;
  secondary) echo "You have exceeded a secondary rate limit" >&2; exit 1 ;;
  auth) echo "To get started, please run: $cli auth login" >&2; exit 1 ;;
  fail) echo "boom: not found" >&2; exit 7 ;;
esac
FAKE
  chmod +x "$TMP_TC/fake/$cli"
done

(
  cd "$TMP_TC/proj"
  export FAKE_LOG="$TMP_TC/log"
  TC() { : > "$FAKE_LOG"; rm -f "$FAKE_LOG.cwd" "$FAKE_LOG.stdin"; PATH="$TMP_TC/fake:$PATH" hvj tracker call "$@" </dev/null; }
  calls() { wc -l < "$FAKE_LOG" | tr -d ' '; }

  # provider resolution
  out=$(TC -- issue list)
  [ "$(cat "$FAKE_LOG")" = "issue list --limit 1000" ] || fail "config provider github should run gh with --limit 1000 (got: $(cat "$FAKE_LOG"))"
  [ "$(echo "$out" | jget data.provider)" = "github" ] || fail "data.provider should be the config provider: $out"
  [ "$(echo "$out" | jget data.exitCode)" = "0" ] || fail "successful call should report exitCode 0: $out"
  [ "$(echo "$out" | jget data.stdout)" = '[{"n": 0}, {"n": 1}]' ] || fail "data.stdout should carry the CLI's stdout: $out"
  out=$(TC --provider gitlab -- issue list)
  [ "$(cat "$FAKE_LOG")" = "issue list --per-page 100" ] || fail "--provider gitlab should add --per-page 100"
  [ "$(echo "$out" | jget data.provider)" = "gitlab" ] || fail "data.provider should be the --provider flag: $out"
  # Text mode prints the CLI's stdout unchanged.
  [ "$(PATH="$TMP_TC/fake:$PATH" "$ROTA_BIN" tracker call -- issue view 3 </dev/null)" = '[{"n": 0}, {"n": 1}]' ] \
    || fail "text mode should print the CLI stdout unchanged"
  pass "provider from config and --provider flag; list limits injected"

  # no double injection
  TC -- issue list -L 5 >/dev/null
  [ "$(cat "$FAKE_LOG")" = "issue list -L 5" ] || fail "explicit -L must not get --limit"
  TC --provider gitlab -- mr list --per-page 20 >/dev/null
  [ "$(cat "$FAKE_LOG")" = "mr list --per-page 20" ] || fail "explicit --per-page must not be doubled"
  TC -- issue view 3 >/dev/null
  [ "$(cat "$FAKE_LOG")" = "issue view 3" ] || fail "non-list commands untouched"
  pass "limits only injected when absent"

  # api paginate
  TC -- api repos/o/r/issues >/dev/null
  [ "$(cat "$FAKE_LOG")" = "api repos/o/r/issues --paginate" ] || fail "GET api should paginate"
  TC -- api -X POST repos/o/r/issues >/dev/null
  if grep -q paginate "$FAKE_LOG"; then fail "POST api must not paginate"; fi
  TC -- api repos/o/r/issues -f title=x >/dev/null
  if grep -q paginate "$FAKE_LOG"; then fail "api with -f must not paginate"; fi
  TC -- api repos/o/r/issues --paginate >/dev/null
  [ "$(cat "$FAKE_LOG")" = "api repos/o/r/issues --paginate" ] || fail "--paginate must not be doubled"
  pass "--paginate only on GET api"

  # stdin forwarded; cwd restored to caller's
  mkdir -p sub; : > "$FAKE_LOG"; rm -f "$FAKE_LOG.cwd"
  (cd sub && echo "body text" | PATH="$TMP_TC/fake:$PATH" "$ROTA_BIN" tracker call -- issue create -F - >/dev/null)
  [ "$(cat "$FAKE_LOG.stdin")" = "body text" ] || fail "stdin should reach the CLI"
  [ "$(cat "$FAKE_LOG.cwd")" = "cwd:$(pwd -P)/sub" ] || fail "CLI must run in the caller's cwd (got $(cat "$FAKE_LOG.cwd"))"
  pass "stdin forwarded; CLI runs in caller's cwd"

  # An inherited stdin that never closes must not block a call that takes no `-` argument.
  mkfifo "$TMP_TC/held"; exec 9<>"$TMP_TC/held"
  rc=0; PATH="$TMP_TC/fake:$PATH" timeout 10 "$ROTA_BIN" tracker call -- issue view 3 <"$TMP_TC/held" >/dev/null || rc=$?
  exec 9>&-
  [ "$rc" = 0 ] || fail "open stdin pipe should not block a call without '-' (rc=$rc)"
  pass "stdin only read when an argument takes it"

  # truncation warning
  out=$(FAKE_N=1000 TC -- issue list 2>/dev/null)
  echo "$out" | jget data.stderr | grep "hit the list limit (1000)" >/dev/null || fail "expected truncation warning in data.stderr: $out"
  out=$(FAKE_N=3 TC -- issue list 2>/dev/null)
  if echo "$out" | jget data.stderr | grep "list limit" >/dev/null; then fail "no warning below the limit"; fi
  pass "truncation warning at the limit only"

  # rate limits
  rc=0; FAKE_MODE=primary-once TC -- issue list >/dev/null 2>&1 || rc=$?
  [ "$rc" = 0 ] && [ "$(calls)" = 2 ] || fail "primary-once: want success after 2 calls (rc=$rc, calls=$(calls))"
  rc=0; out=$(FAKE_MODE=primary TC -- issue list 2>/dev/null) || rc=$?
  [ "$rc" = 6 ] && [ "$(calls)" = 2 ] || fail "primary: want exit 6 after 2 calls (rc=$rc, calls=$(calls))"
  [ "$(echo "$out" | jget error.code)" = "retry" ] || fail "primary: want error code retry: $out"
  rc=0; out=$(FAKE_MODE=secondary TC -- issue list 2>/dev/null) || rc=$?
  [ "$rc" = 6 ] && [ "$(calls)" = 1 ] || fail "secondary: want exit 6 after 1 call (rc=$rc, calls=$(calls))"
  pass "primary retries once; secondary stops at once; both exit 6 (retry)"

  # auth, plain failure
  rc=0; out=$(FAKE_MODE=auth TC -- issue list 2>/dev/null) || rc=$?
  [ "$rc" = 5 ] || fail "auth failure should exit 5 (got $rc)"
  [ "$(echo "$out" | jget error.code)" = "unavailable" ] || fail "auth failure: want error code unavailable: $out"
  rc=0; out=$(FAKE_MODE=fail TC -- issue view 9 2>/dev/null) || rc=$?
  [ "$rc" = 1 ] && [ "$(calls)" = 1 ] || fail "plain failure should exit 1 after one call (rc=$rc)"
  [ "$(echo "$out" | jget data.exitCode)" = "7" ] || fail "data.exitCode should carry the CLI's exit code: $out"
  echo "$out" | jget data.stderr | grep "boom: not found" >/dev/null || fail "data.stderr should carry the CLI's stderr: $out"
  pass "auth failure exits 5; plain failure exits 1 with the CLI's exit code and stderr in data"

  # missing CLI: PATH with python3/coreutils but no gh/glab
  mkdir -p "$TMP_TC/nobin"
  for t in python3 bash env dirname cat wc tr git; do ln -sf "$(command -v $t)" "$TMP_TC/nobin/$t"; done
  rc=0; PATH="$TMP_TC/nobin" "$ROTA_BIN" tracker call -- issue list </dev/null >/dev/null 2>&1 || rc=$?
  [ "$rc" = 5 ] || fail "missing CLI should exit 5 (got $rc)"
  pass "missing CLI exits 5"

  # no provider: no config, no remote
  mkdir -p "$TMP_TC/noprov/.rota" && echo '{}' > "$TMP_TC/noprov/.rota/config.json"
  rc=0; (cd "$TMP_TC/noprov" && PATH="$TMP_TC/fake:$PATH" "$ROTA_BIN" tracker call -- issue list </dev/null >/dev/null 2>&1) || rc=$?
  [ "$rc" = 5 ] || fail "no resolvable provider should exit 5 (got $rc)"
  pass "unresolvable provider exits 5"

  # usage errors
  rc=0; "$ROTA_BIN" tracker call </dev/null --provider 2>/dev/null || rc=$?
  [ "$rc" = 2 ] || fail "bare trailing --provider must exit 2 (got $rc)"
  rc=0; "$ROTA_BIN" tracker call </dev/null --provider github 2>/dev/null || rc=$?
  [ "$rc" = 2 ] || fail "missing CLI args should exit 2 (got $rc)"
  rc=0; "$ROTA_BIN" tracker call </dev/null --provider bogus -- issue list 2>/dev/null || rc=$?
  [ "$rc" = 2 ] || fail "unknown provider value should exit 2 (got $rc)"
  pass "usage errors exit 2"
)

trap 'rm -rf "$TMP"' EXIT
