echo "worker reset — slot reset guard, provable session close, resume-flag rejection"
# Covers #38. A slot is reused across tasks; dispatch must (a) refuse a slot
# holding uncommitted or unmerged work, (b) otherwise cut a fresh per-task
# branch from the cycle branch, (c) refuse to spawn when the old session cannot
# be confirmed closed, (d) reject a workerCommand that resumes a conversation.
# A slot holding work and a resume flag are refusals (exit 4); a session that
# will not close is the host failing (exit 5).
# Hosts are FAKES on PATH: herdr with a stateful tab, tmux with a stateful window.

TMP_RG="$(mktemp -d)"
trap 'rm -rf "$TMP_RG"' EXIT
FK="$TMP_RG/fake"
mkdir -p "$FK/bin"

cat > "$FK/bin/herdr" <<'SH'
#!/usr/bin/env bash
F="$FAKE_HERDR"
printf '%s\n' "$*" >>"$F/log"
err() { printf '{"error":{"code":"%s","message":"fake"},"id":"cli"}\n' "$1" >&2; exit 1; }
agent_json() {
  printf '{"id":"cli","result":{"type":"%s","agent":{"agent":"claude","agent_status":"%s","pane_id":"w9:p11","tab_id":"w9:t7","focused":false}}}\n' "$1" "$2"
}
case "$1 $2" in
  "tab create") touch "$F/tab_alive"
    printf '{"id":"cli","result":{"type":"tab_created","tab":{"tab_id":"w9:t7"},"root_pane":{"pane_id":"w9:p17"}}}\n' ;;
  "tab close") [ -f "$F/dirty_on_close" ] && echo wip > "$(cat "$F/dirty_on_close")"
    [ -f "$F/close_fails" ] || rm -f "$F/tab_alive"; echo '{"id":"cli","result":{"type":"ok"}}' ;;
  "tab get") [ -f "$F/tab_alive" ] || err tab_not_found; echo '{"id":"cli","result":{"type":"tab_info"}}' ;;
  "pane process-info")
    procs=""; [ -f "$F/pid" ] && procs="{\"pid\":$(cat "$F/pid")}"
    printf '{"id":"cli","result":{"process_info":{"foreground_processes":[%s]}}}\n' "$procs" ;;
  "agent start") agent_json agent_started idle ;;
  "agent get") agent_json agent_info idle ;;
  "agent wait") agent_json agent_info idle ;;
  "agent prompt") agent_json agent_prompted working ;;
  *) err unknown_method ;;
esac
SH
cat > "$FK/bin/tmux" <<'SH'
#!/usr/bin/env bash
F="$FAKE_TMUX"
printf '%s\n' "$*" >>"$F/log"
case "$1" in
  list-windows) [ -f "$F/window" ] && echo "w1 $(cat "$F/pid")" || true ;;
  kill-window)  [ -f "$F/kill_fails" ] || rm -f "$F/window" ;;
  has-session)  exit 0 ;;
esac
exit 0
SH
chmod +x "$FK/bin/herdr" "$FK/bin/tmux"

mkdir -p "$TMP_RG/repo/.rota"
(
  cd "$TMP_RG/repo"
  git init -q -b main .
  git config user.email t@t
  git config user.name t
  echo seed > seed.txt
  git add seed.txt
  git commit -q -m seed
) || fail "reset-guard fixture repo setup failed"

rg()  { ( cd "$TMP_RG/repo" && "$@" ); }
rgh() { ( cd "$TMP_RG/repo" && PATH="$FK/bin:$PATH" FAKE_HERDR="$FK" HERDR_ENV=1 HERDR_WORKSPACE_ID=w9 ROTA_HOST_KILL_WAIT=1 "$@" ); }
rgt() { ( cd "$TMP_RG/repo" && PATH="$FK/bin:$PATH" FAKE_TMUX="$FK" ROTA_HOST_KILL_WAIT=1 "$@" ); }
slot_field() {
  python3 -c 'import json,sys; s=[s for s in json.load(open(sys.argv[1]))["slots"] if s["name"]==sys.argv[2]][0]; print(s.get(sys.argv[3]))' \
    "$TMP_RG/repo/.rota/workers.json" "$1" "$2"
}
cfg() { printf '{"work":{"dispatch":"%s"%s}}\n' "$1" "${2:+,\"workerCommand\":\"$2\"}" > "$TMP_RG/repo/.rota/config.json"; }

