#!/usr/bin/env bash
# Section 32 — rota issues — helper existence + provider detection + manual-gate callouts.
# F38 local-trap convention: each tmp tree installs its own trap; restore global before
# the section's final pass line. (Helpers pass/fail and $REPO/$TMP from runner.sh/lib.sh.)
set -euo pipefail

# === Provider detection unit tests ===
echo "Section 32: issues provider classification"
TMP_PROV="$(mktemp -d)"
trap 'rm -rf "$TMP_PROV"; trap '"'"'rm -rf "$TMP"'"'"' EXIT' EXIT

(
  cd "$TMP_PROV"
  mkdir -p .rota
  git init -q
  git config user.email t@t
  git config user.name t

  # Test 1: github.com SSH
  git remote add origin "git@github.com:foo/bar.git"
  out=$("$ROTA_BIN" --json issues provider | jget data.provider) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$out" = "github" ] || { echo "github.com SSH → $out, expected github"; exit 1; }

  # Test 2: gitlab.com HTTPS
  git remote set-url origin "https://gitlab.com/foo/bar.git"
  out=$("$ROTA_BIN" --json issues provider | jget data.provider) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$out" = "gitlab" ] || { echo "gitlab.com HTTPS → $out, expected gitlab"; exit 1; }

  # Test 3: self-hosted GitLab SSH
  git remote set-url origin "git@gitlab.example.com:foo/bar.git"
  out=$("$ROTA_BIN" --json issues provider | jget data.provider) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$out" = "gitlab" ] || { echo "self-hosted gitlab SSH → $out, expected gitlab"; exit 1; }

  # Test 4: GitHub Enterprise HTTPS
  git remote set-url origin "https://github.company.com/foo/bar.git"
  out=$("$ROTA_BIN" --json issues provider | jget data.provider) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$out" = "github" ] || { echo "GH Enterprise HTTPS → $out, expected github"; exit 1; }

  # Test 5: no origin at all
  git remote remove origin
  out=$("$ROTA_BIN" --json issues provider | jget data.provider) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
  [ "$out" = "unknown" ] || { echo "no origin → $out, expected unknown"; exit 1; }
) || fail "issues provider classification failed (see subshell output above)"

trap 'rm -rf "$TMP"' EXIT
pass "issues provider classifies github/gitlab/unknown across 5 fixtures"

# === issues imported smoke ===
echo "Section 32: issues imported smoke"
TMP_IMP="$(mktemp -d)"
trap 'rm -rf "$TMP_IMP"; trap '"'"'rm -rf "$TMP"'"'"' EXIT' EXIT

mkdir -p "$TMP_IMP/.rota/bugs" "$TMP_IMP/.rota/features" "$TMP_IMP/.rota/tasks"

# Seed a minimal BACKLOG.md with one GH and one GL cross-reference
cat > "$TMP_IMP/.rota/BACKLOG.md" <<'EOF'
# TODO

## Features

- **[F01] [Major] Test feature.** Body. GH: #999 Repos: web

## Bugs

- **[B01] [P1] Test bug.** GL: #42

## Tasks

## Completed
EOF

# Run from inside the fake tree so the walk-up resolves .rota/
out=$(cd "$TMP_IMP" && hvj issues imported) || \
  fail "issues imported exited non-zero on fixture BACKLOG"

count=$(echo "$out" | jq '.data.entries | length')
[ "$count" = "2" ] || fail "issues imported expected 2 entries, got $count — output: $out"

echo "$out" | jq -e '.data.entries[] | select(.issue == 999 and .provider == "github" and .itemId == "F01" and .repo == "web" and .status == "open")' >/dev/null || \
  fail "issues imported missing github #999 entry"

echo "$out" | jq -e '.data.entries[] | select(.issue == 42 and .provider == "gitlab" and .itemId == "B01" and .repo == null)' >/dev/null || \
  fail "issues imported missing gitlab #42 entry"

# Remove the GH bullet; re-run; expect length 1
cat > "$TMP_IMP/.rota/BACKLOG.md" <<'EOF'
# TODO

## Features

## Bugs

- **[B01] [P1] Test bug.** GL: #42

