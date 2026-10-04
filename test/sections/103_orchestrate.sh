echo "orchestrate: launcher dry-run per harness and host, bare rota without a terminal (#19)"

# A fixture project and stand-ins for herdr and codex that only answer
# --version, so doctor passes and the plan can name herdr. Nothing starts: dry-run runs doctor and
# prints the plan, and bare rota here has no terminal to attach.
TMP_ORCH="$(mktemp -d)"
trap 'rm -rf "${TMP_ORCH:?}"' EXIT
ORP="$TMP_ORCH/proj"
mkdir -p "$ORP/.rota" "$TMP_ORCH/bin" "$TMP_ORCH/home"
(
  cd "$ORP" && git init -q -b main . && printf '.worktrees/\n' > .gitignore &&
    git add .gitignore && git -c user.email=a@b -c user.name=n commit -q -m init
) || fail "orchestrate fixture repo setup failed"
printf '#!/bin/sh\ncase "$1" in --version) echo "herdr 0.9.3" ;; *) echo "herdr $*" >> "%s/herdr.log"; exit 99 ;; esac\n' "$TMP_ORCH" > "$TMP_ORCH/bin/herdr"
printf '#!/bin/sh\necho "codex-cli 0.159.2"\n' > "$TMP_ORCH/bin/codex"
chmod +x "$TMP_ORCH/bin/herdr" "$TMP_ORCH/bin/codex"
or_cfg() { printf '{"git":{"baseBranch":"main"}%s}\n' "$1" > "$ORP/.rota/config.json"; }
or_dry() { (cd "$ORP" && PATH="$TMP_ORCH/bin:$PATH" HOME="$TMP_ORCH/home" "$ROTA_BIN" --json orchestrate --dry-run 2>&1); }

# Default: claude, in a herdr session of rota's own (herdr is preferred outside a multiplexer).
or_cfg ''
OUT="$(or_dry)" || fail "orchestrate --dry-run failed: $OUT"
[ "$(jget 'data.harness' <<<"$OUT")" = "claude" ] || fail "default harness should be claude: $OUT"
[ "$(jget 'data.host' <<<"$OUT")" = "herdr" ] || fail "outside a multiplexer the plan should prefer herdr: $OUT"
[ "$(jget 'data.mode' <<<"$OUT")" = "herdr-session" ] || fail "herdr mode should be herdr-session: $OUT"
[ "$(jget 'data.session' <<<"$OUT")" = "rota-proj" ] || fail "session name should be rota-<dir>: $OUT"
[ "$(jget 'data.dryRun' <<<"$OUT")" = "true" ] || fail "dry-run should say so: $OUT"
case $OUT in *'/rota-orchestrate'*) ;; *) fail "claude should start /rota-orchestrate: $OUT" ;; esac

# Each harness: its own command and prompt.
for h in codex; do
  or_cfg ",\"orchestrator\":{\"harness\":\"$h\"}"
  OUT="$(or_dry)" || fail "orchestrate --dry-run failed for $h: $OUT"
  [ "$(jget 'data.harness' <<<"$OUT")" = "$h" ] || fail "harness $h not planned: $OUT"
  case $h in
    codex) want='$rota-orchestrate' ;;
  esac
  case $OUT in *"$want"*) ;; *) fail "$h plan should mention $want: $OUT" ;; esac
done

# An unknown harness is a config error that names the choices, before anything starts.
or_cfg ',"orchestrator":{"harness":"emacs"}'
RC=0; OUT="$(or_dry)" || RC=$?
[ "$RC" != 0 ] || fail "an unknown harness should fail"
case $OUT in *"claude, codex"*) ;; *) fail "unknown harness should list the choices: $OUT" ;; esac

# Nothing the dry runs did reached herdr beyond --version.
[ ! -s "$TMP_ORCH/herdr.log" ] || fail "dry-run touched herdr: $(cat "$TMP_ORCH/herdr.log")"

# Bare rota without a terminal: the usage error it always had, never a session
# nobody can see, in an initialized project and outside one, with and without --json.
or_cfg ''
RC=0; OUT="$(cd "$ORP" && PATH="$TMP_ORCH/bin:$PATH" HOME="$TMP_ORCH/home" "$ROTA_BIN" </dev/null 2>&1)" || RC=$?
[ "$RC" = 2 ] || fail "bare rota without a terminal should exit 2, got $RC: $OUT"
case $OUT in *"missing command"*) ;; *) fail "bare rota should say 'missing command': $OUT" ;; esac
RC=0; OUT="$(cd "$TMP_ORCH" && PATH="$TMP_ORCH/bin:$PATH" HOME="$TMP_ORCH/home" "$ROTA_BIN" </dev/null 2>&1)" || RC=$?
[ "$RC" = 2 ] || fail "bare rota outside a project should exit 2, got $RC: $OUT"
RC=0; OUT="$(cd "$ORP" && PATH="$TMP_ORCH/bin:$PATH" HOME="$TMP_ORCH/home" "$ROTA_BIN" --json 2>&1 </dev/null)" || RC=$?
[ "$RC" = 2 ] || fail "bare rota --json should exit 2, got $RC: $OUT"
[ ! -s "$TMP_ORCH/herdr.log" ] || fail "bare rota touched herdr: $(cat "$TMP_ORCH/herdr.log")"

rm -rf "$TMP_ORCH"
trap 'rm -rf "$TMP"' EXIT
pass "orchestrate — dry-run plans per harness, herdr preferred outside a multiplexer, bare rota refuses without a terminal"