rg hvj worker pool init --slots 1 --base main >/dev/null 2>&1 || fail "pool init failed"
WT="$(slot_field w1 worktree)"
echo brief > "$TMP_RG/brief.md"
cfg herdr

# ── (a) refuse a slot that holds work ──────────────────────────────────────
echo wip > "$WT/scratch.txt"
RC=0; OUT="$(rg hvj worker reset w1 --task T1 2>/dev/null)" || RC=$?
[ "$RC" = "4" ] || fail "an untracked file must refuse the slot (exit 4), got $RC"
case "$(jget 'data.dirty' <<<"$OUT")" in *scratch.txt*) ;; *) fail "refusal data must name the dirty path, got: $OUT" ;; esac
[ "$(jget 'data.changed' <<<"$OUT")" = "false" ] || fail "a refused reset changes nothing: $OUT"
RC=0; OUT="$(rg hvj worker reset w1 --task T1 --check-only 2>/dev/null)" || RC=$?
[ "$RC" = "1" ] || fail "--check-only on a slot holding work must exit 1, got $RC"
[ "$(jget 'data.clean' <<<"$OUT")" = "false" ] || fail "--check-only verdict must be clean:false, got: $OUT"
[ "$(git -C "$WT" rev-parse --abbrev-ref HEAD)" = "rota-worker/w1" ] || fail "a refused reset must not move the branch"
rm "$WT/scratch.txt"

echo more >> "$WT/seed.txt"
RC=0; rg hvj worker reset w1 --task T1 >/dev/null 2>&1 || RC=$?
[ "$RC" = "4" ] || fail "a modified tracked file must refuse the slot, got $RC"
git -C "$WT" commit -q -am "worker work"
RC=0; OUT="$(rg hvj worker reset w1 --task T1 2>/dev/null)" || RC=$?
[ "$RC" = "4" ] || fail "an unmerged commit must refuse the slot, got $RC"
case "$(jget 'data.unmerged' <<<"$OUT")" in *"worker work"*) ;; *) fail "refusal data must list the unmerged commit, got: $OUT" ;; esac
pass "worker reset refuses a slot with uncommitted or unmerged work (exit 4), naming what it found"

# Dispatch refuses before it touches the session.
: >"$FK/log"
RC=0; rgh hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T1 >/dev/null 2>&1 || RC=$?
[ "$RC" = "4" ] || fail "dispatch into a slot with unmerged work must exit 4, got $RC"
if grep '^tab \|^agent ' "$FK/log" >/dev/null; then fail "a refused dispatch must not kill or spawn anything; log: $(cat "$FK/log")"; fi
pass "worker dispatch exits 4 on a dirty slot without touching its session"

# ── (b) clean slot gets a fresh branch from the cycle branch ────────────────
git -C "$TMP_RG/repo" merge -q rota-worker/w1 || fail "fixture merge failed"
git -C "$TMP_RG/repo" commit -q --allow-empty -m "landed since" || true
OUT="$(rg hvj worker reset w1 --task B07 2>/dev/null)" || fail "a merged slot must reset cleanly: $OUT"
[ "$(jget 'data.branch' <<<"$OUT")" = "rota-worker/w1-b07" ] || fail "reset data must name the new branch, got: $OUT"
[ "$(jget 'data.changed' <<<"$OUT")" = "true" ] || fail "a real reset reports changed:true: $OUT"
[ "$(jget 'data.sha' <<<"$OUT")" = "$(git -C "$TMP_RG/repo" rev-parse --short=7 main)" ] || fail "reset data sha must be the base tip: $OUT"
rg hvj worker reset w1 --task B07 --check-only >/dev/null 2>&1 || fail "--check-only must exit 0 on a clean slot"
[ "$(git -C "$WT" rev-parse --abbrev-ref HEAD)" = "rota-worker/w1-b07" ] || fail "slot is on $(git -C "$WT" rev-parse --abbrev-ref HEAD), expected rota-worker/w1-b07"
[ "$(git -C "$WT" rev-parse HEAD)" = "$(git -C "$TMP_RG/repo" rev-parse main)" ] || fail "fresh branch must start at the tip of the cycle branch"
[ "$(slot_field w1 branch)" = "rota-worker/w1-b07" ] || fail "registry must record the per-task branch for worker gate"
git -C "$TMP_RG/repo" rev-parse --verify --quiet rota-worker/w1 >/dev/null && fail "the proved-merged previous branch should be deleted"
rg hvj worker pool init --slots 1 --base main >/dev/null 2>&1 || fail "re-init failed"
[ "$(slot_field w1 branch)" = "rota-worker/w1-b07" ] || fail "re-running pool init must not rewind the slot's branch"
pass "a clean slot is cut a fresh per-task branch from the cycle branch; registry and worker pool init agree"