## Tasks

## Completed
EOF

out2=$(cd "$TMP_IMP" && hvj issues imported) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
count2=$(echo "$out2" | jq '.data.entries | length')
[ "$count2" = "1" ] || fail "issues imported expected 1 entry after removing GH bullet, got $count2"

trap 'rm -rf "$TMP"' EXIT
pass "issues imported indexes GH/GL refs from a fixture BACKLOG"

# === issues imported --open-only smoke (F77) ===
echo "Section 32: issues imported --open-only flag"
TMP_OO="$(mktemp -d)"
trap 'rm -rf "$TMP_OO"; trap '"'"'rm -rf "$TMP"'"'"' EXIT' EXIT

mkdir -p "$TMP_OO/.rota/bugs" "$TMP_OO/.rota/features" "$TMP_OO/.rota/tasks"

# Seed BACKLOG with GH + GL refs the post-filter has to probe upstream for.
cat > "$TMP_OO/.rota/BACKLOG.md" <<'EOF'
# TODO

## Features

- **[F01] [Major] Test feature.** Body. GH: #999999

## Bugs

- **[B01] [P1] Test bug.** GL: #999999

## Tasks

## Completed
EOF

# Without --open-only: both entries should appear (existing behavior preserved).
out_all=$(cd "$TMP_OO" && hvj issues imported) || \
  fail "issues imported (no flag) exited non-zero on --open-only fixture"
count_all=$(echo "$out_all" | jq '.data.entries | length')
[ "$count_all" = "2" ] || fail "issues imported (no flag) expected 2 entries on F77 fixture, got $count_all"

# With --open-only, every entry fails the state probe silently, so the result
# is []. Two ways to fail: no gh/glab on PATH at all, and a gh/glab that fails
# every call. NOGH links every tool from /usr/bin and /bin except gh, glab, herdr and tmux:
# a real "CLI missing" PATH that can't reach a real forge (the runner's poison
# stand-ins would count as present).
NOGH="$TMP_OO/nogh-bin"; mkdir -p "$NOGH"
for d in /usr/bin /bin; do
  for t in "$d"/*; do
    n="${t##*/}"; case "$n" in gh|glab|herdr|tmux) continue ;; esac
    [ -e "$NOGH/$n" ] || ln -s "$t" "$NOGH/$n"
  done
done
out_oo=$(cd "$TMP_OO" && env PATH="$NOGH" "$ROTA_BIN" --json issues imported --open-only) || \
  fail "issues imported --open-only exited non-zero with no gh/glab on PATH"
echo "$out_oo" | jq -e '.data.entries == []' >/dev/null || \
  fail "issues imported --open-only with no gh/glab expected no entries, got $out_oo"
out_oo=$(cd "$TMP_OO" && env -u FAKE_TRACKER_DB -u FAKE_TRACKER_LOG PATH="$TESTDIR/fakes:$PATH" "$ROTA_BIN" --json issues imported --open-only) || \
  fail "issues imported --open-only exited non-zero with a failing gh/glab"
echo "$out_oo" | jq -e '.data.entries == []' >/dev/null || \
  fail "issues imported --open-only with a failing gh/glab expected no entries, got $out_oo"

# --open-only is orthogonal to --for-repo: combining them parses fine and still
# emits an entries array (empty: no Repos:-tagged entry matches 'nonexistent').
out_combo=$(cd "$TMP_OO" && env PATH="$NOGH" "$ROTA_BIN" --json issues imported --for-repo nonexistent --open-only) || \
  fail "issues imported --for-repo nonexistent --open-only exited non-zero"
echo "$out_combo" | jq -e '.data.entries | type == "array"' >/dev/null || \
  fail "issues imported --for-repo … --open-only expected an entries array, got $out_combo"

# The old --repo spelling is the global flag now; imported has no repo scope.
rc=0; (cd "$TMP_OO" && "$ROTA_BIN" issues imported --repo nonexistent >/dev/null 2>&1) || rc=$?
[ "$rc" = "2" ] || fail "issues imported --repo should exit 2 (no repo scope), got $rc"

