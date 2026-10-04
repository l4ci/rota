echo "Issue mode: release gate, notes from issues, milestone close-out"

TMP_RL="$(mktemp -d)"
trap 'rm -rf "$TMP_RL"' EXIT

for prov in github gitlab; do
  P="$TMP_RL/$prov"; mkdir -p "$P/.rota"
  echo "{\"backlog\":{\"backend\":\"issues\"},\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0}}" > "$P/.rota/config.json"
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    printf '# Project\n' > CLAUDE.md
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    eq() { [ "$2" = "$3" ] || fail "$prov $1: expected [$2] got [$3]"; }
    RC() { local rc=0; OUT="$("$@" 2>"$P/err")" || rc=$?; RCV=$rc; }
    # The fake tracker's own store: provider-neutral, no helper involved.
    ISSUE() { python3 -c '
import json, sys
db = json.load(open(sys.argv[1]))
i = next(i for i in db["issues"] if i["number"] == int(sys.argv[2]))
# comment bodies are folded to one line (the rota marker line sits under the text)
# glab has no close reason: a closed issue is completed unless it carries the not-planned label
reason = i["state_reason"] or (("not_planned" if "not-planned" in i["labels"] else "completed") if i["state"] == "closed" else None)
print("|".join([i["state"], str(reason), ",".join(sorted(i["labels"])), ";".join(" ".join(c["body"].split()) for c in i["comments"])]))' "$P/db.json" "$1"; }
    NATIVE() { python3 -c '
import json, sys
db = json.load(open(sys.argv[1]))
print(",".join(m["state"] for m in db["milestones"] if m["title"].startswith(sys.argv[2] + " ")))' "$P/db.json" "$1"; }
    WRITES() { grep -cE "issue (edit|close|reopen|comment)|label (create|add)|api -X|milestone (create|edit)" "$P/log" || true; }

    hvj milestone add --title "Launch" --summary "Ship it" >/dev/null   # #1 tracking issue
    hvj milestone status M01 --to active >/dev/null
    hvj item create --kind features --title "Feat" --milestone M01 >/dev/null   # F2
    hvj item create --kind bugs --title "Bug" --milestone M01 >/dev/null        # B3
    hvj item create --kind tasks --title "Task" --milestone M01 >/dev/null      # T4
    hvj item create --kind tasks --title "Dropped" --milestone M01 >/dev/null   # T5
    hvj item create --kind tasks --title "Outside" >/dev/null                   # T6 no milestone

    # --- gate: warnings only
    RC hvj release milestone-check M01
    eq "warning-only exit" "0" "$RCV"
    eq "warning-only clear" "true" "$(echo "$OUT" | jget data.clear)"
    eq "warning rows" '[{"number":2,"title":"Feat"},{"number":3,"title":"Bug"},{"number":4,"title":"Task"},{"number":5,"title":"Dropped"}]' "$(echo "$OUT" | jget data.stillOpen)"
    eq "warning-only no blocked" "[]" "$(echo "$OUT" | jget data.blocked)"
    # --- gate: each blocking label
    for pair in "in-progress:in-progress" "needs-review:needs-review" "changes-requested:changes-requested"; do
      st="${pair%%:*}"
      hvj item state F2 --to "$st" >/dev/null
      RC hvj release milestone-check M01
      eq "$st blocks" "1" "$RCV"
      eq "$st not clear" "false" "$(echo "$OUT" | jget data.clear)"
      eq "$st first blocked" "{\"number\":2,\"title\":\"Feat\",\"label\":\"$st\"}" "$(echo "$OUT" | jget data.blocked[0])"
    done
    hvj item state F2 --to none >/dev/null
    # --- clear
    for r in "F2 done" "B3 done" "T4 done" "T5 dropped"; do
      set -- $r; hvj item complete "$1" --reason "$2" --no-proof >/dev/null
    done
    RC hvj release milestone-check M01
    eq "clear exit" "0" "$RCV"; eq "clear data" "true" "$(echo "$OUT" | jget data.clear)"
    eq "clear nothing open" "[]" "$(echo "$OUT" | jget data.stillOpen)"
    RC hvj release milestone-check M99
    eq "unknown milestone" "3" "$RCV"
    RC hvj release milestone-check
    eq "gate usage" "2" "$RCV"

    # --- notes
    git commit -q --allow-empty -m "feat: tagged thing [F82]"
    git tag base
    git commit -q --allow-empty -m "fix: references issue #4"
    git commit -q --allow-empty -m "chore: untagged cleanup"
    git commit -q --allow-empty -m "docs: tagged [T07]"
    RC hvj release notes --from issues M01
    eq "notes exit" "0" "$RCV"
    eq "notes buckets" "### New

- Feat (#2)

### Fixed

- Bug (#3)

### Changed

- Task (#4)" "$(echo "$OUT" | jget data.markdown)"
    RC hvj release notes --from issues M01 --since base
    MD="$(echo "$OUT" | jget data.markdown)"
    eq "notes other" "### Other

- chore: untagged cleanup" "$(printf '%s\n' "$MD" | sed -n '/^### Other/,$p')"
    eq "notes since keeps buckets" "### New" "$(printf '%s\n' "$MD" | head -1)"
    RC hvj release notes --from issues M01 --since nope
    eq "notes bad ref" "3" "$RCV"
    RC hvj release notes --from issues M99
    eq "notes unknown milestone" "3" "$RCV"
    RC hvj release notes --from issues
    eq "notes issues needs milestone" "2" "$RCV"

    # --- close-out
    RC hvj release close-milestone M01 --release 1.2.0
    eq "close-out exit" "0" "$RCV"
    eq "close-out data" '{"milestone":"M01","release":"1.2.0","tag":"v1.2.0","issues":3,"changed":true}' "$(echo "$OUT" | jget data)"
    eq "feat closed completed" "closed|completed" "$(ISSUE 2 | cut -d"|" -f1,2)"
    for n in 2 3 4; do
      case "$(ISSUE $n)" in *released*"Released in v1.2.0"*"<!-- rota:released -->"*) ;; *) fail "$prov #$n not released: $(ISSUE $n)" ;; esac
    done
    case "$(ISSUE 5)" in *released*|*"Released in"*) fail "$prov dropped issue released" ;; esac
    case "$(ISSUE 6)" in *released*|*"Released in"*) fail "$prov outside issue released" ;; esac
    eq "native closed" "closed" "$(NATIVE M01)"
    eq "status shipped" "shipped" "$(hvj milestone list | jget data.milestones[0].status)"
    w="$(WRITES)"
    RC hvj release close-milestone M01 --release 1.2.0
    eq "second run exit" "0" "$RCV"
    eq "second run issues" "3" "$(echo "$OUT" | jget data.issues)"
    eq "second run no writes" "$w" "$(WRITES)"
    eq "one comment only" "1" "$(ISSUE 2 | awk -F'|' '{print $4}' | grep -o 'Released in' | wc -l)"
    RC hvj release close-milestone M01
    eq "close usage" "2" "$RCV"
    RC hvj release close-milestone M01 --release v1.2
    eq "close bad release" "2" "$RCV"
    RC hvj release close-milestone M99 --release 1.0.0
    eq "close unknown milestone" "3" "$RCV"
  )
done

# --- file mode refuses
P="$TMP_RL/file"; mkdir -p "$P/.rota/milestones"
(
  cd "$P"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  echo '{}' > .rota/counters.json
  # read-only verbs answer no (1), the mutating one refuses (4); both say why
  for h in "1 release milestone-check M01" "1 release notes --from issues M01" "4 release close-milestone M01 --release 1.0.0"; do
    set -- $h; want=$1; shift
    rc=0; out="$(hvj "$@" 2>/dev/null)" || rc=$?
    [ "$rc" = "$want" ] && [ "$(jget data.blockedBy <<<"$out")" = backend ] \
      || fail "file mode $*: expected exit $want with blockedBy backend, got $rc: $out"
  done
)

trap 'rm -rf "$TMP"' EXIT
pass "release: gate (blocked/warning/clear), notes by type, close-out and idempotent re-run (github, gitlab), file mode refuses"
