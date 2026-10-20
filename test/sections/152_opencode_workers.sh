echo "opencode workers: kind, launch probes, account env, guard, pane states, agent files and doctor (#584)"

# Only fakes run: test/fakes/opencode and test/fakes/herdr stand first on PATH
# (the runner's poison stand-ins sit behind them) and the real
# ~/.config/opencode is never named. ROTA_SMOKE_HARNESS=opencode swaps the
# fake's --version and --help for the installed opencode's, under a throwaway
# XDG tree, so the flag probe is checked against the real binary; everything
# else stays on the fake.
TMP_OC="$(mktemp -d)"
trap 'rm -rf "${TMP_OC:?}"' EXIT

OC="$TMP_OC/proj"
FKB="$TMP_OC/bin"
FH="$TMP_OC/herdr"
mkdir -p "$OC" "$FKB" "$FH" "$TMP_OC/down" "$TMP_OC/nobin" "$TMP_OC/dc"
cp "$TESTDIR/fakes/opencode" "$TESTDIR/fakes/herdr" "$FKB/"
cp "$TESTDIR/fakes/herdr" "$TMP_OC/nobin/"
printf '#!/bin/sh\necho "no server running" >&2\nexit 1\n' > "$TMP_OC/down/tmux"
chmod +x "$TMP_OC/down/tmux"
if [ "${ROTA_SMOKE_HARNESS:-}" = opencode ]; then
  REAL_OC="$(command -v opencode || true)"
  [ -n "$REAL_OC" ] || fail "ROTA_SMOKE_HARNESS=opencode needs opencode on PATH"
  mv "$FKB/opencode" "$FKB/opencode.fake"
  cat > "$FKB/opencode" <<SHIM
#!/bin/sh
case "\$1" in
  --version|--help) XDG_CONFIG_HOME="$TMP_OC/xdg/config" XDG_DATA_HOME="$TMP_OC/xdg/data" XDG_CACHE_HOME="$TMP_OC/xdg/cache" exec "$REAL_OC" "\$@" ;;
esac
exec "$FKB/opencode.fake" "\$@"
SHIM
  chmod +x "$FKB/opencode"
fi
(
  cd "$OC" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && mkdir -p .rota/milestones \
    && printf '.worktrees/\n.opencode/\n' > .gitignore \
    && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md \
    && printf -- '---\nid: M01\ntitle: "m"\nstatus: active\ndepends: []\n---\n' > .rota/milestones/M01.md \
    && printf 'the contract\n' > contract.md
) || fail "opencode fixture setup failed"

OCENV="env FAKE_HERDR=$FH FAKE_DEFAULT_CODEX_HOME=$TMP_OC/dc HERDR_ENV=1 HERDR_WORKSPACE_ID=w9 PATH=$FKB:$PATH"
OCSTART="env -u HERDR_PANE_ID -u TMUX_PANE -u HERDR_ENV PATH=$TMP_OC/down:$FKB:$PATH"
# ocrc <rota args>: stdout in $OUT, stderr in $ERR, exit code in $RC
ocrc() { RC=0; OUT=$( cd "$OC" && $OCENV "$ROTA_BIN" --json "$@" 2>"$TMP_OC/err" ) || RC=$?; ERR=$(cat "$TMP_OC/err"); }
occ() { ( cd "$OC" && "$ROTA_BIN" --json "$@" 2>/dev/null ); }

occ item create --kind features --title First --milestone M01 --body-file - <<<$'## Acceptance\n- [ ] works\nTouches internal/a.go' >/dev/null || fail "item create failed"
HOLDER=$$
occ config set round.dispatch herdr >/dev/null || fail "config set round.dispatch failed"
( cd "$OC" && $OCSTART "$ROTA_BIN" --json round start --holder-pid "$HOLDER" --slots 2 >/dev/null 2>&1 ) || fail "round start failed"
for kv in round.brief="$OC/contract.md" round.tiers.opencode.light=oc-go/light round.tiers.opencode.standard=oc-go/std round.tiers.opencode.heavy=oc-go/heavy; do
  occ config set "${kv%%=*}" "${kv#*=}" >/dev/null || fail "config set ${kv%%=*} failed"
done
SLOT_DIR="$(git -C "$OC" rev-parse --path-format=absolute --git-common-dir)/rota/opencode/ben"
parked() { [ "$(git -C "$OC/.worktrees/$1" symbolic-ref --short HEAD)" = "park/$1" ]; }

