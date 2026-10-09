echo "Acceptance ids: create numbers criteria, plan pass needs a PASS proof row (#395)"

TMP_AC="$(mktemp -d "$TMP/accept.XXXXXX")"
(
  P="$TMP_AC/proj"; mkdir -p "$P/.rota"
  echo '{"backlog":{"backend":"issues"},"issues":{"provider":"github","retryWaitSeconds":0}}' > "$P/.rota/config.json"
  cd "$P"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
  eq() { [ "$2" = "$3" ] || fail "$1: expected [$2] got [$3]"; }
  RC() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
  # BODY_OF <n>: the stored issue body
  BODY_OF() { python3 -c '
import json, sys
print(next(i for i in json.load(open(sys.argv[2]))["issues"] if i["number"] == int(sys.argv[1]))["body"])' "$1" "$P/db.json"; }

  printf 'Why.\n\n## Acceptance\n\n- [ ] it parses\n- [ ] it prints\n' > "$P/body.md"
  ID="$(hvj item create --kind features --title "Export" --tag Minor --body-file "$P/body.md" | jget data.id)" || vfail
  case "$(BODY_OF "$ID")" in *"- [ ] AC-1: it parses"*"- [ ] AC-2: it prints"*) ;; *) fail "create should number the criteria: $(BODY_OF "$ID")" ;; esac
  pass "item create numbers acceptance criteria"

  OUT="$(hvj item show "$ID")" || vfail
  eq "show ids" "AC-1 AC-2 false" "$(jget data.acceptance[0].id <<<"$OUT") $(jget data.acceptance[1].id <<<"$OUT") $(jget data.acceptance[1].met <<<"$OUT")"

  eq "no proof row refuses" "4" "$(RC hvj plan pass "#$ID" AC-1 --proof "abc1234:echo ok")"
  eq "unknown AC id" "2" "$(RC hvj plan pass "#$ID" AC-9 --proof "abc1234:echo ok")"
  eq "malformed --proof" "2" "$(RC hvj plan pass "#$ID" AC-1 --proof "abc1234")"
  eq "acceptance note kind is reserved" "2" "$(RC hvj item note add "$ID" --kind acceptance --body-file "$P/body.md")"
  pass "plan pass refuses without a proof row (4), unknown AC and bad input (2)"

  hvj proof add "$ID" --check "echo ok" --result PASS --evidence e --sha abc1234 > /dev/null
  OUT="$(hvj plan pass "#$ID" AC-2 --proof "abc1234:echo ok")" || fail "plan pass after a PASS row failed: $OUT"
  eq "pass data" "AC-2 true" "$(jget data.ac <<<"$OUT") $(jget data.changed <<<"$OUT")"
  OUT="$(hvj item show "$ID")" || vfail
  eq "met with proof" "true abc1234:echo ok" "$(jget data.acceptance[1].met <<<"$OUT") $(jget data.acceptance[1].proof <<<"$OUT")"
  case "$(hvj item note show "$ID" --kind acceptance | jget data.body)" in "- AC-2 · "*" · abc1234 · echo ok · it prints") ;; *) fail "acceptance note should hold the mark" ;; esac
  case "$(BODY_OF "$ID")" in *"[x]"*) fail "pass must not tick the body" ;; esac
  pass "plan pass marks a criterion met in the acceptance note"

  python3 - "$P/db.json" "$ID" <<'PY'
import json, sys
db = json.load(open(sys.argv[1]))
for i in db["issues"]:
    if i["number"] == int(sys.argv[2]):
        i["body"] = i["body"].replace("it prints", "it prints twice")
json.dump(db, open(sys.argv[1], "w"))
PY
  OUT="$(hvj item show "$ID")" || vfail
  eq "drift" "false changed" "$(jget data.acceptance[1].met <<<"$OUT") $(jget data.acceptance[1].flag <<<"$OUT")"
  pass "editing a met criterion is flagged on the next read"
)