# ── (c) provable close ──────────────────────────────────────────────────────
rgh hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T2 >/dev/null || fail "first herdr dispatch failed"
[ "$(slot_field w1 handle)" = "w9:t7" ] || fail "dispatch did not record the tab"

touch "$FK/close_fails"; : >"$FK/log"
RC=0; OUT="$(rgh hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T3 2>/dev/null)" || RC=$?
[ "$RC" = "5" ] || fail "herdr: a tab that will not close must exit 5, got $RC: $OUT"
if grep '^tab create' "$FK/log" >/dev/null; then fail "herdr: must not spawn a second session when the first would not close"; fi
rm -f "$FK/close_fails"

sleep 300 & SLEEPER=$!
echo "$SLEEPER" > "$FK/pid"
RC=0; OUT="$(rgh hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T3 2>/dev/null)" || RC=$?
kill "$SLEEPER" 2>/dev/null || true
[ "$RC" = "5" ] || fail "herdr: a surviving agent process must exit 5 even when the tab is gone, got $RC: $OUT"
case "$(jget error.message <<<"$OUT")" in *"$SLEEPER"*) ;; *) fail "herdr: refusal must name the surviving pid, got: $OUT" ;; esac
rm -f "$FK/pid"
touch "$FK/tab_alive"
rgh hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T3 >/dev/null \
  || fail "herdr: dispatch must succeed once the old tab closes and its pids are gone"

# tmux twin: first the window refuses to die, then it dies but a pid survives.
cfg tmux
python3 - "$TMP_RG/repo/.rota/workers.json" <<'PY'
import json, sys
p = sys.argv[1]; d = json.load(open(p))
d["slots"][0]["handle"] = "rota:w1"; json.dump(d, open(p, "w"))
PY
sleep 300 & SLEEPER=$!
echo "$SLEEPER" > "$FK/pid"
touch "$FK/window" "$FK/kill_fails"; : >"$FK/log"
RC=0; OUT="$(rgt hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T4 2>/dev/null)" || RC=$?
[ "$RC" = "5" ] || fail "tmux: a window that will not die must exit 5, got $RC: $OUT"
if grep '^new-window' "$FK/log" >/dev/null; then fail "tmux: must not spawn a second window"; fi
rm -f "$FK/kill_fails"; touch "$FK/window"
RC=0; OUT="$(rgt hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T4 2>/dev/null)" || RC=$?
[ "$RC" = "5" ] || fail "tmux: a surviving pane process must exit 5 even when the window is gone, got $RC: $OUT"
case "$(jget error.message <<<"$OUT")" in *"$SLEEPER"*) ;; *) fail "tmux: refusal must name the surviving pid $SLEEPER, got: $OUT" ;; esac
kill "$SLEEPER" 2>/dev/null || true; wait "$SLEEPER" 2>/dev/null || true
pass "dispatch exits 5 and spawns nothing when the old tab/window (or its pids) is not confirmed gone"

