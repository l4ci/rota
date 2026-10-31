#!/usr/bin/env bash
# The video drift check (#2536): scripts/videos.py --check names every stale
# episode and needs only Python and a rota binary. Runs against fake binaries so
# a changed flag, a failing command and a masked build stamp are all exercised.
# Sourced by test/doclint.sh (REPO, TMP, pass, fail set); runnable alone:
#   bash test/videos-drift.sh
if [ -z "${REPO:-}" ]; then
  set -euo pipefail
  REPO="$(cd "$(dirname "$0")/.." && pwd)"
  . "$REPO/test/lib.sh"
  TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
fi
VID="$REPO/scripts/videos.py"
VD="$TMP/videos-drift"; mkdir -p "$VD"

# fake_ver <path> <stdout of 'rota version'>: a binary that prints one form of the stamp
fake_ver() { printf '#!/bin/sh\necho "%s"\n' "$2" > "$1"; chmod +x "$1"; }

# fake_rota <path> <help-flag> <exit-of-help> <stamp-commit>
fake_rota() {
  cat > "$1" <<FAKE
#!/bin/sh
case "\$1" in
  version) echo "rota 1.2.3 ($4, 2026-0$5-01T00:00:00Z)" ;;
  --help) echo "usage: rota $2"; echo "home=\$HOME"; echo "slot=\${ROTA_SLOT:-none} git=\${GIT_DIR:-none}"; exit $3 ;;
esac
FAKE
  chmod +x "$1"
}
fake_rota "$VD/rota-a" "--old-flag" 0 aaaaaaa 1
fake_rota "$VD/rota-stamp" "--old-flag" 0 bbbbbbb 2
fake_rota "$VD/rota-flag" "--new-flag" 0 aaaaaaa 1
fake_rota "$VD/rota-fail" "--old-flag" 3 aaaaaaa 1

mkroot() { # <dir>: a videos root holding the fixture episode and its snapshot from rota-a
  mkdir -p "$1/episodes" "$1/snapshots"
  cp "$REPO/test/fixtures/videos/00-fixture.md" "$1/episodes/00-fixture.md"
  python3 "$VID" capture "$1/episodes/00-fixture.md" --bin "$VD/rota-a" --write "$1/snapshots/00-fixture.json" \
    || fail "videos.py capture failed on the fixture"
}
R="$VD/root"; mkroot "$R"
chk() { rc=0; OUT="$(ROTA_SLOT=leak GIT_DIR=/nonexistent python3 "$VID" --check --root "$R" --bin "$1" 2>&1)" || rc=$?; }

chk "$VD/rota-a"
{ [ "$rc" = 0 ] && grep -q '1 episode(s) current' <<<"$OUT"; } || fail "a current episode should pass (rc $rc): $OUT"
pass "videos --check passes an episode whose snapshot matches the binary"

chk "$VD/rota-stamp"
[ "$rc" = 0 ] || fail "a changed build stamp alone should not go stale (rc $rc): $OUT"
pass "videos --check masks the rota version build stamp (commit and time) and nothing else"

chk "$VD/rota-flag"
{ [ "$rc" = 1 ] && grep -q '00-fixture: output changed: shot 01 `rota --help`' <<<"$OUT"; } \
  || fail "a changed flag should go stale and name the episode and command (rc $rc): $OUT"
pass "videos --check exits 1 and names the episode when a command's output changed"

chk "$VD/rota-fail"
{ [ "$rc" = 1 ] && grep -q '00-fixture: command failed: shot 01 `rota --help` exited 3' <<<"$OUT"; } \
  || fail "a failing command should go stale (rc $rc): $OUT"
pass "videos --check reports a command that fails"

grep -q "home=$HOME\b" "$R/snapshots/00-fixture.json" && fail "capture leaked the real HOME"
grep -q 'slot=none git=none' "$R/snapshots/00-fixture.json" || fail "capture did not drop ROTA_* and GIT_DIR from the environment"
pass "capture pins HOME and drops ROTA_* and GIT_* variables"

# script edited since render: the manifest holds a different source hash
mkdir -p "$R/out"
printf '{"episodes":{"00-fixture":{"rota_version":"1.2.3","source_hash":"deadbeef"}}}\n' > "$R/out/manifest.json"
chk "$VD/rota-a"
{ [ "$rc" = 1 ] && grep -q '00-fixture: script edited since render' <<<"$OUT"; } \
  || fail "an edited script should go stale against the manifest (rc $rc): $OUT"
