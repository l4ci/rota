echo "T103 --repo flag guard — bare trailing --repo exits with usage error"

# Every repo-scoped knowledge/glossary verb MUST refuse a bare trailing --repo
# (--repo with no value) with exit 2. Pre-T103 the three glossary helpers
# silently set REPO_FLAG="", which auto-resolved to umbrella and masked the
# user's intent. The invocations are otherwise complete, so the usage error
# can only come from the dangling flag.
#
# Single-repo callers never pass --repo, so output for valid invocations
# is unchanged — this section only covers the guard.

for v in "knowledge query Architecture" \
         "knowledge add --topic Architecture --title t --body-file -" \
         "knowledge amend --topic Architecture --fragment f --mode append --body-file -" \
         "knowledge hit --topic Architecture --title t" \
         "knowledge tier get --topic Architecture --title t" \
         "knowledge tier set --topic Architecture --title t --tier confirmed" \
         "knowledge tier list" \
         "glossary read term" \
         "glossary write term --def d" \
         "glossary import --body-file -" \
         "block knowledge"; do
  rc=0
  ( cd "$TMP" && hvj $v --repo </dev/null >/dev/null 2>&1 ) || rc=$?
  [ "$rc" -eq 2 ] || fail "T103: rota $v accepted bare trailing --repo (rc=$rc; expected 2)"
done

# Verbs with no repo scope reject --repo itself, with a value too (exit 2).
for v in "knowledge stats" \
         "knowledge contradiction list" \
         "knowledge contradiction clear" \
         "knowledge contradiction has --topic Architecture --title t" \
         "knowledge contradiction add --topic Architecture --title t --text x"; do
  rc=0
  ( cd "$TMP" && hvj $v --repo web </dev/null >/dev/null 2>&1 ) || rc=$?
  [ "$rc" -eq 2 ] || fail "T103: rota $v accepted --repo although it has no repo scope (rc=$rc; expected 2)"
done

pass "T103 — scoped verbs reject bare trailing --repo; unscoped verbs reject --repo"
