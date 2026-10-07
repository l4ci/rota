echo "C6: rota doctor reports each preflight check and exits 1 on any failure"

# Tool lookup is ROTA_TEST_DOCTOR_PATH, so these fakes stand in for herdr and the
# forge CLIs without ever touching the real ones. Only git is the real one
# (symlinked in): doctor's git calls are read-only.
TMP_DR="$(mktemp -d)"
trap 'rm -rf "$TMP_DR"' EXIT

DR_BIN="$TMP_DR/bin"
mkdir -p "$DR_BIN"
ln -s "$(command -v git)" "$DR_BIN/git"; printf '#!/bin/sh\nexit 0\n' > "$DR_BIN/jq"; chmod +x "$DR_BIN/jq"
dr_herdr() { # dr_herdr <version>: a fake herdr; "installed" in CLAUDE_CONFIG_DIR means the claude hook is current
  cat >"$DR_BIN/herdr" <<FAKE
#!/bin/sh
case "\$1 \$2" in
  "--version "*) echo "herdr $1" ;;
  "integration status")
    if [ -f "\$CLAUDE_CONFIG_DIR/installed" ]; then echo "claude: current (v10) (/x/hook.sh)"; else echo "claude: not installed (/x/hook.sh)"; fi
    echo "codex: current (v8) (/x/codex.sh)" ;;
  *) exit 99 ;;
esac
FAKE
  chmod +x "$DR_BIN/herdr"
}
dr_herdr 0.9.3

dr_field() { # dr_field <check> <field>: one field of one check from the envelope on stdin
  python3 -c '
import json,sys
cs={c["name"]:c for c in json.load(sys.stdin)["data"]["checks"]}
print(cs[sys.argv[1]].get(sys.argv[2],"ABSENT"))' "$1" "$2"
}
dr_ok() { python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["ok"])'; }
# The skills check reads ~/.claude/skills and ~/.agents/skills: point HOME and
# CLAUDE_CONFIG_DIR at an empty sandbox so it never sees the real ones. (An
# account's configDir still reaches the fake herdr; doctor sets it per call.)
mkdir -p "$TMP_DR/home" "$TMP_DR/claude"
# Host detection reads HERDR_ENV and TMUX, so clear them: running the suite inside a pane must not change the result.
# DR_HERDR=1 or DR_TMUX=<value> in front of dr_run fakes a pane.
dr_run() { # dr_run <dir>: doctor in <dir> against the fakes; prints the envelope, returns the exit code
  env -u HERDR_ENV -u TMUX ${DR_HERDR:+HERDR_ENV=1} ${DR_TMUX:+TMUX=$DR_TMUX} HOME="$TMP_DR/home" CLAUDE_CONFIG_DIR="$TMP_DR/claude" ROTA_TEST_DOCTOR_PATH="$DR_BIN" "$ROTA_BIN" --json -C "$1" doctor 2>/dev/null
}

# (a) a herdr project with one account and the hook installed: all pass or skip
mkdir -p "$TMP_DR/acct" "$TMP_DR/proj/.rota"
echo '{}' >"$TMP_DR/acct/.credentials.json"
: >"$TMP_DR/acct/installed"
printf '.worktrees/\n' >"$TMP_DR/proj/.gitignore"
printf '{"work":{"dispatch":"herdr","accounts":[{"name":"a","configDir":"%s"}]}}\n' "$TMP_DR/acct" >"$TMP_DR/proj/.rota/config.json"
# D1 (#65): the orchestrator hooks are opt-in, so a project that has not installed them skips both checks
git -C "$TMP_DR/proj" init -q
rc=0; OUT="$(dr_run "$TMP_DR/proj")" || rc=$?
[ "$rc" -eq 0 ] || fail "C6[a]: healthy project exited $rc: $OUT"
[ "$(printf '%s' "$OUT" | dr_ok)" = "True" ] || fail "C6[a]: ok is not true: $OUT"
NAMES="$(printf '%s' "$OUT" | python3 -c 'import json,sys; print(",".join(c["name"] for c in json.load(sys.stdin)["data"]["checks"]))')"
[ "$NAMES" = "git,jq,host,tracker,accounts,hook,statusline,stop-hook,switch,skills,codex,agents,verify" ] || fail "C6[a]: checks were: $NAMES"
for pair in git:pass host:pass tracker:skip accounts:pass hook:pass statusline:skip stop-hook:skip skills:skip codex:skip; do
  [ "$(printf '%s' "$OUT" | dr_field "${pair%%:*}" status)" = "${pair##*:}" ] || fail "C6[a]: ${pair%%:*} was not ${pair##*:}: $OUT"
done
case "$OUT" in *'"changed"'*) fail "C6[a]: doctor reports changed: $OUT" ;; esac
pass "C6[a]: a healthy herdr project passes in contract order, with no changed"

# (b) hook not installed for the account: fail with the install hint, same data on exit 1
rm "$TMP_DR/acct/installed"
rc=0; OUT="$(dr_run "$TMP_DR/proj")" || rc=$?
[ "$rc" -eq 1 ] || fail "C6[b]: missing hook exited $rc, not 1"
[ "$(printf '%s' "$OUT" | dr_ok)" = "False" ] || fail "C6[b]: ok is not false: $OUT"
[ "$(printf '%s' "$OUT" | dr_field hook status)" = "fail" ] || fail "C6[b]: hook did not fail: $OUT"
[ "$(printf '%s' "$OUT" | dr_field hook hint)" = "herdr integration install claude" ] || fail "C6[b]: wrong hook hint: $OUT"
: >"$TMP_DR/acct/installed"
pass "C6[b]: an uninstalled hook fails with the install hint"

