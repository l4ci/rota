echo "worker pool init: work.envSetup runs per slot, skips on unchanged lockfiles, fails fast (#398)"

TMP_ES="$(mktemp -d "$TMP/envsetup.XXXXXX")"
ESP="$TMP_ES/proj"
mkdir -p "$ESP/.rota"
printf '.worktrees/\n' > "$ESP/.gitignore"
# The command appends one line per run to a log outside the repo and records its cwd.
printf '{"work":{"envSetup":"echo \\"$PWD\\" >> %s/runs.log"}}\n' "$TMP_ES" > "$ESP/.rota/config.json"
(
  cd "$ESP" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add .gitignore seed.txt .rota/config.json && git commit -q -m seed
) || fail "env-setup fixture repo setup failed"
esrun() { ( cd "$ESP" && "$ROTA_BIN" --json "$@" 2>&1 ); }
esruns() { if [ -f "$TMP_ES/runs.log" ]; then wc -l < "$TMP_ES/runs.log" | tr -d ' '; else echo 0; fi; }

RC=0; esrun worker pool init --slots 2 --base main > /dev/null || RC=$?
[ "$RC" = "0" ] && [ "$(esruns)" = "2" ] || fail "init should run setup once per new slot: rc=$RC runs=$(esruns)"
grep -q "/.worktrees/w1\$" "$TMP_ES/runs.log" || fail "setup should run with the slot worktree as cwd: $(cat "$TMP_ES/runs.log")"
pass "setup runs once in each new slot worktree"

esrun worker pool init --slots 2 --base main > /dev/null
[ "$(esruns)" = "2" ] || fail "unchanged lockfiles should skip setup: runs=$(esruns)"
pass "an unchanged lockfile hash skips setup"

echo "lock v1" > "$ESP/.worktrees/w1/package-lock.json"
esrun worker pool init --slots 2 --base main > /dev/null
[ "$(esruns)" = "3" ] || fail "a changed lockfile should rerun setup for that slot only: runs=$(esruns)"
pass "a changed lockfile reruns setup for that slot"

printf '{"work":{"envSetup":"echo broken; exit 7"}}\n' > "$ESP/.rota/config.json"
RC=0; OUT=$(esrun worker pool init --slots 3 --base main) || RC=$?
[ "$RC" = "1" ] || fail "a red setup should fail pool init with exit 1: rc=$RC $OUT"
case "$OUT" in *"slot w1"*"exit 7"*) ;; *) fail "the failure should name the slot and exit code: $OUT" ;; esac
case "$OUT" in *"exit 7"*"echo broken"*) ;; *) fail "the failure should name the command: $OUT" ;; esac
pass "a red setup fails pool init fast, naming slot and command"

RC=0; esrun worker pool init --slots 3 --base main > /dev/null || RC=$?
[ "$RC" = "1" ] || fail "no hash should be stored for the red run, so it retries and fails again: rc=$RC"
pass "a failed setup stores no hash"

rm -rf "$TMP_ES"