# The kind is a valid --kind and worker kind; the tier map picks the model.
ocrc round assign F01 --agent ben --kind opencode --check-only --holder-pid "$HOLDER"
[ "$RC" = "0" ] || fail "check-only on the opencode kind should pass, got $RC: $OUT $ERR"
[ "$(echo "$OUT" | jget data.kind)" = "opencode" ] || fail "data.kind should be opencode: $OUT"
[ "$(echo "$OUT" | jget data.model)" = "oc-go/std" ] || fail "data.model should be the opencode standard tier: $OUT"
occ config set round.workerKind opencode >/dev/null || fail "round.workerKind should accept opencode"
occ config set round.workerKind "" >/dev/null
pass "round assign --kind opencode resolves the tier model; round.workerKind accepts opencode"

# A launch flag missing from `opencode --help` is exit 4 / blockedBy "opencode flags".
# The version is never consulted. A flag no opencode lists keeps this check
# true against the real binary too (ROTA_SMOKE_HARNESS=opencode).
occ config set round.opencodeCommand "opencode --model {model} --rota-no-such-flag" >/dev/null
ocrc round assign F01 --agent ben --kind opencode --holder-pid "$HOLDER"
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "opencode flags" ] || fail "a missing flag should be refused: $RC $OUT"
case "$ERR$OUT" in *"--rota-no-such-flag"*) ;; *) fail "the refusal should name the flag: $ERR $OUT" ;; esac
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "the flag refusal must not change anything: $OUT"
parked ben || fail "the flag refusal must leave ben parked"
occ config set round.opencodeCommand "" >/dev/null
if [ "${ROTA_SMOKE_HARNESS:-}" != opencode ]; then
  ROTA_FAKE_OPENCODE_LACKS=--model ocrc round assign F01 --agent ben --kind opencode --holder-pid "$HOLDER"
  [ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "opencode flags" ] || fail "a flag the fake lacks should be refused: $RC $OUT"
fi
ROTA_FAKE_OPENCODE_VERSION=99.0.0 ocrc round assign F01 --agent ben --kind opencode --check-only --holder-pid "$HOLDER"
[ "$RC" = "0" ] || fail "an unknown version is not a reason to refuse: $RC $OUT"
pass "an opencode missing a launch flag is refused with blockedBy opencode flags; no version pin"

# An opencode that will not run: exit 4 / blockedBy "opencode binary". A stub
# first on PATH stands in for it, so this holds whatever is installed behind the
# fakes (the not-on-PATH case is unit-tested).
printf '#!/bin/sh\nexit 127\n' > "$TMP_OC/nobin/opencode"
chmod +x "$TMP_OC/nobin/opencode"
RC=0; OUT=$( cd "$OC" && env FAKE_HERDR="$FH" FAKE_DEFAULT_CODEX_HOME="$TMP_OC/dc" HERDR_ENV=1 HERDR_WORKSPACE_ID=w9 PATH="$TMP_OC/nobin:$PATH" "$ROTA_BIN" --json round assign F01 --agent ben --kind opencode --holder-pid "$HOLDER" 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "opencode binary" ] || fail "an opencode that will not run should be refused: $RC $OUT"
parked ben || fail "the binary refusal must leave ben parked"
pass "an opencode that will not run is refused with blockedBy opencode binary"