# (c) wrong herdr minor: host fails and names what it found
dr_herdr 0.8.2
rc=0; OUT="$(dr_run "$TMP_DR/proj")" || rc=$?
[ "$rc" -eq 1 ] || fail "C6[c]: herdr 0.8.2 exited $rc, not 1"
[ "$(printf '%s' "$OUT" | dr_field host detail)" = "herdr 0.8.2, need 0.9.x" ] || fail "C6[c]: wrong host detail: $OUT"
dr_herdr 0.9.3
pass "C6[c]: herdr 0.8.2 fails the host check"

# (d) herdr hidden: host fails, hook is skipped (nothing to run), a missing tool is exit 1 not 5
mv "$DR_BIN/herdr" "$TMP_DR/herdr.away"
rc=0; OUT="$(dr_run "$TMP_DR/proj")" || rc=$?
[ "$rc" -eq 1 ] || fail "C6[d]: missing herdr exited $rc, not 1"
[ "$(printf '%s' "$OUT" | dr_field host status)" = "fail" ] || fail "C6[d]: host did not fail: $OUT"
[ "$(printf '%s' "$OUT" | dr_field hook status)" = "skip" ] || fail "C6[d]: hook did not skip: $OUT"
mv "$TMP_DR/herdr.away" "$DR_BIN/herdr"
pass "C6[d]: a missing herdr is a failed check, not an unavailable dependency"

# (e) account without credentials fails and says where
rm "$TMP_DR/acct/.credentials.json"
rc=0; OUT="$(dr_run "$TMP_DR/proj")" || rc=$?
[ "$rc" -eq 1 ] || fail "C6[e]: missing credentials exited $rc, not 1"
[ "$(printf '%s' "$OUT" | dr_field accounts status)" = "fail" ] || fail "C6[e]: accounts did not fail: $OUT"
pass "C6[e]: an account with no credentials file fails"

# (f) no .rota/ at all: runs on defaults; no host, accounts or hook to check
mkdir -p "$TMP_DR/bare"
printf '.worktrees/\n' >"$TMP_DR/bare/.gitignore"
git -C "$TMP_DR/bare" init -q
rc=0; OUT="$(dr_run "$TMP_DR/bare")" || rc=$?
[ "$rc" -eq 0 ] || fail "C6[f]: doctor without .rota/ exited $rc: $OUT"
for n in host accounts hook statusline stop-hook; do
  [ "$(printf '%s' "$OUT" | dr_field "$n" status)" = "skip" ] || fail "C6[f]: $n did not skip without .rota/: $OUT"
done
pass "C6[f]: doctor runs without .rota/ on defaults"

# (g) no repo scope
rc=0; HOME="$TMP_DR/home" CLAUDE_CONFIG_DIR="$TMP_DR/claude" ROTA_TEST_DOCTOR_PATH="$DR_BIN" "$ROTA_BIN" --json -C "$TMP_DR/bare" doctor --repo x >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 2 ] || fail "C6[g]: --repo exited $rc, not 2"
pass "C6[g]: doctor rejects --repo"

# (h) host detection (#236): init's default work.dispatch=subagent still gets the herdr checks inside a herdr pane
printf '{"work":{"dispatch":"subagent","accounts":[{"name":"a","configDir":"%s"}]}}\n' "$TMP_DR/acct" >"$TMP_DR/proj/.rota/config.json"
echo '{}' >"$TMP_DR/acct/.credentials.json"
rc=0; OUT="$(dr_run "$TMP_DR/proj")" || rc=$?
[ "$(printf '%s' "$OUT" | dr_field host status)" = "skip" ] || fail "C6[h]: subagent outside a pane did not skip host: $OUT"
[ "$(printf '%s' "$OUT" | dr_field hook status)" = "skip" ] || fail "C6[h]: subagent outside a pane did not skip hook: $OUT"
rc=0; OUT="$(DR_HERDR=1 dr_run "$TMP_DR/proj")" || rc=$?
[ "$(printf '%s' "$OUT" | dr_field host status)" = "pass" ] || fail "C6[h]: herdr pane did not pass host: $OUT"
[ "$(printf '%s' "$OUT" | dr_field host detail)" = "herdr 0.9.3" ] || fail "C6[h]: wrong host detail in a herdr pane: $OUT"
[ "$(printf '%s' "$OUT" | dr_field hook status)" = "pass" ] || fail "C6[h]: herdr pane did not run hook: $OUT"
rm "$TMP_DR/acct/installed"
rc=0; OUT="$(DR_HERDR=1 dr_run "$TMP_DR/proj")" || rc=$?
[ "$rc" -eq 1 ] || fail "C6[h]: uninstalled hook in a herdr pane exited $rc, not 1"
[ "$(printf '%s' "$OUT" | dr_field hook status)" = "fail" ] || fail "C6[h]: uninstalled hook in a herdr pane did not fail: $OUT"
: >"$TMP_DR/acct/installed"
printf '#!/bin/sh\nexit 0\n' >"$DR_BIN/tmux"; chmod +x "$DR_BIN/tmux"
rc=0; OUT="$(DR_TMUX=/tmp/tmux-1/default,1,0 dr_run "$TMP_DR/proj")" || rc=$?
[ "$(printf '%s' "$OUT" | dr_field host detail)" = "tmux on PATH" ] || fail "C6[h]: tmux session not detected: $OUT"
rm "$DR_BIN/tmux"
pass "C6[h]: doctor detects the host like the round verbs when work.dispatch is subagent"

trap 'rm -rf "$TMP"' EXIT