trap 'rm -rf "$TMP"' EXIT
pass "issues imported --open-only filters by upstream state, no-ops gracefully without gh/glab"

# === issues label / close ===
echo "Section 32: issues label and close against the fake forges"
TMP_IV="$(mktemp -d)"
trap 'rm -rf "$TMP_IV"; trap '"'"'rm -rf "$TMP"'"'"' EXIT' EXIT

for prov in github gitlab; do
  P="$TMP_IV/$prov"; mkdir -p "$P/.rota"
  echo "{\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0}}" > "$P/.rota/config.json"
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    # issues label/close detect the provider from origin, not from issues.provider
    git remote add origin "https://$prov.com/o/r.git"
    SHA=$(git rev-parse HEAD)
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    [ "$(command -v "$([ "$prov" = github ] && echo gh || echo glab)")" = "$TESTDIR/fakes/$([ "$prov" = github ] && echo gh || echo glab)" ] || fail "$prov: fake forge CLI not first on PATH"
    eq() { [ "$2" = "$3" ] || fail "$prov issues $1: expected [$2] got [$3]"; }
    rcof() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
    # The fake store is the forge's own state; no verb reads raw labels, state or comments.
    DBQ() { python3 -c '
import json, sys
n = int(sys.argv[2]); i = next(i for i in json.load(open(sys.argv[3]))["issues"] if i["number"] == n)
print({"labels": ",".join(sorted(i["labels"])), "state": i["state"].lower(),
       "comments": len(i["comments"])}[sys.argv[1]])' "$1" "$2" "$P/db.json"; }
    if [ "$prov" = github ]; then
      gh label create bug >/dev/null && gh issue create -t "first" -b "body one" -l bug >/dev/null && gh issue create -t "second" -b "body two" >/dev/null
    else
      glab issue create -t "first" -d "body one" -l bug >/dev/null && glab issue create -t "second" -d "body two" >/dev/null
    fi

    # label: add, remove, usage and a missing issue
    OUT=$(hvj issues label 2 --add triage) || fail "$prov issues label --add failed: $OUT"
    eq "label add data" "2 triage add true" "$(echo "$OUT" | jq -r '.data | "\(.issue) \(.label) \(.action) \(.changed)"')"
    eq "label add state" "triage" "$(DBQ labels 2)"
    OUT=$(hvj issues label 2 --remove triage) || fail "$prov issues label --remove failed: $OUT"
    eq "label remove data" "2 triage remove" "$(echo "$OUT" | jq -r '.data | "\(.issue) \(.label) \(.action)"')"
    eq "label remove state" "" "$(DBQ labels 2)"
    eq "label needs one of add/remove" 2 "$(rcof "$ROTA_BIN" --json issues label 2)"
    eq "label rejects add+remove" 2 "$(rcof "$ROTA_BIN" --json issues label 2 --add a --remove b)"
    eq "label missing issue" 3 "$(rcof "$ROTA_BIN" --json issues label 99 --add triage)"

    # close: closes once and comments with the short sha; a repeat posts no second comment
    OUT=$(hvj issues close 2 --commit "$SHA" --item B07) || fail "$prov issues close failed: $OUT"
    eq "close data" "2 $SHA true" "$(echo "$OUT" | jq -r '.data | "\(.issue) \(.commit) \(.changed)"')"
    eq "close state" closed "$(DBQ state 2)"
    eq "close comment" 1 "$(DBQ comments 2)"
    eq "other issue untouched" open "$(DBQ state 1)"
    hvj issues close 2 --commit "$SHA" >/dev/null || fail "$prov repeat close should exit 0"
    eq "repeat close adds no comment" 1 "$(DBQ comments 2)"
    eq "close unknown commit" 3 "$(rcof "$ROTA_BIN" --json issues close 1 --commit deadbeef0000)"
    eq "close missing issue" 3 "$(rcof "$ROTA_BIN" --json issues close 99 --commit "$SHA")"
    eq "close without --commit" 2 "$(rcof "$ROTA_BIN" --json issues close 1)"
    eq "close non-numeric issue" 2 "$(rcof "$ROTA_BIN" --json issues close abc --commit "$SHA")"
  ) || fail "issues label/close on $prov failed (see subshell output above)"
