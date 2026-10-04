echo "C6: rota reap previews by default, never touches a live or held thing, and --apply removes the rest"

# Everything lives in one fresh mktemp dir. ROTA_TEST_REAP_HOST stands in for
# herdr: no real host is ever asked, so no real tab or agent can be touched.
TMP_RP="$(mktemp -d)"
trap 'rm -rf "$TMP_RP"' EXIT

RP="$TMP_RP/proj"
mkdir -p "$RP/.rota"
rp_git() { git -C "$RP" -c user.email=a@b -c user.name=n "$@"; }
git init -q -b main "$RP"
rp_git commit -q --allow-empty -m init
rp_git worktree add -q -b kit/1-old "$RP/.worktrees/old" HEAD       # clean and merged, no agent: a candidate
rp_git worktree add -q -b kit/2-dirty "$RP/.worktrees/dirty" HEAD   # unowned but holds work
rp_git worktree add -q -b kit/3-live "$RP/.worktrees/live" HEAD     # an unregistered worker with a live agent
rp_git worktree add -q -b park/ben "$RP/.worktrees/ben" HEAD        # parked, clean
rp_git branch kit/9-gone HEAD                                       # merged, checked out nowhere
echo wip >"$RP/.worktrees/dirty/wip.txt"

FX="$TMP_RP/host.json"
cat >"$FX" <<JSON
{"workspaces":[
  {"id":"w5","tabs":[{"id":"w5:t1","cwd":"$RP/.worktrees/old","agent":false}]},
  {"id":"w6","tabs":[{"id":"w6:t1","cwd":"$RP/.worktrees/ben","agent":false}]},
  {"id":"w3","tabs":[{"id":"w3:t1","cwd":"$RP/.worktrees/live","agent":true}]}],
 "agents":[{"tab":"w3:t1","name":"live","cwd":"$RP/.worktrees/live","status":"working"}],
 "processes":[
  {"pid":4242,"name":"npm","tab":"w5:t1","cwd":"$RP/.worktrees/old"},
  {"pid":4343,"name":"node","tab":"w3:t1","cwd":"$RP/.worktrees/live"}]}
JSON

rp_run() { ROTA_TEST_REAP_HOST="${RP_FX-$FX}" "$ROTA_BIN" --json -C "$RP" reap "$@" 2>/dev/null; }
rp_ids() { python3 -c '
import json,sys
d=json.load(sys.stdin)["data"]
print(",".join(c["id"]+("!" if "held" in c else "") for c in d["candidates"]))'; }
rp_field() { python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["data"][sys.argv[1]]))' "$1"; }

# (a) preview: lists the safe and the held, lists nothing live or parked, deletes nothing
rc=0; OUT="$(rp_run)" || rc=$?
[ "$rc" -eq 0 ] || fail "C6[reap a]: preview exited $rc: $OUT"
WANT="worktree:dirty!,worktree:old,branch:kit/9-gone,tab:w5:t1,process:4242"
[ "$(printf '%s' "$OUT" | rp_ids)" = "$WANT" ] || fail "C6[reap a]: candidates were $(printf '%s' "$OUT" | rp_ids), want $WANT"
case "$OUT" in *'preview only; pass --apply'*) ;; *) fail "C6[reap a]: no preview warning: $OUT" ;; esac
[ "$(printf '%s' "$OUT" | rp_field changed)" = "false" ] || fail "C6[reap a]: preview reports changed: $OUT"
[ -d "$RP/.worktrees/old" ] || fail "C6[reap a]: preview removed a worktree"
[ ! -e "$FX.removed" ] || fail "C6[reap a]: preview touched the host"
pass "C6[reap a]: preview lists candidates, skips live and parked, deletes nothing"

# (b) --kind filter and unknown kind
OUT="$(rp_run --kind branch)"
[ "$(printf '%s' "$OUT" | rp_ids)" = "branch:kit/9-gone" ] || fail "C6[reap b]: --kind branch gave $(printf '%s' "$OUT" | rp_ids)"
rc=0; rp_run --kind bogus >/dev/null || rc=$?
[ "$rc" -eq 2 ] || fail "C6[reap b]: unknown kind exited $rc, not 2"
rc=0; rp_run --repo x >/dev/null || rc=$?
[ "$rc" -eq 2 ] || fail "C6[reap b]: --repo exited $rc, not 2"
pass "C6[reap b]: --kind filters, an unknown kind and --repo exit 2"

# (c) host unavailable (fixture unreadable): git-provable branches only, with a warning
rc=0; OUT="$(RP_FX="$TMP_RP/missing.json" rp_run)" || rc=$?
[ "$rc" -eq 0 ] || fail "C6[reap c]: host-less preview exited $rc"
[ "$(printf '%s' "$OUT" | rp_ids)" = "branch:kit/9-gone" ] || fail "C6[reap c]: host-less candidates were $(printf '%s' "$OUT" | rp_ids)"
case "$OUT" in *'host unavailable'*) ;; *) fail "C6[reap c]: no host warning: $OUT" ;; esac
pass "C6[reap c]: with no host data no worktree, tab or process is listed"