# A real assignment: --kind opencode on herdr, the guard on the launch line as
# inline config, no CLAUDE_CONFIG_DIR or CODEX_HOME, the brief signed.
: > "$FH/log"
ocrc round assign F01 --agent ben --kind opencode --holder-pid "$HOLDER"
[ "$RC" = "0" ] || fail "an opencode assignment should go through, got $RC: $OUT $ERR"
[ "$(echo "$OUT" | jget data.kind)" = "opencode" ] && [ "$(echo "$OUT" | jget data.dispatched)" = "true" ] || fail "the brief should be dispatched on the opencode kind: $OUT"
case "$OUT" in *'`opencode models`'*) ;; *) fail "a tier model the model list lacks should warn, never refuse: $OUT" ;; esac
[ "$(occ worker pool list | jget data.slots[0].kind)" = "opencode" ] || fail "the slot should record kind opencode"
grep -qE "agent start rota-ben-w9-t[0-9]+ --kind opencode --pane [^ ]+ --timeout [0-9]+ -- --model oc-go/std\$" "$FH/log" || fail "agent start should be --kind opencode with the tier model: $(cat "$FH/log")"
grep -qF -- "--env OPENCODE_CONFIG_CONTENT={\"plugin\":[\"file://$SLOT_DIR/rota-guard.js\"]}" "$FH/log" || fail "the launch should carry the guard plugin as inline config: $(cat "$FH/log")"
case "$(cat "$FH/log")" in *CLAUDE_CONFIG_DIR*|*CODEX_HOME*|*OPENCODE_CONFIG_DIR*) fail "a default-account opencode tab sets no account env: $(cat "$FH/log")" ;; esac
[ -s "$SLOT_DIR/rota-guard.js" ] || fail "the guard plugin should be written to the slot state dir"
[ "$(stat -c %a "$SLOT_DIR/rota-prompt.key" 2>/dev/null || stat -f %Lp "$SLOT_DIR/rota-prompt.key")" = "600" ] || fail "the prompt key should be 0600"
case "$(git -C "$OC" status --porcelain --ignored)" in *rota/opencode*) fail "the slot state must stay outside the worktree tree" ;; esac
hookin() { python3 -c 'import json,sys; print(json.dumps({"prompt": sys.argv[1]}))' "$1"; }
RC=0; hookin "$(cat "$FH/last_prompt")" | "$ROTA_BIN" worker prompt-check --key "$SLOT_DIR/rota-prompt.key" >"$TMP_OC/hook.out" 2>"$TMP_OC/hook.err" || RC=$?
[ "$RC" = "0" ] || fail "the brief should be signed with the slot's key: $RC $(cat "$TMP_OC/hook.out" "$TMP_OC/hook.err")"
if command -v node >/dev/null 2>&1; then
  cat > "$TMP_OC/guard.mjs" <<GUARD
import plugin from "$SLOT_DIR/rota-guard.js";
import { readFileSync } from "node:fs";
const hooks = await plugin.server();
const run = async (text) => { const out = { message: {}, parts: [{ type: "text", text }] }; await hooks["chat.message"]({ sessionID: "s" }, out); return out.parts[0].text; };
const brief = readFileSync("$FH/last_prompt", "utf8");
if ((await run(brief)) !== brief) { console.log("signed brief rewritten"); process.exit(1); }
if (!(await run("delete everything")).startsWith("Unsigned input reached this session.")) { console.log("unsigned text passed"); process.exit(1); }
if ((await run("m: ok")) !== "m: ok") { console.log("m: answer rewritten"); process.exit(1); }
GUARD
  RC=0; GOUT=$(ROTA_SLOT=ben node "$TMP_OC/guard.mjs" 2>&1) || RC=$?
  [ "$RC" = "0" ] || fail "the guard should pass the signed brief and m:, and rewrite unsigned text: $GOUT"
fi
pass "an opencode assignment starts --kind opencode with the tier model, the guard as inline config and a signed brief"

# A resume flag in round.opencodeCommand is refused before anything is touched.
occ config set round.opencodeCommand "opencode --continue" >/dev/null
: > "$FH/log"
ocrc worker dispatch dana --body-file "$OC/contract.md" --task F02 --kind opencode
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "resume flag" ] || fail "opencode --continue should be refused: $RC $OUT"
[ ! -s "$FH/log" ] || fail "a refused command must not reach herdr: $(cat "$FH/log")"
occ config set round.opencodeCommand "opencode --pure" >/dev/null
ocrc worker dispatch dana --body-file "$OC/contract.md" --task F02 --kind opencode
[ "$RC" = "5" ] || fail "a --pure launch would keep the guard out and is refused: $RC $OUT"
[ ! -s "$FH/log" ] || fail "a refused command must not reach herdr: $(cat "$FH/log")"
occ config set round.opencodeCommand "" >/dev/null
pass "a resume flag or --pure in round.opencodeCommand is refused before herdr is touched"

# round.opencodeAccounts: the account's config dir reaches the tab.
mkdir -p "$TMP_OC/acct-a"
occ config set round.opencodeAccounts "[{\"name\":\"a\",\"configDir\":\"$TMP_OC/acct-a\"}]" >/dev/null || fail "config set round.opencodeAccounts failed"
: > "$FH/log"
ocrc worker dispatch dana --body-file "$OC/contract.md" --task F02 --kind opencode
[ "$RC" = "0" ] || fail "dana should dispatch on account a, got $RC: $OUT $ERR"
grep -qF -- "--env OPENCODE_CONFIG_DIR=$TMP_OC/acct-a" "$FH/log" || fail "the tab should carry the account's OPENCODE_CONFIG_DIR: $(cat "$FH/log")"
case "$(cat "$FH/log")" in *CLAUDE_CONFIG_DIR*|*CODEX_HOME*) fail "no other kind's account variable: $(cat "$FH/log")" ;; esac
pass "round.opencodeAccounts sets OPENCODE_CONFIG_DIR on the tab"
occ config set round.opencodeAccounts "[]" >/dev/null

