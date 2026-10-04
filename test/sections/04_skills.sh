echo "block skills"
# Fresh CLAUDE.md — first run should create the block.
rm -f CLAUDE.md
"$ROTA_BIN" block skills >/dev/null
grep -q "<!-- rota-skills-start -->" CLAUDE.md || fail "block skills didn't write start marker"
grep -q "<!-- rota-skills-end -->" CLAUDE.md || fail "block skills didn't write end marker"
grep -q "Capture & pick" CLAUDE.md || fail "rota body missing canonical sections"
grep -q "rota knowledge query" CLAUDE.md || fail "rota body missing consult-points"
pass "block skills creates managed block with canonical body"

# Second run on existing CLAUDE.md with prior content — must update in place,
# not duplicate, and must preserve unrelated content above and below. The
# stale block carries the pre-rename `## hv-skills` heading (#231): the
# regenerated block must say `## rota`.
cat > CLAUDE.md <<'EOF'
# Project notes

Some pre-existing content.

<!-- rota-skills-start -->
## hv-skills

stale body — should be replaced.
<!-- rota-skills-end -->

Trailing content that must survive.
EOF
"$ROTA_BIN" block skills >/dev/null
[ "$(grep -c '<!-- rota-skills-start -->' CLAUDE.md)" = "1" ] || fail "block skills duplicated start marker"
grep -q "stale body" CLAUDE.md && fail "block skills didn't replace stale body"
grep -q "Some pre-existing content" CLAUDE.md || fail "block skills clobbered pre-block content"
grep -q "Trailing content that must survive" CLAUDE.md || fail "block skills clobbered post-block content"
grep -q "Capture & pick" CLAUDE.md || fail "block skills didn't write fresh body on update"
grep -qx "## rota" CLAUDE.md || fail "block skills didn't write the ## rota heading"
grep -qx "## hv-skills" CLAUDE.md && fail "block skills kept the old ## hv-skills heading"
pass "block skills updates in place and preserves unrelated content"