HASH="$(python3 -c "import hashlib,sys;print(hashlib.sha256(open(sys.argv[1],'rb').read()).hexdigest())" "$R/episodes/00-fixture.md")"
printf '{"episodes":{"00-fixture":{"rota_version":"1.2.3","source_hash":"%s"}}}\n' "$HASH" > "$R/out/manifest.json"
chk "$VD/rota-a"
[ "$rc" = 0 ] || fail "a script matching the manifest hash should be current (rc $rc): $OUT"
pass "videos --check compares each script's hash with out/manifest.json"

# a script outside the grammar is reported, not a traceback
printf -- '---\nnumber: 9\n---\n\n### 01 terminal\n\nvo: x\n' > "$R/episodes/09-bad.md"
chk "$VD/rota-a"
{ [ "$rc" = 1 ] && grep -q '09-bad: script does not parse' <<<"$OUT"; } || fail "a bad script should be named (rc $rc): $OUT"
rm "$R/episodes/09-bad.md"
pass "videos --check names a script that does not parse"

# against the real binary: the fixture's `rota version` is stable once the stamp is masked
if [ -n "${ROTA_BIN:-}" ]; then
  R2="$VD/real"; mkdir -p "$R2/episodes" "$R2/snapshots"
  printf -- '---\nnumber: 0\nslug: real\n---\n\n### 01 terminal\n\nvo: x\n\n```sh\nrota version\n```\n\nvertical: x\n' > "$R2/episodes/00-real.md"
  python3 "$VID" capture "$R2/episodes/00-real.md" --bin "$ROTA_BIN" --write "$R2/snapshots/00-real.json"
  OUT="$(python3 "$VID" --check --root "$R2" --bin "$ROTA_BIN" 2>&1)" || fail "real-binary check failed: $OUT"
  # whatever form `rota version` prints (stamped, ldflags only, dev), no commit or build time survives
  if grep -Eq '\([0-9a-f]{7,40}|[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]+Z' "$R2/snapshots/00-real.json"; then
    fail "a commit or build time survived in the snapshot: $(grep -E 'rota' "$R2/snapshots/00-real.json" | head -2)"
  fi
  pass "videos --check runs against \$ROTA_BIN"
fi

# say: is the spoken form; captions always come from vo:
printf -- '---\nnumber: 0\n---\n\n### 01 motion\n\nvo: Open the .rota folder.\n\nsay: Open the dot Ro-Ta folder.\n\nscene: x\n\nvertical: x\n' > "$VD/say.md"
SAY="$(python3 "$VID" parse "$VD/say.md" | python3 -c "import json,sys;s=json.load(sys.stdin)['shots'][0];print(s['caption']+'|'+s['speak'])")"
[ "$SAY" = 'Open the .rota folder.|Open the dot Ro-Ta folder.' ] || fail "say: should set the spoken text and leave the caption on vo: ($SAY)"
pass "videos.py parse: captions come from vo:, audio from say: when present"

# crop: prose becomes per-command first/last rules; unparsed prose crops nothing
CR="$(python3 -c "
import sys,json;sys.path.insert(0,'$REPO/scripts')
import videos
print(json.dumps([videos.crop_rules('rota init keeps its last 3 lines; rota design show F12 keeps its first 8 lines.',['rota init','rota design show F12']),videos.crop_rules('keep the FAIL line',['a'])],sort_keys=True))")"
[ "$CR" = '[{"rota design show F12": {"mode": "first", "n": 8}, "rota init": {"mode": "last", "n": 3}}, {}]' ] || fail "crop rules parsed wrongly: $CR"
pass "videos.py reads crop: first/last N lines per command"