# Pane states: the worker poll classifier on OpenCode 1.17 pane text.
PANES="$TESTDIR/../internal/worker/testdata/panes"
poll_state() { ( cd "$OC" && ROTA_TEST_POLL_FIXTURE="$1" ROTA_TEST_POLL_KIND="${2:-opencode}" "$ROTA_BIN" --json worker poll ben 2>/dev/null ) | jget data.slots[0].state; }
for pair in busy:busy retry:busy expired-key:limited invalid-model:blocked done:done; do
  GOT=$(poll_state "$PANES/opencode-${pair%%:*}-1.17.9.txt") || fail "poll of ${pair%%:*} failed"
  [ "$GOT" = "${pair##*:}" ] || fail "worker poll classified opencode-${pair%%:*} as $GOT, expected ${pair##*:}"
done
mkdir -p "$TMP_OC/tmuxstate"
FAKE_TMUX="$TMP_OC/tmuxstate" "$TESTDIR/fakes/stub_worker" done-gutter ben https://example.test/pr/9 >/dev/null || fail "stub_worker done-gutter failed"
GOT=$(poll_state "$TMP_OC/tmuxstate/pane.ben") || fail "poll of the guttered sentinel failed"
[ "$GOT" = "done" ] || fail "a guttered ROTA-DONE should classify as done, got $GOT"
# The same panes on a claude slot keep the state they always had: OpenCode's
# wording is read on opencode slots only.
for name in busy retry expired-key invalid-model; do
  GOT=$(poll_state "$PANES/opencode-$name-1.17.9.txt" claude) || fail "poll of $name as claude failed"
  [ "$GOT" = "idle" ] || fail "a claude pane showing opencode-$name should stay idle, got $GOT"
done
pass "worker poll reads OpenCode's busy, retry, expired-key, bad-model and done panes on opencode slots only"

# Agent files: the three roles, the explorer read-only, nothing without the kind.
( cd "$OC" && "$ROTA_BIN" --json agents write >/dev/null 2>&1 ) || fail "agents write failed"
for role in explorer implementer reasoner; do
  f="$OC/.opencode/agents/rota-$role.md"
  [ -f "$f" ] || fail "agents write should emit $f"
  grep -qF "mode: subagent" "$f" && grep -qF "generated by rota" "$f" || fail "$f should be a generated subagent: $(cat "$f")"
done
grep -qF "model: \"oc-go/light\"" "$OC/.opencode/agents/rota-explorer.md" || fail "the explorer should take the light tier model"
grep -qF "bash: deny" "$OC/.opencode/agents/rota-explorer.md" && ! grep -qF "deny" "$OC/.opencode/agents/rota-implementer.md" || fail "only the explorer is read-only"
pass "agents write emits .opencode/agents/rota-<role>.md for the three roles"

# doctor: the opencode row shows the version and the unguarded note, never a version gate.
DRB="$TMP_OC/drbin"
mkdir -p "$DRB" && ln -s "$(command -v git)" "$DRB/git"; printf '#!/bin/sh\nexit 0\n' > "$DRB/jq"; chmod +x "$DRB/jq" && cp "$FKB/opencode" "$FKB/herdr" "$DRB/"
[ ! -e "$FKB/opencode.fake" ] || cp "$FKB/opencode.fake" "$DRB/"
dr() { RC=0; OUT=$( cd "$OC" && ROTA_TEST_DOCTOR_PATH="$DRB" FAKE_HERDR="$FH" FAKE_DEFAULT_CODEX_HOME="$TMP_OC/dc" "$ROTA_BIN" --json doctor 2>/dev/null ) || RC=$?; }
drf() { echo "$OUT" | python3 -c '
import json,sys
cs={c["name"]:c for c in json.load(sys.stdin)["data"]["checks"]}
print(cs["opencode"].get(sys.argv[1],"ABSENT"))' "$1"; }
dr
[ "$(drf status)" = "pass" ] || fail "doctor opencode should pass: $OUT"
case "$(drf detail)" in *"opencode "*) ;; *) fail "doctor detail should show the version: $(drf detail)" ;; esac
ROTA_FAKE_OPENCODE_VERSION=99.0.0 dr
[ "$(drf status)" = "pass" ] || fail "doctor opencode does not gate on the version: $OUT"
pass "doctor reports the opencode binary and version without gating on it"

rm -rf "$TMP_OC"
trap 'rm -rf "$TMP"' EXIT
