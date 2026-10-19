echo "round scope: inferred scope is recorded, shown, beaten by Touches and warn-only (#900)"

# File-mode project, scope open. F01 is held by slot ben; F02 has no scope; F03
# declares the same entry F01 does.
SC="$(mktemp -d "$TMP/round-scope.XXXXXX")"
(
  cd "$SC" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && mkdir -p .rota \
    && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
) || fail "scope fixture setup failed"
sc() { ( cd "$SC" && "$ROTA_BIN" --json "$@" 2>/dev/null ); }
sc config set round.scope open >/dev/null || vfail
sc config set round.scopeOverlap block >/dev/null || vfail
sc item create --kind features --title Holder --body-file - <<<$'## Acceptance\n- [ ] a\n\n## Touches\n- POST /items\n' >/dev/null || vfail
sc item create --kind features --title Bare --body-file - <<<$'## Acceptance\n- [ ] b\n' >/dev/null || vfail
sc item create --kind features --title Declared --body-file - <<<$'## Acceptance\n- [ ] c\n\n## Touches\n- other\n' >/dev/null || vfail
printf '{"slots":[{"name":"ben","task":"F01","branch":"ben/F01-holder"}]}\n' > "$SC/.rota/workers.json"

OUT=$(sc round candidates) || vfail
[ "$(echo "$OUT" | jget data.candidates[0].scopeSource)" = "none" ] || fail "F02 declares nothing: $OUT"
[ "$(echo "$OUT" | jget data.candidates[1].scopeSource)" = "touches" ] || fail "F03 declares Touches: $OUT"

printf -- '- POST /items\n- Worker.Slot\n' > "$SC/inferred.txt"
OUT=$(sc round scope F02 --body-file "$SC/inferred.txt") || vfail
[ "$(echo "$OUT" | jget data.source)" = "inferred" ] || fail "scope should record: $OUT"
OUT=$(sc round candidates) || vfail
[ "$(echo "$OUT" | jget data.candidates[0].scopeSource)" = "inferred" ] || fail "F02 should be inferred: $OUT"
[ "$(echo "$OUT" | jget data.candidates[0].ready)" = "true" ] || fail "an inferred clash must warn, not block, under scopeOverlap block: $OUT"
[ "$(echo "$OUT" | jget data.candidates[0].overlaps[0].inferred)" = "true" ] || fail "the overlap should say inferred: $OUT"
grep -q 'inferred scopes post /items' <<<"$OUT" || fail "detail should say inferred scopes: $OUT"
pass "recorded scope is shown and its clash only warns"

# A declared Touches section on the same item wins over the recorded one.
printf '\n## Touches\n- other\n' >> "$SC/.rota/features/F02.md"
OUT=$(sc round candidates) || vfail
[ "$(echo "$OUT" | jget data.candidates[0].scopeSource)" = "touches" ] || fail "declared Touches must win: $OUT"
[ "$(echo "$OUT" | jget data.candidates[0].overlaps | tr -d ' \n')" = "" ] || [ "$(echo "$OUT" | jget data.candidates[0].overlaps)" = "None" ] || fail "no clash once Touches differs: $OUT"
pass "declared Touches wins over inferred"