# ── (d) resume flags and unparseable commands ───────────────────────────────
cfgj() { python3 -c 'import json,sys; print(json.dumps({"work":{"dispatch":"herdr","workerCommand":sys.argv[1]}}))' "$1" > "$TMP_RG/repo/.rota/config.json"; }
dispatch_rc() { RC=0; OUT="$(rgh hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T5 2>/dev/null)" || RC=$?; }
for CMD in "claude --model sonnet --continue" "claude -r" "claude -c" "claude --resume" "claude --resume=abc" \
           "claude --continue=1" "claude -cr" 'sh -c "claude -c"' "env X=1 claude -c" "X=1 claude --resume abc"; do
  cfgj "$CMD"; dispatch_rc
  [ "$RC" = "4" ] || fail "workerCommand '$CMD' must be refused (exit 4), got $RC: $OUT"
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "refusing '$CMD' changes nothing: $OUT"
done
cfgj 'claude --model "sonnet'; dispatch_rc
[ "$RC" = "2" ] || fail "an unbalanced quote must exit 2, not crash with 1, got $RC"
# A wrapper's own -c is not a resume flag: the command is judged by what follows claude.
cfgj "my-wrapper -c --x"; dispatch_rc
[ "$RC" = "5" ] || fail "a wrapper without claude passes the resume check and fails herdr's own claude check (exit 5), got $RC: $OUT"
cfgj "claude --model sonnet -p"; dispatch_rc
[ "$RC" = "0" ] || fail "a command with no resume flag must dispatch, got $RC: $OUT"
pass "worker dispatch refuses resume flags in every spelling, even inside sh -c or env, and unparseable commands"

# ── (e) retry of the slot's own task keeps its WIP ──────────────────────────
cfg herdr
echo wip > "$WT/retry.txt"; git -C "$WT" add retry.txt; git -C "$WT" commit -q -m "task wip"; echo dirty > "$WT/dirty.txt"
BR="$(git -C "$WT" symbolic-ref --short HEAD)"; HEADSHA="$(git -C "$WT" rev-parse HEAD)"
OUT="$(rg hvj worker reset w1 --task T5 2>/dev/null)" || fail "retry of the slot's own task must not be refused: $OUT"
[ "$(jget data.retained <<<"$OUT")" = "true" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "a retry reports retained:true, changed:false: $OUT"
[ "$(git -C "$WT" rev-parse HEAD)" = "$HEADSHA" ] && [ -f "$WT/dirty.txt" ] || fail "a retry must keep the branch's commits and edits"
RC=0; rg hvj worker reset w1 --task OTHER >/dev/null 2>&1 || RC=$?
[ "$RC" = "4" ] || fail "a different task must still be refused, got $RC"
rm "$WT/dirty.txt"; git -C "$WT" reset -q --hard main
pass "re-dispatching the slot's own task continues it in place; any other task is still refused"

# ── (f) the session died but the second reset refused: handle must not go stale
[ "$(slot_field w1 handle)" != "None" ] || fail "fixture: slot should hold a handle here"
touch "$FK/tab_alive"; echo "$WT/raced.txt" > "$FK/dirty_on_close"
RC=0; OUT="$(rgh hvj worker dispatch w1 --body-file "$TMP_RG/brief.md" --task T7 2>/dev/null)" || RC=$?
rm -f "$FK/dirty_on_close" "$WT/raced.txt"
[ "$RC" = "4" ] || fail "a reset refused after the kill must exit 4, got $RC: $OUT"
[ "$(slot_field w1 handle)" = "None" ] || fail "the dead session's handle must be cleared, got $(slot_field w1 handle)"
[ "$(slot_field w1 state)" = "idle" ] || fail "the slot must not stay busy with no session, got $(slot_field w1 state)"
pass "a failure after the kill clears the slot's stale handle"

# ── (g) a detached worktree is not registered as branch 'HEAD' ──────────────
git -C "$WT" switch -q --detach
BEFORE_BR="$(slot_field w1 branch)"
rg hvj worker pool init --slots 1 --base main >/dev/null 2>&1 || fail "re-init on a detached worktree failed"
[ "$(slot_field w1 branch)" = "$BEFORE_BR" ] && [ "$(slot_field w1 branch)" != "HEAD" ] \
  || fail "pool init registered '$(slot_field w1 branch)' for a detached worktree, expected $BEFORE_BR (registry value kept)"
pass "worker pool init keeps the slot's branch when its worktree is detached"

trap 'rm -rf "$TMP"' EXIT
pass "worker reset guard contract"
