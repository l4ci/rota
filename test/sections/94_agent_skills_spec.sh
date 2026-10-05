echo "E2/F6a: Agent Skills frontmatter lint and rota skills install (#69, #230)"
# test/validate-skills.py checks every SKILL.md against the Agent Skills spec;
# `rota skills install|update|uninstall|status` copies the embedded skills into
# the Claude and Codex skill directories. Every case points HOME at a mktemp
# dir, so nothing here touches the real ~/.claude or ~/.agents.
VALIDATE="$TESTDIR/validate-skills.py"
SPEC_TMP="$(mktemp -d)"
trap 'rm -rf "$SPEC_TMP"' EXIT
SK='SKILL''.md'

# A fixture repo with one skill whose frontmatter the caller supplies.
sp_fixture() {
  local d="$1"
  mkdir -p "$d/skills/rota-a" "$d/skills/references"
  printf -- '---\n%b---\n\nbody\n' "$2" > "$d/skills/rota-a/$SK"
}
# sp_run <dir> [pending] prints the validator output and returns its exit code.
sp_run() { ( cd "$1" && ROTA_DOCLINT_PROSE=off ROTA_SPEC_PENDING="${2:-}" python3 "$VALIDATE" 2>&1 ); }

# (a) a compliant skill passes, optional spec keys included
sp_fixture "$SPEC_TMP/ok" 'name: rota-a\ndescription: Does a thing.\nlicense: MIT\ncompatibility: needs rota\nmetadata:\n  owner: l4ci\nallowed-tools: Bash\n'
OUT="$(sp_run "$SPEC_TMP/ok")" || fail "E2[a]: lint flagged a compliant skill: $OUT"
sp_fixture "$SPEC_TMP/folded" 'name: rota-a\ndescription: >\n  Folded text\n  over lines.\n'
OUT="$(sp_run "$SPEC_TMP/folded")" || fail "E2[a]: lint flagged a folded description: $OUT"
sp_fixture "$SPEC_TMP/quoted" 'name: rota-a\ndescription: >-\n  Use on "you are the orchestrator" or "can I ship".\n'
OUT="$(sp_run "$SPEC_TMP/quoted")" || fail "E2[a]: lint flagged a second-person word inside a quoted trigger: $OUT"
pass "E2[a]: a compliant skill passes, optional spec keys, folded descriptions and quoted triggers included"

# (b) each rule fails, and the message names the file and the rule
LONG="$(python3 -c 'print("x" * 1025)')"
OVERCAP="$(python3 -c 'print("x" * 351)')"
n=0
while IFS='|' read -r want fm; do
  n=$((n + 1)); F="$SPEC_TMP/bad$n"; sp_fixture "$F" "$fm"
  RC=0; OUT="$(sp_run "$F")" || RC=$?
  [ "$RC" = 1 ] || fail "E2[b]: lint passed '$want' (rc $RC): $OUT"
  grep -qF "skills/rota-a/$SK" <<<"$OUT" || fail "E2[b]: lint did not name the file for '$want': $OUT"
  grep -qF "$want" <<<"$OUT" || fail "E2[b]: lint did not report '$want': $OUT"
done <<EOF
must equal the directory|name: rota-b\ndescription: ok\n
lowercase letters, digits|name: Hv-a\ndescription: ok\n
lowercase letters, digits|name: hv--a\ndescription: ok\n
the spec allows 1024|name: rota-a\ndescription: $LONG\n
the cap is 350|name: rota-a\ndescription: $OVERCAP\n
not in the Agent Skills spec|name: rota-a\ndescription: ok\nuser-invocable: true\n
missing required key 'description'|name: rota-a\n
missing required key 'name'|description: ok\n
not valid YAML unquoted|name: rota-a\ndescription: Links GH: 12 refs.\n
not valid YAML unquoted|name: rota-a\ndescription: Links issue #12 refs.\n
third person|name: rota-a\ndescription: Use when you need to hand off.\n
third person|name: rota-a\ndescription: Use when I want a plan.\n
EOF
pass "E2[b]: a wrong name, a long description, an over-cap description, an unknown key, a missing key, invalid unquoted YAML and a second-person description each fail"

