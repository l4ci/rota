echo "migrate issues: file backlog onto the tracker (github, gitlab)"

TMP_MI="$(mktemp -d)"
trap 'rm -rf "$TMP_MI"' EXIT

# MAKE <dir> <provider>: a file-backend project with an open backlog, artifacts and two milestones
MAKE_MI() {
  local P="$1" prov="$2"
  mkdir -p "$P/.rota/bugs" "$P/.rota/features" "$P/.rota/tasks" "$P/.rota/designs" "$P/.rota/plans" "$P/.rota/milestones"
  printf '{"issues":{"provider":"%s","retryWaitSeconds":0,"bulkPaceMs":0}}\n' "$prov" > "$P/.rota/config.json"
  cat > "$P/.rota/BACKLOG.md" <<'BL'
# TODO

## Bugs

- **[B1] [P1] Crash on save.** Saving a large file crashes. Detail: .rota/bugs/B1.md
- **[B2] [P2] Typo in help.** Fix the help text. Milestone: M01

## Features

- **[F1] [Major] Export data.** Export to CSV. Detail: .rota/features/F1.md Related: [F2], [F9], F80 Milestone: M07 Since: abc1234
- **[F2] [Minor] Load files.** Read files in. Repos: web Related: [F9]

## Tasks

- **[T1] Clean up scripts.** Remove dead scripts.

## Completed

- ~~**[F9] [Minor] Old thing.** done long ago~~ Done 2026-01-01 [`abc1234`]
BL
  printf 'Crash details.\n' > "$P/.rota/bugs/B1.md"
  printf 'Export details.\n\n## Proof\n\n- 2026-09-01 · smoke · PASS · abc1234 · all green\n' > "$P/.rota/features/F1.md"
  printf '# Design F1\n\nSee [F2] and F2 but not F22.\n' > "$P/.rota/designs/F1.md"
  printf '# Plan F1\n\nDepends on F2.\n' > "$P/.rota/plans/M07-F1.md"
  printf -- '---\nid: M07\ntitle: Tracker work\nstatus: active\ndepends: []\n---\n\n# M07 — Tracker work\n\n## Goal\n\nMove to the tracker. Needs [F1].\n' > "$P/.rota/milestones/M07.md"
  printf -- '---\nid: M01\ntitle: Old launch\nstatus: shipped\ndepends: []\n---\n\n# M01 — Old launch\n\n## Goal\n\nDone.\n' > "$P/.rota/milestones/M01.md"
  printf '# M07-S01\n\nSlice: [F1] first, then T1 of the plan.\n' > "$P/.rota/plans/M07-S01.md"
}