# every real episode parses
for f in "$REPO"/videos/episodes/*.md; do python3 "$VID" parse "$f" >/dev/null || fail "$f does not parse"; done
pass "every episode under videos/episodes/ parses"

# every form `rota version` can print compares equal across builds of different commits
fake_ver "$VD/v-vcs" "rota 1.2.3 (abc1234, 2026-01-01T00:00:00Z)"
fake_ver "$VD/v-vcs2" "rota 1.2.3 (def5678, 2026-02-02T00:00:00Z)"
fake_ver "$VD/v-ldflags" "rota 1.2.3"
fake_ver "$VD/v-dev" "rota dev"
mkdir -p "$VD/vroot/episodes"
printf -- '---\nnumber: 0\n---\n\n### 01 terminal\n\nvo: x\n\n```sh\nrota version\n```\n\nvertical: x\n' > "$VD/vroot/episodes/00-v.md"
cap() { python3 "$VID" capture "$VD/vroot/episodes/00-v.md" --bin "$1" | python3 -c "import json,sys;print(json.load(sys.stdin)['captures'][0]['stdout'].strip())"; }
[ "$(cap "$VD/v-vcs")" = "$(cap "$VD/v-vcs2")" ] || fail "builds of different commits should compare equal"
[ "$(cap "$VD/v-vcs")" = "$(cap "$VD/v-ldflags")" ] || fail "the stamped and unstamped forms should compare equal"
[ "$(cap "$VD/v-dev")" = "rota dev" ] || fail "the dev form should pass through"
[ "$(cap "$VD/v-vcs")" != "rota 9.9.9" ] || fail "sanity"
mkdir -p "$VD/vroot2"; cp -r "$VD/vroot/episodes" "$VD/vroot2/"
fake_ver "$VD/v-newver" "rota 2.0.0 (abc1234, 2026-01-01T00:00:00Z)"
[ "$(cap "$VD/v-newver")" != "$(cap "$VD/v-vcs")" ] || fail "a changed version number must still differ"
pass "the rota version stamp is masked in every form, and a changed version still differs"

# anim: lines (#2537): an optional animated scene per motion shot, parsed inside the grammar
AN="$VD/anim"; mkdir -p "$AN"
mkanim() { # <name> <anim line> [kind]: a one-shot episode; parse prints its anim
  printf -- '---\nnumber: 9\n---\n\n### 01 %s\n\nvo: x\n\n%s\n\nvertical: y\n' "${3:-motion}" "$2" > "$AN/$1.md"
}
mkanim ok 'anim: cards | items=a~b; c | last-tone=danger'
python3 "$VID" parse "$AN/ok.md" | python3 -c '
import json, sys
a = json.load(sys.stdin)["shots"][0]["anim"]
assert a == {"name": "cards", "params": {"items": "a~b; c", "last-tone": "danger"}}, a' \
  || fail "anim: should parse into a name and key=value params"
mkanim none 'scene: plain'
python3 "$VID" parse "$AN/none.md" | grep -q '"anim": null' || fail "a motion shot without anim: should keep anim null"
pass "videos parse reads an optional anim: line and leaves other motion shots a title card"
for bad in 'anim: wobble | x=1' 'anim: cards | nope=1' 'anim: cards | items' 'anim: cards | items=a | items=b'; do
  mkanim bad "$bad"
  rc=0; OUT="$(python3 "$VID" parse "$AN/bad.md" 2>&1)" || rc=$?
  { [ "$rc" = 1 ] && grep -q 'videos.py: .*shot 01' <<<"$OUT" && ! grep -q Traceback <<<"$OUT"; } \
    || fail "anim line '$bad' should be a grammar error (rc $rc): $OUT"
done
mkanim term 'anim: cards | items=a' terminal
printf '\n```sh\nrota version\n```\n' >> "$AN/term.md"
rc=0; OUT="$(python3 "$VID" parse "$AN/term.md" 2>&1)" || rc=$?
[ "$rc" = 1 ] && grep -q 'terminal shot with an anim' <<<"$OUT" || fail "anim: on a terminal shot should be refused (rc $rc): $OUT"
pass "videos parse refuses unknown scenes, unknown or repeated parameters and anim: on a terminal shot"
# the script hash covers the anim line, so changing a scene makes the episode stale
H1="$(python3 "$VID" parse "$AN/ok.md" | grep source_hash)"
sed -i 's/last-tone=danger/last-tone=ask/' "$AN/ok.md"
[ "$H1" != "$(python3 "$VID" parse "$AN/ok.md" | grep source_hash)" ] || fail "an anim edit should change the source hash"
pass "an anim: edit changes the script hash the drift check compares"
