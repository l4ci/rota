echo "knowledge/decisions query match topics partially; topics lists headings (#582)"

TP="$(mktemp -d "$TMP/topics.XXXXXX")"
mkdir -p "$TP/.rota"
printf '# K\n\n## Build & Tooling: Smoke testing\n\n- **Smoke one** — a. <!-- 2026-01-01 -->\n\n## Build & Tooling: Git\n\n- **Git one** — b. <!-- 2026-01-02 -->\n- **Git two** — c. <!-- 2026-01-03 -->\n\n## Build\n\n- **Build one** — d. <!-- 2026-01-04 -->\n' > "$TP/.rota/KNOWLEDGE.md"
cp "$TP/.rota/KNOWLEDGE.md" "$TP/.rota/DECISIONS.md"
tpr() { ( cd "$TP" && "$ROTA_BIN" "$@" 2>/dev/null ) || true; }

OUT=$(tpr knowledge query "smoke TESTING")
case "$OUT" in *"## Build & Tooling: Smoke testing"*) ;; *) fail "substring query should match: $OUT" ;; esac
case "$OUT" in *"Git one"*) fail "substring query leaked another topic: $OUT" ;; esac
OUT=$(tpr knowledge query "tooling")
case "$OUT" in *"Smoke one"*"Git two"*) ;; *) fail "category query should return the family: $OUT" ;; esac
case "$OUT" in *"Build one"*) fail "category query leaked Build: $OUT" ;; esac
OUT=$(tpr knowledge query "build")
case "$OUT" in *"Build one"*) ;; *) fail "exact heading should match: $OUT" ;; esac
case "$OUT" in *"Smoke one"*) fail "exact heading must win alone: $OUT" ;; esac
OUT=$(tpr decisions query "git")
case "$OUT" in *"Git two"*) ;; *) fail "decisions query should match partially: $OUT" ;; esac

WANT=$'Build & Tooling: Smoke testing: 1 bullets\nBuild & Tooling: Git: 2 bullets\nBuild: 1 bullets'
[ "$(tpr knowledge topics)" = "$WANT" ] || fail "knowledge topics text wrong: $(tpr knowledge topics)"
[ "$(tpr decisions topics)" = "$WANT" ] || fail "decisions topics text wrong"
OUT=$(tpr --json knowledge topics)
[ "$(echo "$OUT" | jget 'data.topics[1].bullets')" = "2" ] || fail "knowledge topics --json wrong: $OUT"
OUT=$(tpr --json decisions topics)
[ "$(echo "$OUT" | jget 'data.topics[0].name')" = "Build & Tooling: Smoke testing" ] || fail "decisions topics --json wrong: $OUT"

# block knowledge writes the block into the instructions files; a query must not change them.
tpr block knowledge >/dev/null
B1=$(cat "$TP"/AGENTS.md "$TP"/CLAUDE.md 2>/dev/null)
[ -n "$B1" ] || fail "block knowledge wrote no instructions file"
tpr knowledge query "tooling" >/dev/null
tpr block knowledge >/dev/null
B2=$(cat "$TP"/AGENTS.md "$TP"/CLAUDE.md 2>/dev/null)
[ "$B1" = "$B2" ] || fail "block knowledge output changed across a query"
