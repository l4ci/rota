echo "test isolation: env scrub and worktree guard (#320)"
# test/lib/isolate.sh is what test/runner.sh and test/gate.sh use so no check can
# reach a live round's host, ssh-agent, home or worktrees. Each case runs in its
# own bash so the scrub does not leak into the runner's environment.

ISO="$TESTDIR/lib/isolate.sh"
IS="$(mktemp -d "$TMP/isolate.XXXXXX")"

# isolate_env drops host and ssh identity, and pins home and XDG under <dir>.
OUT=$(env HOME=/real/home XDG_CONFIG_HOME=/real/xdg HERDR_ENV=1 HERDR_PANE_ID=w1:p1 TMUX=/s,1,0 TMUX_PANE=%1 \
  SSH_AUTH_SOCK=/real/agent SSH_AGENT_PID=42 bash -c '. "$1"; isolate_env "$2"
  echo "herdr=${HERDR_ENV:-} pane=${HERDR_PANE_ID:-} tmux=${TMUX:-} tp=${TMUX_PANE:-} sock=${SSH_AUTH_SOCK:-} pid=${SSH_AGENT_PID:-}"
  echo "home=$HOME cfg=$XDG_CONFIG_HOME cache=$XDG_CACHE_HOME data=$XDG_DATA_HOME state=$XDG_STATE_HOME"' _ "$ISO" "$IS/env") \
  || fail "isolate_env failed"
[ "$(sed -n 1p <<<"$OUT")" = "herdr= pane= tmux= tp= sock= pid=" ] || fail "isolate_env left host or ssh state set: $OUT"
case "$(sed -n 2p <<<"$OUT")" in
  "home=$IS/env/home cfg=$IS/env/xdg/config cache=$IS/env/xdg/cache data=$IS/env/xdg/data state=$IS/env/xdg/state") ;;
  *) fail "isolate_env did not pin HOME/XDG under the dir: $OUT" ;;
esac
[ -d "$IS/env/home" ] || fail "isolate_env did not create the pinned home"
pass "isolate_env strips HERDR_*/TMUX*/ssh-agent state and pins HOME/XDG"

# The worktree guard fails when the repo's worktree list changes, and only then.
GR="$IS/repo"; git init -q "$GR"
git -C "$GR" -c user.email=t@t -c user.name=t commit -q --allow-empty -m seed
git -C "$GR" worktree add -q -b park/ben "$GR/.worktrees/ben"
guard() { bash -c '. "$1"; worktree_guard_check "$2" "$3"' _ "$ISO" "$@"; }
bash -c '. "$1"; worktree_snapshot "$2"' _ "$ISO" "$GR" > "$IS/before" || fail "worktree_snapshot failed"
guard "$IS/before" "$GR" >/dev/null 2>&1 || fail "worktree guard flagged an unchanged repo"
git -C "$GR" worktree add -q -b park/nia "$GR/.worktrees/nia"
if guard "$IS/before" "$GR" >/dev/null 2>&1; then fail "worktree guard missed an added worktree"; fi
git -C "$GR" worktree remove --force "$GR/.worktrees/nia"
guard "$IS/before" "$GR" >/dev/null 2>&1 || fail "worktree guard flagged a restored repo"
git -C "$GR" worktree remove --force "$GR/.worktrees/ben"
if guard "$IS/before" "$GR" >/dev/null 2>&1; then fail "worktree guard missed a removed worktree"; fi
pass "worktree guard fails on an added or removed worktree and passes when unchanged"

# rota's own scratch trees (a train's, a CI verify's) come and go beside a run without tripping it (#427).
bash -c '. "$1"; worktree_snapshot "$2"' _ "$ISO" "$GR" > "$IS/before" || fail "worktree_snapshot failed"
mkdir -p "$IS/rota-train-abc123" "$IS/rota-ci-def456"
git -C "$GR" worktree add -q --detach "$IS/rota-train-abc123/tree"
git -C "$GR" worktree add -q --detach "$IS/rota-ci-def456/tree"
guard "$IS/before" "$GR" >/dev/null 2>&1 || fail "worktree guard flagged rota's own scratch trees"
git -C "$GR" worktree add -q --detach "$IS/other-scratch"
if guard "$IS/before" "$GR" >/dev/null 2>&1; then fail "worktree guard missed a foreign worktree beside the scratch trees"; fi
git -C "$GR" worktree remove --force "$IS/other-scratch"
git -C "$GR" worktree remove --force "$IS/rota-train-abc123/tree"
git -C "$GR" worktree remove --force "$IS/rota-ci-def456/tree"
pass "worktree guard ignores rota's scratch trees and still flags any other worktree"

python3 "$TESTDIR/runner_leak_test.py" || fail "runner checkout guard regression"
pass "runner preserves concurrent edits, deletions and merges across overlapping shards"

trap 'rm -rf "$TMP"' EXIT
