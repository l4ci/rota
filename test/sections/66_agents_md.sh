echo "F84: managed blocks target AGENTS.md when present, else CLAUDE.md"

TMP_AG="$(mktemp -d)"
trap 'rm -rf "$TMP_AG"' EXIT
mkdir -p "$TMP_AG/.rota"
printf '# Knowledge\n\n## Architecture\n- x\n' > "$TMP_AG/.rota/KNOWLEDGE.md"
blk() { "$ROTA_BIN" -C "$TMP_AG" block "$@"; }

# (a) no AGENTS.md -> CLAUDE.md, exactly as before
printf 'vision body\n' | blk vision --body-file - >/dev/null
blk knowledge >/dev/null
grep -q "<!-- rota-vision-start -->" "$TMP_AG/CLAUDE.md" || fail "F84[a]: vision block missing from CLAUDE.md"
grep -q "^- Architecture" "$TMP_AG/CLAUDE.md" || fail "F84[a]: knowledge block missing from CLAUDE.md"
[ ! -e "$TMP_AG/AGENTS.md" ] || fail "F84[a]: AGENTS.md must not be created"
pass "F84[a]: no AGENTS.md -> blocks land in CLAUDE.md"

# (b) AGENTS.md present -> blocks land there, CLAUDE.md untouched
printf '# Agents\n' > "$TMP_AG/AGENTS.md"
cp "$TMP_AG/CLAUDE.md" "$TMP_AG/CLAUDE.before"
printf 'vision body\n' | blk vision --body-file - >/dev/null
blk knowledge >/dev/null
grep -q "<!-- rota-vision-start -->" "$TMP_AG/AGENTS.md" || fail "F84[b]: vision block missing from AGENTS.md"
grep -q "^- Architecture" "$TMP_AG/AGENTS.md" || fail "F84[b]: knowledge block missing from AGENTS.md"
cmp -s "$TMP_AG/CLAUDE.md" "$TMP_AG/CLAUDE.before" || fail "F84[b]: CLAUDE.md must be untouched"
pass "F84[b]: AGENTS.md present -> blocks land in AGENTS.md, CLAUDE.md untouched"

# (c) sub-repo scope resolves per sub-repo dir
mkdir -p "$TMP_AG/web"
printf '{"repos":[{"name":"web","path":"./web"}]}' > "$TMP_AG/.rota/repos.json"
printf '# web agents\n' > "$TMP_AG/web/AGENTS.md"
blk knowledge --repo web >/dev/null
grep -q "<!-- rota-knowledge-start -->" "$TMP_AG/web/AGENTS.md" || fail "F84[c]: sub-repo block missing from web/AGENTS.md"
[ ! -e "$TMP_AG/web/CLAUDE.md" ] || fail "F84[c]: web/CLAUDE.md must not be created"
pass "F84[c]: sub-repo scope honours the sub-repo's AGENTS.md"

trap 'rm -rf "$TMP"' EXIT
rm -rf "$TMP_AG"

echo "F84: instructions init sets up AGENTS.md + CLAUDE.md @AGENTS.md"

TMP_II="$(mktemp -d)"
trap 'rm -rf "$TMP_II"' EXIT
# Prints one `action:file[:keys]` line per action, so the cases below compare plain text.
actions_of() { python3 -c 'import json,sys; [print(a["action"]+":"+a["file"]+(":"+",".join(a["keys"]) if "keys" in a else "")) for a in json.load(sys.stdin)["data"]["actions"]]'; }
init_ii() { hvj -C "$1" instructions init | actions_of; }

# (d) fresh dir: both files created, blocks then land in AGENTS.md
mkdir -p "$TMP_II/fresh/.rota"
printf '# Knowledge\n\n## Architecture\n- x\n' > "$TMP_II/fresh/.rota/KNOWLEDGE.md"
OUT="$(init_ii "$TMP_II/fresh")"
[ "$OUT" = "$(printf 'created:AGENTS.md\ncreated:CLAUDE.md')" ] || fail "F84[d]: unexpected output: $OUT"
grep -qx '@AGENTS.md' "$TMP_II/fresh/CLAUDE.md" || fail "F84[d]: CLAUDE.md missing @AGENTS.md"
"$ROTA_BIN" -C "$TMP_II/fresh" block knowledge >/dev/null
grep -q "<!-- rota-knowledge-start -->" "$TMP_II/fresh/AGENTS.md" || fail "F84[d]: block not in AGENTS.md"
if grep -q "rota-knowledge" "$TMP_II/fresh/CLAUDE.md"; then fail "F84[d]: block leaked into CLAUDE.md"; fi
pass "F84[d]: fresh dir -> both files created, blocks land in AGENTS.md"

# (e) CLAUDE.md with user text + managed blocks (dashed and legacy): blocks move, text stays
mkdir -p "$TMP_II/mig/.rota"
cat > "$TMP_II/mig/CLAUDE.md" <<'MD'
# My project

User rules here.

<!-- rota-knowledge-start -->
## Project Knowledge
- Architecture
<!-- rota-knowledge-end -->

More user text.