# (b2) a reference over 100 lines needs a ## Contents section near the top, so a
# partial read still shows its scope (Anthropic skill authoring guide, #248)
sp_fixture "$SPEC_TMP/toc" 'name: rota-a\ndescription: ok\n'
{ echo '# Long'; for i in $(seq 1 100); do echo "line $i"; done; } > "$SPEC_TMP/toc/skills/references/long.md"
RC=0; OUT="$(sp_run "$SPEC_TMP/toc")" || RC=$?
[ "$RC" = 1 ] && grep -qF "skills/references/long.md: 101 lines with no '## Contents'" <<<"$OUT" || fail "E2[b2]: a long reference without contents passed (rc $RC): $OUT"
{ echo '# Long'; echo; echo '## Contents'; for i in $(seq 1 100); do echo "line $i"; done; } > "$SPEC_TMP/toc/skills/references/long.md"
OUT="$(sp_run "$SPEC_TMP/toc")" || fail "E2[b2]: a long reference with contents was flagged: $OUT"
pass "E2[b2]: a reference over 100 lines fails without a ## Contents section and passes with one"

# (c) the transitional sets excuse what is listed, and only that
sp_fixture "$SPEC_TMP/pend" 'name: rota-a\ndescription: ok\nuser-invocable: true\n'
OUT="$(sp_run "$SPEC_TMP/pend" "user-invocable")" || fail "E2[c]: a pending key was flagged: $OUT"
sp_fixture "$SPEC_TMP/clean" 'name: rota-a\ndescription: ok\n'
RC=0; OUT="$(sp_run "$SPEC_TMP/clean" "user-invocable")" || RC=$?
[ "$RC" = 1 ] && grep -qF "uses 'user-invocable' any more" <<<"$OUT" || fail "E2[c]: an unused pending key passed (rc $RC): $OUT"
sp_fixture "$SPEC_TMP/plong" "name: rota-a\ndescription: $LONG\n"
OUT="$(sp_run "$SPEC_TMP/plong" "skills/rota-a/$SK")" || fail "E2[c]: a pending long description was flagged: $OUT"
RC=0; OUT="$(sp_run "$SPEC_TMP/ok" "skills/rota-a/$SK")" || RC=$?
[ "$RC" = 1 ] && grep -qF "remove it from PENDING_LONG" <<<"$OUT" || fail "E2[c]: an unused PENDING_LONG entry passed (rc $RC): $OUT"
pass "E2[c]: pending entries excuse only what they name, and a stale entry fails"

# hs <home> <dir> <args...> runs `rota skills` in <dir> with HOME=<home> and
# CLAUDE_CONFIG_DIR=$HS_CFG (unset when empty); RC and OUT
# hold the exit code and the --json envelope.
hs() {
  local h="$1" d="$2"; shift 2
  RC=0; OUT="$( cd "$d" && HOME="$h" CLAUDE_CONFIG_DIR="${HS_CFG:-}" "$ROTA_BIN" --json skills "$@" 2>/dev/null )" || RC=$?
}
SPEC_HOME="$SPEC_TMP/home"; mkdir -p "$SPEC_HOME" "$SPEC_TMP/cwd"
CL="$SPEC_HOME/.claude/skills"; AG="$SPEC_HOME/.agents/skills"

# (d) install writes both roots, self-contained, with a manifest; status is current
hs "$SPEC_HOME" "$SPEC_TMP/cwd" status --scope user
[ "$(jget data.roots[0].installed <<<"$OUT")" = "false" ] || fail "F6a[d]: status before install: ${OUT:0:300}"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" install
[ "$RC" = 0 ] || fail "F6a[d]: install exited $RC: ${OUT:0:300}"
[ "$(jget data.roots[0].agent <<<"$OUT")" = "claude" ] && [ "$(jget data.roots[1].agent <<<"$OUT")" = "codex" ] || fail "F6a[d]: install roots: ${OUT:0:300}"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "F6a[d]: first install changed nothing: ${OUT:0:300}"
for r in "$CL" "$AG"; do
  [ -f "$r/rota-pause/$SK" ] && [ -f "$r/.rota-manifest.json" ] || fail "F6a[d]: $r lacks rota-pause or the manifest"
