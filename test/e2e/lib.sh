#!/usr/bin/env bash
# Shared helpers for the e2e scenarios (test/e2e/scenarios/*.sh, #386). A
# scenario is sourced by test/e2e/run.sh inside its own subshell and sandbox:
# it calls e2e_fixture, drives `rota` through `rota_j`, steers the stub worker
# through `stub`, and asserts only public surfaces (exit codes, --json
# envelopes, .rota/workers.json). Everything runs on fakes: test/fakes/tmux,
# test/fakes/stub_worker and local git. No model, no network.

. "$(dirname "${BASH_SOURCE[0]}")/../lib/common.sh"

e2e_pass() { pass "$1"; }
e2e_fail() { printf '  \033[31mFAIL\033[0m %s\n' "$1" >&2; exit 1; }

# expect <got> <want> <what>: fail with the three if they differ.
expect() { [ "$1" = "$2" ] || e2e_fail "$3: want '$2', got '$1'"; }

# e2e_fixture <slot>...: a git project with a worktree and a tmux window per
# slot (each holding issue 100+n), a registry in .rota/workers.json and the
# fake tmux state. Sets E2E_ROOT, E2E_FK (the fake's state dir).
e2e_fixture() {
  E2E_ROOT="$E2E_TMP/proj"; E2E_FK="$E2E_TMP/fake"
  mkdir -p "$E2E_ROOT/.rota" "$E2E_FK/bin" "$E2E_FK/tmux"
  cp "$E2E_FAKES/tmux" "$E2E_FK/bin/tmux"
  cp "$E2E_FAKES/stub_worker" "$E2E_FK/bin/stub_worker"
  : >"$E2E_FK/tmux/session"; : >"$E2E_FK/tmux/windows"; : >"$E2E_FK/snapshot"
  (
    cd "$E2E_ROOT" && git init -q -b main . && git config user.email t@t && git config user.name t \
      && echo seed >seed.txt && git add seed.txt && git commit -q -m seed
  ) || e2e_fail "fixture repo setup failed"
  printf '{"git":{"baseBranch":"main"},"round":{"dispatch":"tmux"}}\n' >"$E2E_ROOT/.rota/config.json"
  local n=0 slot rows=""
  for slot in "$@"; do
    n=$((n + 1))
    git -C "$E2E_ROOT" worktree add -q -b "$slot/$((100 + n))-task" "$E2E_ROOT/.worktrees/$slot" main \
      || e2e_fail "worktree for $slot failed"
    echo "$slot 4194001" >>"$E2E_FK/tmux/windows"
    printf 'rota:%s\t%s\n' "$slot" "$E2E_ROOT/.worktrees/$slot" >>"$E2E_FK/snapshot"
    rows="$rows${rows:+,}{\"name\":\"$slot\",\"branch\":\"$slot/$((100 + n))-task\",\"worktree\":\"$E2E_ROOT/.worktrees/$slot\",\"base\":\"main\",\"handle\":\"rota:$slot\",\"state\":\"busy\",\"issue\":\"$((100 + n))\",\"task\":\"T$n\",\"relays\":[]}"
  done
  printf '{"session":"rota","round":1,"slots":[%s]}\n' "$rows" >"$E2E_ROOT/.rota/workers.json"
}

# rota_j <args>: `rota --json <args>` in the fixture, tmux host faked, real
# tmux/herdr/gh/glab unreachable. Prints the envelope; returns rota's exit code.
rota_j() {
  # Host and home isolation is the runner's isolate_env (test/lib/isolate.sh); no second scrub here.
  ( cd "$E2E_ROOT" && env \
      PATH="$E2E_FK/bin:$E2E_POISON:$PATH" FAKE_TMUX="$E2E_FK/tmux" FAKE_TMUX_SNAPSHOT="$E2E_FK/snapshot" \
      "$ROTA_BIN" --json "$@" 2>/dev/null )
}

# stub <cue> [args]: play a worker cue on the fake host.
stub() { STUB_ROOT="$E2E_ROOT" FAKE_TMUX="$E2E_FK/tmux" FAKE_TMUX_SNAPSHOT="$E2E_FK/snapshot" "$E2E_FK/bin/stub_worker" "$@" >/dev/null; }

# e2e_slot <slot> <field>: one field of a slot in this fixture's .rota/workers.json.
e2e_slot() { slot_field "$E2E_ROOT/.rota/workers.json" "$1" "$2"; }