# (d) --apply removes only what has no held; live, dirty, parked stay
rc=0; OUT="$(rp_run --apply)" || rc=$?
[ "$rc" -eq 0 ] || fail "C6[reap d]: apply exited $rc: $OUT"
[ "$(printf '%s' "$OUT" | rp_field reaped)" = '["worktree:old", "branch:kit/9-gone", "tab:w5:t1", "process:4242"]' ] || fail "C6[reap d]: reaped was $(printf '%s' "$OUT" | rp_field reaped)"
[ "$(printf '%s' "$OUT" | rp_field changed)" = "true" ] || fail "C6[reap d]: changed is not true"
[ ! -d "$RP/.worktrees/old" ] || fail "C6[reap d]: the safe worktree is still there"
for n in dirty live ben; do [ -d "$RP/.worktrees/$n" ] || fail "C6[reap d]: worktree $n was removed"; done
[ -f "$RP/.worktrees/dirty/wip.txt" ] || fail "C6[reap d]: held work was deleted"
rp_git rev-parse --verify -q kit/9-gone >/dev/null && fail "C6[reap d]: the merged branch is still there"
for b in kit/2-dirty kit/3-live park/ben; do rp_git rev-parse --verify -q "$b" >/dev/null || fail "C6[reap d]: branch $b was deleted"; done
[ "$(sort "$FX.removed" | tr '\n' ',')" = "process 4242,tab w5:t1," ] || fail "C6[reap d]: host removals were: $(cat "$FX.removed")"
pass "C6[reap d]: --apply removes only unheld candidates and never a live agent's tab or process"

# (e) the next run: the freed branch is now a candidate and the held worktree is still held
OUT="$(rp_run)"
# the fixture file is static, so the host entries remain
[ "$(printf '%s' "$OUT" | rp_ids)" = "worktree:dirty!,branch:kit/1-old,tab:w5:t1,process:4242" ] || fail "C6[reap e]: second run gave $(printf '%s' "$OUT" | rp_ids)"
pass "C6[reap e]: a second run finds the branch the first freed and still holds the dirty worktree"

# (f) the round lease: only a stale one (holder gone, this host) is listed and cleared
LEASE_RP="$(rp_git rev-parse --path-format=absolute --git-common-dir)/rota/round-lease.json"
mkdir -p "$(dirname "$LEASE_RP")"
true & DEAD_RP=$!; wait "$DEAD_RP" || true
rp_lease() { python3 - "$LEASE_RP" "$1" "$2" <<'PY'
import json, socket, sys
p, pid, host = sys.argv[1], int(sys.argv[2]), sys.argv[3] or socket.gethostname()
json.dump({"pid": pid, "start": 1, "host": host, "root": "x", "round": 4, "startedAt": "2026-01-02T03:04:05Z"}, open(p, "w"))
PY
}
sleep 30 & LIVE_RP=$!
python3 - "$LEASE_RP" "$LIVE_RP" <<'PY'
import json, socket, sys
p, pid = sys.argv[1], int(sys.argv[2])
start = int(open("/proc/%d/stat" % pid).read().rsplit(")", 1)[1].split()[19])
json.dump({"pid": pid, "start": start, "host": socket.gethostname(), "root": "x", "round": 4, "startedAt": "2026-01-02T03:04:05Z"}, open(p, "w"))
PY
OUT="$(rp_run --kind lease --apply)"
kill "$LIVE_RP" 2>/dev/null || true
[ "$(printf '%s' "$OUT" | rp_ids)" = "" ] || fail "C6[reap f]: a live lease was listed: $OUT"
[ -f "$LEASE_RP" ] || fail "C6[reap f]: a live lease was deleted"
rp_lease "$DEAD_RP" "elsewhere"
OUT="$(rp_run --kind lease --apply)"
[ "$(printf '%s' "$OUT" | rp_ids)" = "" ] || fail "C6[reap f]: a foreign lease was listed: $OUT"
[ -f "$LEASE_RP" ] || fail "C6[reap f]: a foreign lease was deleted"
rp_lease "$DEAD_RP" ""
OUT="$(rp_run --kind lease)"
[ "$(printf '%s' "$OUT" | rp_ids)" = "lease:round" ] || fail "C6[reap f]: stale lease candidates were $(printf '%s' "$OUT" | rp_ids)"
case "$OUT" in *"pid $DEAD_RP"*) ;; *) fail "C6[reap f]: the reason does not name the holder: $OUT" ;; esac
[ -f "$LEASE_RP" ] || fail "C6[reap f]: preview deleted the lease"
OUT="$(rp_run --kind lease --apply)"
[ "$(printf '%s' "$OUT" | rp_field reaped)" = '["lease:round"]' ] || fail "C6[reap f]: reaped was $(printf '%s' "$OUT" | rp_field reaped)"
[ ! -e "$LEASE_RP" ] || fail "C6[reap f]: the stale lease is still there"
OUT="$(rp_run --kind lease)"
[ "$(printf '%s' "$OUT" | rp_ids)" = "" ] || fail "C6[reap f]: no lease should remain: $OUT"
pass "C6[reap f]: a stale lease is listed and cleared; live, foreign and absent ones are left alone"

rm -rf "${TMP_RP:?}"
trap 'rm -rf "$TMP"' EXIT