done
hs "$SPEC_HOME" "$SPEC_TMP/cwd" status --scope user
[ "$(jget data.roots[0].current <<<"$OUT")" = "true" ] && [ "$(jget data.roots[1].current <<<"$OUT")" = "true" ] || fail "F6a[d]: status not current: ${OUT:0:300}"
# every references/<name>.md an installed file cites, and that exists in the
# repo's skills/references/, sits inside that skill's own directory
for sk in "$CL"/rota-*; do
  for f in "$sk"/*.md "$sk"/references/*.md; do
    CITED="$(grep -o 'references/[A-Za-z0-9._-]*\.md' "$f" || true)"
    for c in $CITED; do
      [ -f "$REPO/skills/$c" ] || continue
      [ -f "$sk/$c" ] || fail "F6a[d]: $f cites $c, missing from $sk"
    done
  done
done
hs "$SPEC_HOME" "$SPEC_TMP/cwd" install
[ "$RC" = 0 ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "F6a[d]: second install was not a no-op (rc $RC): ${OUT:0:300}"
pass "F6a[d]: rota skills install writes both roots with references and a manifest, and is idempotent"

# (e) an edit survives update (exit 4); --overwrite replaces it
echo "mine" > "$CL/rota-pause/$SK"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" update
[ "$RC" = 4 ] && [ "$(jget data.blockedBy <<<"$OUT")" = "edited" ] || fail "F6a[e]: update over an edit (rc $RC): ${OUT:0:300}"
[ "$(cat "$CL/rota-pause/$SK")" = "mine" ] || fail "F6a[e]: update overwrote an edited file"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" status --scope user
[ "$(jget data.roots[0].edited[0] <<<"$OUT")" = "rota-pause/$SK" ] || fail "F6a[e]: status does not list the edit: ${OUT:0:300}"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" update --overwrite
[ "$RC" = 0 ] && [ "$(cat "$CL/rota-pause/$SK")" != "mine" ] || fail "F6a[e]: --overwrite did not replace the edit (rc $RC): ${OUT:0:300}"
# an edited file in the codex root is kept the same way
rm "$AG/rota-spike/$SK" && printf '%s' "theirs" > "$AG/rota-spike/$SK"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" install
[ "$RC" = 4 ] && [ "$(jget data.blockedBy <<<"$OUT")" = "edited" ] && [ "$(cat "$AG/rota-spike/$SK")" = "theirs" ] || fail "F6a[e]: an edited codex file was not kept (rc $RC): ${OUT:0:300}"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" install --overwrite
[ "$RC" = 0 ] || fail "F6a[e]: install --overwrite exited $RC: ${OUT:0:300}"
pass "F6a[e]: an edit is kept with exit 4 and replaced with --overwrite"

# (f) uninstall removes what rota wrote, keeps an edit (exit 4), never a stranger
echo "mine" > "$CL/rota-pause/$SK"
printf '%s' "stranger" > "$CL/rota-work/mine.md"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" uninstall
[ "$RC" = 4 ] && [ "$(jget data.blockedBy <<<"$OUT")" = "edited" ] || fail "F6a[f]: uninstall over an edit (rc $RC): ${OUT:0:300}"
[ "$(cat "$CL/rota-pause/$SK")" = "mine" ] && [ -f "$CL/rota-work/mine.md" ] || fail "F6a[f]: uninstall removed an edited or unmanaged file"
[ ! -e "$AG/rota-pause" ] && [ ! -e "$AG/.rota-manifest.json" ] || fail "F6a[f]: the unedited codex root was not removed"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" uninstall --overwrite
[ "$RC" = 0 ] && [ ! -e "$CL/rota-pause" ] && [ ! -e "$CL/.rota-manifest.json" ] || fail "F6a[f]: uninstall --overwrite left files (rc $RC): ${OUT:0:300}"
[ -f "$CL/rota-work/mine.md" ] || fail "F6a[f]: uninstall removed an unmanaged file"
hs "$SPEC_HOME" "$SPEC_TMP/cwd" update
[ "$RC" = 1 ] || fail "F6a[f]: update with nothing installed exited $RC, want 1: ${OUT:0:300}"
pass "F6a[f]: uninstall removes the manifest paths, keeps edits and strangers, and update then exits 1"

# (g) project scope: the git toplevel from a subdirectory; exit 3 outside a repo
PROJ="$SPEC_TMP/proj"; mkdir -p "$PROJ/sub/dir"; git -C "$PROJ" init -q
hs "$SPEC_HOME" "$SPEC_TMP/cwd" install --scope project
[ "$RC" = 3 ] || fail "F6a[g]: project scope outside a repo exited $RC, want 3: ${OUT:0:300}"
hs "$SPEC_HOME" "$PROJ/sub/dir" install --scope project --agent codex
[ "$RC" = 0 ] || fail "F6a[g]: project install exited $RC: ${OUT:0:300}"
[ -f "$PROJ/.agents/skills/rota-pause/$SK" ] && [ -f "$PROJ/.agents/skills/.rota-manifest.json" ] || fail "F6a[g]: the project root is not at the toplevel"
[ ! -e "$PROJ/.claude" ] && [ ! -e "$AG/rota-pause" ] || fail "F6a[g]: --agent codex --scope project wrote elsewhere"
hs "$SPEC_HOME" "$PROJ" status
[ "$(jget data.roots[3].installed <<<"$OUT")" = "true" ] || fail "F6a[g]: status does not see the project root: ${OUT:0:300}"
# a legacy `rota init --codex` symlink is replaced, not reported as unmanaged
hs "$SPEC_HOME" "$PROJ" uninstall --scope project
mkdir -p "$SPEC_TMP/legacy/rota-pause"; printf -- '---\nname: rota-pause\n---\n' > "$SPEC_TMP/legacy/rota-pause/$SK"
ln -s "$SPEC_TMP/legacy/rota-pause" "$PROJ/.agents/skills/rota-pause"
hs "$SPEC_HOME" "$PROJ" install --scope project --agent codex
[ "$RC" = 0 ] && [ ! -L "$PROJ/.agents/skills/rota-pause" ] && [ -f "$PROJ/.agents/skills/rota-pause/$SK" ] || fail "F6a[g]: the legacy symlink was not replaced (rc $RC): ${OUT:0:300}"
grep -q 'name: rota-pause' "$SPEC_TMP/legacy/rota-pause/$SK" || fail "F6a[g]: install wrote through the legacy symlink"
pass "F6a[g]: project scope installs at the git toplevel, exits 3 outside a repo and replaces a legacy symlink"

# (h) doctor: skip with nothing installed, pass when current, fail on an edit
DRB="$SPEC_TMP/drbin"; mkdir -p "$DRB"; ln -s "$(command -v git)" "$DRB/git"
DHOME="$SPEC_TMP/dhome"; DREPO="$SPEC_TMP/drepo"; mkdir -p "$DHOME" "$DREPO"; git -C "$DREPO" init -q
# dr runs doctor (RC, OUT); dsk <field> reads a field of its skills check.
dr() { RC=0; OUT="$( cd "$DREPO" && HOME="$DHOME" CLAUDE_CONFIG_DIR= ROTA_TEST_DOCTOR_PATH="$DRB" "$ROTA_BIN" --json doctor 2>/dev/null )" || RC=$?; }
dsk() {
  python3 -c '
import json, sys
cs = {c["name"]: c for c in json.load(sys.stdin)["data"]["checks"]}
print(cs["skills"].get(sys.argv[1], "ABSENT"))' "$1" <<<"$OUT"
}
dr
[ "$(dsk status)" = "skip" ] || fail "F6a[h]: doctor should skip with nothing installed: ${OUT:0:300}"
case "$(dsk detail)" in *"rota skills install"*) ;; *) fail "F6a[h]: skip detail lacks the install command: ${OUT:0:300}" ;; esac
hs "$DHOME" "$DREPO" install --agent claude
dr
[ "$(dsk status)" = "pass" ] || fail "F6a[h]: doctor should pass on a current install: ${OUT:0:300}"
echo "mine" > "$DHOME/.claude/skills/rota-work/$SK"
dr
[ "$(dsk status)" = "fail" ] && [ "$RC" = 1 ] || fail "F6a[h]: doctor should fail on an edited file (rc $RC): ${OUT:0:300}"
[ "$(dsk hint)" = "run: rota skills update --overwrite" ] || fail "F6a[h]: doctor hint: $(dsk hint)"
pass "F6a[h]: doctor skips until installed, passes when current, fails on an edit"

# (i) CLAUDE_CONFIG_DIR moves the Claude root only; no HOME exits 3
CFGH="$SPEC_TMP/cfghome"; CFGD="$SPEC_TMP/cfgdir"; mkdir -p "$CFGH" "$CFGD"
HS_CFG="$CFGD" hs "$CFGH" "$SPEC_TMP/cwd" install
[ "$RC" = 0 ] && [ -f "$CFGD/skills/rota-pause/$SK" ] && [ -f "$CFGH/.agents/skills/rota-pause/$SK" ] && [ ! -e "$CFGH/.claude" ] || fail "F6a[i]: CLAUDE_CONFIG_DIR not honored (rc $RC): ${OUT:0:300}"
RC=0; ( cd "$SPEC_TMP/cwd" && env -u HOME CLAUDE_CONFIG_DIR= "$ROTA_BIN" --json skills install >/dev/null 2>&1 ) || RC=$?
[ "$RC" = 3 ] || fail "F6a[i]: install without HOME exited $RC, want 3"
pass "F6a[i]: CLAUDE_CONFIG_DIR moves the Claude root, and an unknown home exits 3"

# Codex discovery itself is a manual check, not a smoke step: the runner puts a
# poison codex on PATH so no section runs the real CLI (E1). The probe is in
# docs/usage/codex-skills.md and its result is recorded in the PR.

trap 'rm -rf "$TMP"' EXIT
rm -rf "$SPEC_TMP"
