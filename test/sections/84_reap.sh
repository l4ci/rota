echo "C6: rota reap lists and clears a stale round lease and nothing a live round holds"

# Everything lives in one fresh mktemp dir, and only --kind lease is applied,
# so no real tab or agent can be touched.
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

# reap snapshots the pane host, tmux when nothing says herdr: an empty fake
# tmux stands in so nothing real is asked.
mkdir -p "$TMP_RP/bin"
printf '#!/bin/sh\nexit 0\n' >"$TMP_RP/bin/tmux"
chmod +x "$TMP_RP/bin/tmux"
rp_run() { env -u HERDR_ENV -u TMUX PATH="$TMP_RP/bin:$PATH" "$ROTA_BIN" --json -C "$RP" reap "$@" 2>/dev/null; }
rp_ids() { python3 -c '
import json,sys
d=json.load(sys.stdin)["data"]
print(",".join(c["id"]+("!" if "held" in c else "") for c in d["candidates"]))'; }
rp_field() { python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["data"][sys.argv[1]]))' "$1"; }

# The worktree, branch, tab and process candidates need a host the binary
# cannot fake: they are covered by internal/cli/reap_test.go through Deps.Host.
# What is left here is the lease, which reap finds from git and the process
# table alone.

# the round lease: only a stale one (holder gone, this host) is listed and cleared
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
[ "$(printf '%s' "$OUT" | rp_ids)" = "" ] || fail "C6[reap lease]: a live lease was listed: $OUT"
[ -f "$LEASE_RP" ] || fail "C6[reap lease]: a live lease was deleted"
rp_lease "$DEAD_RP" "elsewhere"
OUT="$(rp_run --kind lease --apply)"
[ "$(printf '%s' "$OUT" | rp_ids)" = "" ] || fail "C6[reap lease]: a foreign lease was listed: $OUT"
[ -f "$LEASE_RP" ] || fail "C6[reap lease]: a foreign lease was deleted"
rp_lease "$DEAD_RP" ""
OUT="$(rp_run --kind lease)"
[ "$(printf '%s' "$OUT" | rp_ids)" = "lease:round" ] || fail "C6[reap lease]: stale lease candidates were $(printf '%s' "$OUT" | rp_ids)"
case "$OUT" in *"pid $DEAD_RP"*) ;; *) fail "C6[reap lease]: the reason does not name the holder: $OUT" ;; esac
[ -f "$LEASE_RP" ] || fail "C6[reap lease]: preview deleted the lease"
OUT="$(rp_run --kind lease --apply)"
[ "$(printf '%s' "$OUT" | rp_field reaped)" = '["lease:round"]' ] || fail "C6[reap lease]: reaped was $(printf '%s' "$OUT" | rp_field reaped)"
[ ! -e "$LEASE_RP" ] || fail "C6[reap lease]: the stale lease is still there"
OUT="$(rp_run --kind lease)"
[ "$(printf '%s' "$OUT" | rp_ids)" = "" ] || fail "C6[reap lease]: no lease should remain: $OUT"
pass "C6[reap lease]: a stale lease is listed and cleared; live, foreign and absent ones are left alone"

# a released adopted slot keeps its checkout, here outside .worktrees/: the merged
# branch is listed, held by that checkout, and --apply leaves both alone
rp_git worktree add -q -b codex/7-ext "$TMP_RP/outside" HEAD
# the adopt record is what makes a branch outside the roster's shape reap's to list
printf '%s\n' '{"ts":"2026-01-01T00:00:00Z","kind":"adopt","round":1,"issue":"7","slot":"ext-1","detail":{"branch":"codex/7-ext"}}' >"$RP/.rota/ledger.jsonl"
OUT="$(rp_run --kind branch)"
[ "$(printf '%s' "$OUT" | rp_ids)" = "branch:codex/7-ext!,branch:kit/9-gone" ] || fail "reap[external]: branch candidates were $(printf '%s' "$OUT" | rp_ids)"
OUT="$(rp_run --kind branch --apply)"
[ "$(printf '%s' "$OUT" | rp_field reaped)" = '["branch:kit/9-gone"]' ] || fail "reap[external]: reaped was $(printf '%s' "$OUT" | rp_field reaped)"
rp_git rev-parse -q --verify refs/heads/codex/7-ext >/dev/null || fail "reap[external]: the held external branch was deleted"
[ -d "$TMP_RP/outside" ] || fail "reap[external]: the external worktree was deleted"
pass "reap[external]: a merged branch checked out outside .worktrees/ is listed as held, never deleted"

rm -rf "${TMP_RP:?}"
trap 'rm -rf "$TMP"' EXIT