for prov in github gitlab; do
  P="$TMP_MI/$prov"; MAKE_MI "$P" "$prov"
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    eq() { [ "$2" = "$3" ] || fail "$prov migrate $1: expected [$2] got [$3]"; }
    has() { case "$3" in *"$2"*) ;; *) fail "$prov migrate $1: [$3] lacks [$2]";; esac; }
    RC() { local rc=0; OUT="$("$@" 2>"$P/err")" || rc=$?; RCV=$rc; ERR="$(cat "$P/err")"; }
    TREE() { find .rota -type f | sort | xargs cat | md5sum | cut -c1-32; }
    CALLS() { if [ -f "$P/log" ]; then wc -l < "$P/log" | tr -d ' '; else echo 0; fi; }
    # The tracker state is read from the fake's store (FAKE_TRACKER_DB), the same file for both providers.
    # DUMP: one line per issue: number|title|sorted labels|native milestone|state
    DUMP() { python3 -c '
import json, os
for i in sorted(json.load(open(os.environ["FAKE_TRACKER_DB"]))["issues"], key=lambda i: i["number"]):
    ms = i["milestone"]
    ms = ms[0] if isinstance(ms, list) else ms
    print("|".join([str(i["number"]), i["title"], ",".join(sorted(i["labels"])), str(ms or ""), i["state"]]))'; }
    # NOTES n -> comments of issue n joined by ~~
    NOTES() { python3 -c '
import json, os, sys
db = json.load(open(os.environ["FAKE_TRACKER_DB"]))
print("~~".join(c["body"] for i in db["issues"] if i["number"] == int(sys.argv[1]) for c in i["comments"]))' "$1"; }
    BODY() { python3 -c '
import json, os, sys
db = json.load(open(os.environ["FAKE_TRACKER_DB"]))
print(next(i["body"] for i in db["issues"] if i["number"] == int(sys.argv[1])))' "$1"; }
    # OPS: the preview/apply operations of the envelope in $OUT, one "action text" line each
    OPS() { echo "$OUT" | python3 -c '
import json, sys
for o in json.load(sys.stdin)["data"]["operations"]:
    print(o["action"], o["text"])'; }
    MAPQ() { python3 -c '
import json, sys
m = json.load(open(".rota/issue-map.json"))
print(eval(sys.argv[1]))' "$1"; }

    # --- usage and refusals
    RC hvj migrate issues --bogus
    eq "bad flag" "2" "$RCV"
    RC hvj migrate issues --limit x
    eq "bad limit" "2" "$RCV"
    mkdir -p "$TMP_MI/empty-$prov/.rota"; printf '{}\n' > "$TMP_MI/empty-$prov/.rota/config.json"
    RC hvj -C "$TMP_MI/empty-$prov" migrate issues --apply
    eq "missing backlog exit" "3" "$RCV"
    cp -r "$P" "$TMP_MI/umb-$prov"
    printf '{"repos":[{"name":"a","path":"a"}]}\n' > "$TMP_MI/umb-$prov/.rota/repos.json"; mkdir -p "$TMP_MI/umb-$prov/a"
    RC hvj -C "$TMP_MI/umb-$prov" migrate issues --apply
    eq "umbrella exit" "4" "$RCV"
    eq "umbrella changed" "false" "$(echo "$OUT" | jget data.changed)"

    # --- preview: default mode, lists the plan, touches nothing
    BEFORE="$(TREE)"
    RC hvj migrate issues
    eq "dry exit" "0" "$RCV"
    eq "dry applied" "false" "$(echo "$OUT" | jget data.applied)"
    eq "dry changed" "false" "$(echo "$OUT" | jget data.changed)"
    has "dry preview warning" "preview only; pass --apply" "$(echo "$OUT" | jget warnings)"
    DRY_OPS="$(OPS)"
    has "dry milestone" "create-milestone M07 (active)" "$DRY_OPS"
    has "dry issue" "create-issue F1 " "$DRY_OPS"
    has "dry issue title" "Export data" "$DRY_OPS"
    has "dry bug" "create-issue B1 " "$DRY_OPS"
    has "dry design" "note design on F1" "$DRY_OPS"
    has "dry proof" "note proof on F1" "$DRY_OPS"
    has "dry plan" "note plan on F1" "$DRY_OPS"
    has "dry slice" "note plan:S01 on M07" "$DRY_OPS"
    has "dry related" "rewrite Related on F1" "$DRY_OPS"
    eq "dry map has the planned items" "True" "$(echo "$OUT" | python3 -c '
import json, sys
m = json.load(sys.stdin)["data"]["map"]
print(all(k in m for k in ("M07", "B1", "B2", "F1", "F2", "T1")))')"
    case "$DRY_OPS" in *"issue F9"*|*"create-milestone M01"*) fail "$prov migrate dry: completed item / shipped milestone planned";; esac
    eq "dry map has no completed item" "False" "$(echo "$OUT" | python3 -c '
import json, sys
m = json.load(sys.stdin)["data"]["map"]
print("F9" in m or "M01" in m)')"
    has "dry dropped milestone warning" "M01" "$(echo "$OUT" | jget warnings)"
    eq "dry makes no tracker call" "0" "$(CALLS)"
    eq "dry leaves the tree unchanged" "$BEFORE" "$(TREE)"
    [ ! -e .rota/issue-map.json ] || fail "$prov migrate dry wrote the map"

    # --- apply
    RC hvj migrate issues --apply
    eq "apply exit" "0" "$RCV"
    eq "apply applied" "true" "$(echo "$OUT" | jget data.applied)"
    eq "apply changed" "true" "$(echo "$OUT" | jget data.changed)"
    eq "apply migrated" "5" "$(echo "$OUT" | jget data.migrated)"
    eq "apply total" "5" "$(echo "$OUT" | jget data.total)"
    eq "issue list" "1|M07 — Tracker work|milestone-tracker,status:active|M07 — Tracker work|open
2|Crash on save|p1,type:bug||open
3|Typo in help|p2,type:bug||open
4|Export data|size:Major,type:feature|M07 — Tracker work|open
5|Load files|size:Minor,type:feature||open
6|Clean up scripts|type:task||open" "$(DUMP)"
    eq "map ids" "M07:1 B1:B2 B2:B3 F1:F4 F2:F5 T1:T6" \
       "$(MAPQ '" ".join(k + ":" + str(v["id"] if k != "M07" else v["number"]) for k, v in m.items())')"
    eq "map data matches the file" "True" "$(echo "$OUT" | python3 -c '
import json, sys
print(json.load(sys.stdin)["data"]["map"] == json.load(open(".rota/issue-map.json")))')"
    eq "map has no completed item" "False" "$(MAPQ '"F9" in m or "M01" in m')"
    eq "map entry url" "True" "$(MAPQ 'm["F1"]["url"].endswith("/4") and m["F1"]["number"] == 4')"
    has "F1 body detail" "Export details." "$(BODY 4)"
    case "$(BODY 4)" in *"## Proof"*) fail "$prov migrate: proof section left in the body";; esac
    has "F1 fields block" "Related: [F5]" "$(BODY 4)"
    has "F2 repos field" "Repos: web" "$(BODY 5)"
    has "Since kept in fields block" "Since: abc1234" "$(BODY 4)"
    has "F1 dangling Related dropped from field" "Related: [F5]" "$(BODY 4)"
    case "$(BODY 4)" in *"Related: [F5], "*|*"[F9]"*|*"Related: [F9]"*) fail "$prov migrate: dangling Related left in F1 field: $(BODY 4)";; esac
    has "F1 unmapped listed in body" "Related before migration (not migrated): F9, F80" "$(BODY 4)"
    has "F2 unmapped listed in body" "Related before migration (not migrated): F9" "$(BODY 5)"
    case "$(BODY 5)" in *"Related:"*) fail "$prov migrate: F2 kept an all-dangling Related field: $(BODY 5)";; esac
    has "B1 body" "Crash details." "$(BODY 2)"
    N4="$(NOTES 4)"
    has "proof note" "<!-- rota:proof -->" "$N4"; has "proof rows" "smoke · PASS" "$N4"
    has "design note rewritten" "See [F5] and F5 but not F22." "$N4"
    has "plan note rewritten" "Depends on F5." "$N4"
    N1="$(NOTES 1)"
    has "slice note" "<!-- rota:plan:S01 -->" "$N1"
    has "slice bracket rewrite only" "Slice: [F4] first, then T1 of the plan." "$N1"
    has "milestone body rewritten" "Needs [F4]." "$(BODY 1)"
    has "milestone status kept" "status: active" "$(BODY 1)"
    eq "banner first line" "true" "$(grep -q '^> Frozen: this backlog moved to the issue tracker on ' <<<"$(head -1 .rota/BACKLOG.md)" && echo true)"
    eq "banner once" "1" "$(grep -c '^> Frozen:' .rota/BACKLOG.md)"
    grep -q 'Old thing' .rota/BACKLOG.md || fail "$prov migrate: completed item lost"
    case "$(cat .rota/config.json)" in *backend*) fail "$prov migrate: backend flipped";; esac

    # --- re-run is a no-op
    TREE_DONE="$(TREE)"; C0="$(CALLS)"
    RC hvj migrate issues --apply
    eq "rerun exit" "0" "$RCV"
    eq "rerun changed" "false" "$(echo "$OUT" | jget data.changed)"
    eq "rerun makes no tracker call" "$C0" "$(CALLS)"
    eq "rerun leaves the tree unchanged" "$TREE_DONE" "$(TREE)"
    eq "rerun banner once" "1" "$(grep -c '^> Frozen:' .rota/BACKLOG.md)"
    has "rerun skips" "skip F1 " "$(OPS)"
  ) 2>"$TMP_MI/sub-$prov.err" || { cat "$TMP_MI/sub-$prov.err" >&2; fail "$prov migrate main flow failed"; }

  # --- rate limit mid-run, then resume without duplicates
  R="$TMP_MI/rl-$prov"; MAKE_MI "$R" "$prov"
  (
    cd "$R"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$R/db.json" FAKE_TRACKER_LOG="$R/log"
    eq() { [ "$2" = "$3" ] || fail "$prov migrate rate-limit $1: expected [$2] got [$3]"; }
    has() { case "$3" in *"$2"*) ;; *) fail "$prov migrate rate-limit $1: [$3] lacks [$2]";; esac; }
    RC() { local rc=0; OUT="$("$@" 2>"$R/err")" || rc=$?; RCV=$rc; ERR="$(cat "$R/err")"; }
    COUNT() { python3 -c '
import json, os
print(len(json.load(open(os.environ["FAKE_TRACKER_DB"]))["issues"]))'; }
    BODY() { python3 -c '
import json, os, sys
db = json.load(open(os.environ["FAKE_TRACKER_DB"]))
print(next(i["body"] for i in db["issues"] if i["number"] == int(sys.argv[1])))' "$1"; }

    FAKE_TRACKER_FAIL="Load files" FAKE_TRACKER_FAIL_MSG="secondary rate limit" RC hvj migrate issues --apply
    eq "stop exit" "6" "$RCV"
    has "stop report" "3 of 5 migrated" "$(echo "$OUT" | jget error.message)"
    eq "map saved" "M07 B1 B2 F1" "$(python3 -c 'import json;print(" ".join(json.load(open(".rota/issue-map.json"))))')"
    eq "created so far" "4" "$(COUNT)"
    [ "$(grep -c '^> Frozen:' .rota/BACKLOG.md || true)" = "0" ] || fail "$prov migrate rate-limit: froze an incomplete migration"
    RC hvj migrate issues --apply
    eq "resume exit" "0" "$RCV"
    eq "resume migrated" "5" "$(echo "$OUT" | jget data.migrated)"
    eq "no duplicates" "6" "$(COUNT)"
    eq "resume map" "M07 B1 B2 F1 F2 T1" "$(python3 -c 'import json;print(" ".join(json.load(open(".rota/issue-map.json"))))')"
    has "resume Related rewritten" "Related: [F5]" "$(BODY 4)"
    eq "resume banner" "1" "$(grep -c '^> Frozen:' .rota/BACKLOG.md)"
  ) 2>"$TMP_MI/rl-$prov.err" || { cat "$TMP_MI/rl-$prov.err" >&2; fail "$prov migrate rate-limit flow failed"; }

  # --- a plain tracker failure stops with exit 5 and keeps the map
  E="$TMP_MI/er-$prov"; MAKE_MI "$E" "$prov"
  (
    cd "$E"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$E/db.json" FAKE_TRACKER_LOG="$E/log"
    rc=0; FAKE_TRACKER_FAIL="Load files" hvj migrate issues --apply >/dev/null 2>&1 || rc=$?
    [ "$rc" = "5" ] || fail "$prov migrate tracker failure: expected exit 5 got $rc"
    [ -f .rota/issue-map.json ] || fail "$prov migrate tracker failure: map not saved"
  ) || fail "$prov migrate tracker failure flow failed"

  # --- --limit: create N items per run; notes wait until everything exists
  L="$TMP_MI/lim-$prov"; MAKE_MI "$L" "$prov"
  (
    cd "$L"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$L/db.json" FAKE_TRACKER_LOG="$L/log"
    eq() { [ "$2" = "$3" ] || fail "$prov migrate limit $1: expected [$2] got [$3]"; }
    has() { case "$3" in *"$2"*) ;; *) fail "$prov migrate limit $1: [$3] lacks [$2]";; esac; }
    KEYS() { python3 -c 'import json;print(" ".join(json.load(open(".rota/issue-map.json"))))'; }
    OUT="$(hvj migrate issues --apply --limit 2 2>/dev/null)"
    eq "limit keys" "M07 B1 B2" "$(KEYS)"
    eq "limit migrated" "2" "$(echo "$OUT" | jget data.migrated)"
    eq "limit total" "5" "$(echo "$OUT" | jget data.total)"
    [ "$(grep -c '^> Frozen:' .rota/BACKLOG.md || true)" = "0" ] || fail "$prov migrate limit: froze early"
    hvj migrate issues --apply --limit 2 >/dev/null 2>&1
    eq "limit second run" "M07 B1 B2 F1 F2" "$(KEYS)"
    hvj migrate issues --apply --limit 2 >/dev/null 2>&1
    eq "limit third run" "M07 B1 B2 F1 F2 T1" "$(KEYS)"
    eq "limit finished: banner" "1" "$(grep -c '^> Frozen:' .rota/BACKLOG.md)"
    has "limit finished: notes" "rota:design" "$(python3 -c '
import json, os
db = json.load(open(os.environ["FAKE_TRACKER_DB"]))
print(" ".join(c["body"] for i in db["issues"] if i["number"] == 4 for c in i["comments"]))')"
  ) 2>"$TMP_MI/lim-$prov.err" || { cat "$TMP_MI/lim-$prov.err" >&2; fail "$prov migrate limit flow failed"; }
done

trap 'rm -rf "$TMP"' EXIT
pass "migrate issues: preview, apply, map, rewrites, banner, no-op re-run, rate-limit resume, limit (github, gitlab)"