<!-- hv-skills-start -->
legacy skills block
<!-- hv-skills-end -->
MD
OUT="$(init_ii "$TMP_II/mig")"
grep -q '^created:AGENTS.md$' <<<"$OUT" || fail "F84[e]: AGENTS.md not reported created"
grep -q '^moved:AGENTS.md:knowledge,skills$' <<<"$OUT" || fail "F84[e]: moved line wrong: $OUT"
grep -q '^linked:CLAUDE.md$' <<<"$OUT" || fail "F84[e]: linked line missing: $OUT"
grep -q "rota-knowledge-start" "$TMP_II/mig/AGENTS.md" || fail "F84[e]: knowledge block not in AGENTS.md"
grep -q "legacy skills block" "$TMP_II/mig/AGENTS.md" || fail "F84[e]: legacy block not in AGENTS.md"
if grep -q "rota-knowledge\|hv-skills-start" "$TMP_II/mig/CLAUDE.md"; then fail "F84[e]: blocks remain in CLAUDE.md"; fi
grep -q "User rules here." "$TMP_II/mig/CLAUDE.md" || fail "F84[e]: user text lost"
grep -q "More user text." "$TMP_II/mig/CLAUDE.md" || fail "F84[e]: user text lost"
[ "$(grep -cx '@AGENTS.md' "$TMP_II/mig/CLAUDE.md")" = "1" ] || fail "F84[e]: @AGENTS.md not added exactly once"
if grep -Pzq '\n\n\n\n' "$TMP_II/mig/CLAUDE.md"; then fail "F84[e]: blank lines not collapsed"; fi
pass "F84[e]: managed blocks moved, user text kept, @AGENTS.md added once"

# (f) second run: no output, files byte-identical
cp "$TMP_II/mig/CLAUDE.md" "$TMP_II/mig.claude"; cp "$TMP_II/mig/AGENTS.md" "$TMP_II/mig.agents"
OUT="$(init_ii "$TMP_II/mig")"
[ -z "$OUT" ] || fail "F84[f]: second run printed: $OUT"
cmp -s "$TMP_II/mig/CLAUDE.md" "$TMP_II/mig.claude" || fail "F84[f]: CLAUDE.md changed"
cmp -s "$TMP_II/mig/AGENTS.md" "$TMP_II/mig.agents" || fail "F84[f]: AGENTS.md changed"
pass "F84[f]: second run is a silent no-op"

# (g) AGENTS.md exists, CLAUDE.md lacks reference: reference added only
mkdir -p "$TMP_II/ex/.rota"
printf '# Agents\n\n<!-- rota-skills-start -->\nkeep\n<!-- rota-skills-end -->\n' > "$TMP_II/ex/AGENTS.md"
printf '# Notes\n\n<!-- rota-qa-start -->\nstay\n<!-- rota-qa-end -->\n' > "$TMP_II/ex/CLAUDE.md"
cp "$TMP_II/ex/AGENTS.md" "$TMP_II/ex.agents"
OUT="$(init_ii "$TMP_II/ex")"
[ "$OUT" = "linked:CLAUDE.md" ] || fail "F84[g]: unexpected output: $OUT"
cmp -s "$TMP_II/ex/AGENTS.md" "$TMP_II/ex.agents" || fail "F84[g]: AGENTS.md must be untouched"
grep -q "stay" "$TMP_II/ex/CLAUDE.md" || fail "F84[g]: CLAUDE.md content must be kept (no block move)"
[ "$(grep -cx '@AGENTS.md' "$TMP_II/ex/CLAUDE.md")" = "1" ] || fail "F84[g]: reference not added once"
pass "F84[g]: existing AGENTS.md -> only the CLAUDE.md reference is added"

# (h) symlinked pair: untouched
mkdir -p "$TMP_II/sym/.rota"
printf '# Agents\n' > "$TMP_II/sym/AGENTS.md"
ln -s AGENTS.md "$TMP_II/sym/CLAUDE.md"
OUT="$(init_ii "$TMP_II/sym")"
grep -q '^skippedSymlink:' <<<"$OUT" || fail "F84[h]: expected skippedSymlink, got: $OUT"
[ "$(cat "$TMP_II/sym/AGENTS.md")" = "# Agents" ] || fail "F84[h]: AGENTS.md modified through symlink"
[ -L "$TMP_II/sym/CLAUDE.md" ] || fail "F84[h]: CLAUDE.md symlink replaced"
pass "F84[h]: symlinked pair left untouched"

# (i) CLAUDE.md held only managed blocks: it becomes the plain stub
mkdir -p "$TMP_II/only/.rota"
printf '<!-- rota-knowledge-start -->\nx\n<!-- rota-knowledge-end -->\n' > "$TMP_II/only/CLAUDE.md"
init_ii "$TMP_II/only" >/dev/null
[ "$(cat "$TMP_II/only/CLAUDE.md")" = "$(printf '# CLAUDE.md\n\nProject instructions live in AGENTS.md.\n\n@AGENTS.md')" ] \
  || fail "F84[i]: CLAUDE.md is not the stub: $(cat "$TMP_II/only/CLAUDE.md")"
grep -q "rota-knowledge-start" "$TMP_II/only/AGENTS.md" || fail "F84[i]: block not moved"
pass "F84[i]: blocks-only CLAUDE.md becomes the stub"

trap 'rm -rf "$TMP"' EXIT
rm -rf "$TMP_II"
