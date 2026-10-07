# Environment and worktree isolation for the test harness (#320), sourced by
# test/runner.sh, test/gate.sh and smoke section 116. A round runs inside herdr
# with live worker worktrees, an ssh-agent and the developer's home; nothing the
# suite runs may reach any of them.

# isolate_env <dir>: drop host and ssh identity, pin home and XDG under <dir>.
# Go's caches and env file are pinned to their real locations first, so the
# build still finds its module cache once HOME points at an empty dir.
isolate_env() {
  local dir="$1" v
  if [ -z "${GOCACHE:-}" ] && command -v go >/dev/null 2>&1; then
    export GOCACHE GOMODCACHE GOPATH GOENV
    GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOPATH="$(go env GOPATH)" GOENV="$(go env GOENV)"
  fi
  for v in $(compgen -e | grep -E '^(HERDR_|TMUX)'); do unset "$v"; done
  unset SSH_AUTH_SOCK SSH_AGENT_PID
  export HOME="$dir/home" XDG_CONFIG_HOME="$dir/xdg/config" XDG_CACHE_HOME="$dir/xdg/cache" \
    XDG_DATA_HOME="$dir/xdg/data" XDG_STATE_HOME="$dir/xdg/state"
  mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME"
}

# worktree_snapshot <repo>: the paths of the repo's worktrees, sorted. Paths only,
# not HEADs: a worker committing in its own slot mid-run is not a leak. rota's
# own scratch trees (a train's rota-train-*/tree, a CI verify's rota-ci-*/tree)
# are left out: a gate or train running beside this run owns them, and they come
# and go by design (#427).
worktree_snapshot() {
  git -C "$1" worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p' \
    | grep -Ev '/rota-(train|ci)-[^/]+/tree$' | sort
}

# worktree_guard_check <before-file> <repo>: fail, naming the difference, when the
# repo's worktree list is not what worktree_snapshot recorded in <before-file>.
worktree_guard_check() {
  local now d
  now="$(mktemp)" || return 1
  worktree_snapshot "$2" > "$now"
  if d="$(diff "$1" "$now")"; then rm -f "$now"; return 0; fi
  rm -f "$now"
  printf 'error: the run changed the worktrees of %s (< before, > after):\n%s\n' "$2" "$d" >&2
  return 1
}