done

trap 'rm -rf "$TMP"' EXIT
rm -rf "$TMP_IV"
pass "issues label and close behave on github and gitlab (data, store state, exit codes)"

# === issues label/close exits: missing issue, no provider (#48) ===
echo "Section 32: issues label/close exit 3 for a missing issue and no provider"
TMP_EX="$(mktemp -d)"
trap 'rm -rf "$TMP_EX"; trap '"'"'rm -rf "$TMP"'"'"' EXIT' EXIT

mkexit() { # <dir> <origin url or ""> <config json>
  mkdir -p "$1/.rota"
  echo "$3" > "$1/.rota/config.json"
  git -C "$1" init -q
  git -C "$1" config user.email t@t
  git -C "$1" config user.name t
  git -C "$1" commit -q --allow-empty -m init
  if [ -n "$2" ]; then git -C "$1" remote add origin "$2"; fi
}
rcv() { local rc=0; (cd "$1" && shift && env PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$TMP_EX/db.json" "$ROTA_BIN" --json "$@") >"$TMP_EX/out" 2>&1 || rc=$?; echo "$rc"; }

# A missing issue is not_found (exit 3) on both forges, not a forge failure (5).
for prov in github gitlab; do
  P="$TMP_EX/$prov"
  mkexit "$P" "https://$prov.com/o/r.git" '{"issues":{"retryWaitSeconds":0}}'
  rc=$(rcv "$P" issues label 99 --remove bug)
  [ "$rc" = "3" ] || fail "$prov issues label on a missing issue should exit 3, got $rc: $(cat "$TMP_EX/out")"
  rc=$(rcv "$P" issues label 99 --add bug)
  [ "$rc" = "3" ] || fail "$prov issues label --add on a missing issue should exit 3, got $rc: $(cat "$TMP_EX/out")"
  rc=$(rcv "$P" issues close 99 --commit HEAD)
  [ "$rc" = "3" ] || fail "$prov issues close on a missing issue should exit 3, got $rc: $(cat "$TMP_EX/out")"
  # another forge failure stays exit 5
  : > "$TMP_EX/argv.log"
  rc=0
  (cd "$P" && env PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$TMP_EX/db.json" \
    FAKE_TRACKER_LOG="$TMP_EX/argv.log" FAKE_TRACKER_FAIL="issue" FAKE_TRACKER_FAIL_MSG="HTTP 500: server error" \
    "$ROTA_BIN" --json issues close 1 --commit HEAD) >"$TMP_EX/out" 2>&1 || rc=$?
  [ "$rc" = "5" ] || fail "$prov issues close with a failing forge should exit 5, got $rc: $(cat "$TMP_EX/out" "$TMP_EX/argv.log")"
done

# No origin and no issues.provider: exit 3 with a message, never a silent [].
P="$TMP_EX/none"
mkexit "$P" "" '{"issues":{"retryWaitSeconds":0}}'
for argv in "issues label 1 --add bug" "issues close 1 --commit HEAD"; do
  rc=$(rcv "$P" $argv)
  [ "$rc" = "3" ] || fail "no provider: rota $argv should exit 3, got $rc: $(cat "$TMP_EX/out")"
  grep -q "issues.provider" "$TMP_EX/out" || fail "no provider: rota $argv message should name issues.provider: $(cat "$TMP_EX/out")"
done

# issues.provider stands in when origin names no forge; an origin that does wins.
P="$TMP_EX/fallback"
mkexit "$P" "" '{"issues":{"provider":"github","retryWaitSeconds":0}}'
[ "$(cd "$P" && "$ROTA_BIN" --json issues provider | jget data.provider)" = "github" ] || fail "issues provider should answer from issues.provider with no origin"
git -C "$P" remote add origin "https://gitlab.com/o/r.git"
[ "$(cd "$P" && "$ROTA_BIN" --json issues provider | jget data.provider)" = "gitlab" ] || fail "an origin naming a forge should beat issues.provider"

trap 'rm -rf "$TMP"' EXIT
pass "issues label/close exit 3 for a missing issue and for no provider; issues.provider is the fallback"
