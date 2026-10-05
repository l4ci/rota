echo ".worktrees/ — one gitignored worktree root; nothing walks into it"
# Covers #79. (a) rota init adds `.worktrees/` to .gitignore once;
# (b) rota worker pool init creates slots under .worktrees/ and leaves a slot that is
# registered at the old .claude/worktrees/rota-worker path where it is.
# The decoy and no-recursive-walk checks on validate-skills live in test/doclint.sh.

TMP_WR="$(mktemp -d)"
trap 'rm -rf "$TMP_WR"' EXIT

# ── (a) rota init ─────────────────────────────────────────────────────────────
mkdir -p "$TMP_WR/boot"
(
  cd "$TMP_WR/boot"
  git init -q -b main .
  "$ROTA_BIN" init >/dev/null 2>&1 || exit 1
  "$ROTA_BIN" init >/dev/null 2>&1 || exit 1
) || fail "rota init failed in a fresh repo"
[ "$(grep -cxF '.worktrees/' "$TMP_WR/boot/.gitignore")" = "1" ] \
  || fail "rota init must add .worktrees/ to .gitignore exactly once, got $(grep -cxF '.worktrees/' "$TMP_WR/boot/.gitignore")"
# An existing project that already has the whole rota block still gets the line,
# and the block is not repeated.
mkdir -p "$TMP_WR/boot2"
(
  cd "$TMP_WR/boot2"
  git init -q -b main .
  "$ROTA_BIN" init >/dev/null 2>&1 || exit 1
  grep -vxF '.worktrees/' .gitignore | grep -vxF '# Worker worktrees (rota worker pool, parallel rounds)' > .gitignore.new && mv .gitignore.new .gitignore
  "$ROTA_BIN" init >/dev/null 2>&1 || exit 1
) || fail "rota init re-run failed"
[ "$(grep -cxF '.worktrees/' "$TMP_WR/boot2/.gitignore")" = "1" ] || fail "an upgraded project must gain .worktrees/ once"
# Every spelling git treats as the same ignore must stop a duplicate append.
for SPELL in '.worktrees' '/.worktrees' '/.worktrees/' '.worktrees/'"$(printf '\r')"; do
  rm -rf "$TMP_WR/boot3"; mkdir -p "$TMP_WR/boot3"
  ( cd "$TMP_WR/boot3" && git init -q -b main . && printf '%s\n' "$SPELL" > .gitignore \
      && "$ROTA_BIN" init >/dev/null 2>&1 ) || fail "rota init failed with an existing '$SPELL' line"
  tr -d '\r' < "$TMP_WR/boot3/.gitignore" | grep -xE '/?\.worktrees/?' >/dev/null || fail "fixture lost its ignore line"
  [ "$(tr -d '\r' < "$TMP_WR/boot3/.gitignore" | grep -cE '^/?\.worktrees/?$')" = "1" ] \
    || fail "an existing '$SPELL' line must not be duplicated: $(cat "$TMP_WR/boot3/.gitignore")"
done
[ "$(grep -cxF '.rota/status.json' "$TMP_WR/boot2/.gitignore")" = "1" ] || fail "adding .worktrees/ must not repeat the rota block"
grep -qxF '.worktrees/' "$REPO/.gitignore" || fail "this repo's own .gitignore must list .worktrees/"
pass "rota init adds .worktrees/ to .gitignore once, in fresh and upgraded projects"

# ── (b) pool root + legacy slots ────────────────────────────────────────────
mkdir -p "$TMP_WR/repo/.rota"
(
  cd "$TMP_WR/repo"
  git init -q -b main .
  git config user.email t@t; git config user.name t
  printf '.worktrees/\n' > .gitignore
  echo seed > seed.txt; git add seed.txt .gitignore; git commit -q -m seed
) || fail "worktrees-root fixture setup failed"
slot_wt() { hvj -C "$TMP_WR/repo" worker pool list | jget 'data.slots[0].worktree'; }
pool_init() { hvj -C "$TMP_WR/repo" worker pool init --slots 1 --base main 2>/dev/null; }
RC=0; pool_init >/dev/null || RC=$?
[ "$RC" = "0" ] || fail "pool init failed, rc $RC"
[ "$(slot_wt)" = "$(cd "$TMP_WR/repo" && pwd -P)/.worktrees/w1" ] || fail "new slot must live in <root>/.worktrees/w1, got $(slot_wt)"
[ -z "$(git -C "$TMP_WR/repo" status --porcelain -- . ':!.rota')" ] || fail "an ignored .worktrees/ must leave the project clean"
RC=0; OUT="$(pool_init)" || RC=$?
[ "$RC" = "0" ] || fail "re-init failed, rc $RC"
if jget warnings <<<"$OUT" >/dev/null; then fail "no gitignore warning expected when .worktrees/ is ignored: $OUT"; fi

# A slot registered at the legacy root keeps its path; init neither errors nor duplicates.
( cd "$TMP_WR/repo" && git worktree remove --force .worktrees/w1 && git branch -D rota-worker/w1 >/dev/null \
    && mkdir -p .claude/worktrees/rota-worker && git worktree add -q -b rota-worker/w1 .claude/worktrees/rota-worker/w1 main )
python3 - "$TMP_WR/repo/.rota/workers.json" "$TMP_WR/repo/.claude/worktrees/rota-worker/w1" <<'PY'
import json, os, sys
p, wt = sys.argv[1:3]; d = json.load(open(p))
d["slots"][0]["worktree"] = os.path.realpath(wt); json.dump(d, open(p, "w"))
PY
RC=0; OUT="$(pool_init)" || RC=$?
[ "$RC" = "0" ] || fail "init must accept a legacy-path slot, rc $RC: $OUT"
case "$(slot_wt)" in */.claude/worktrees/rota-worker/w1) ;; *) fail "a healthy legacy slot must keep its registered path, got $(slot_wt)" ;; esac
[ "$(jget 'data.slots[0].worktree' <<<"$OUT")" = "$(slot_wt)" ] || fail "init data must report the legacy path: $OUT"
[ ! -e "$TMP_WR/repo/.worktrees/w1" ] || fail "init must not create a second worktree for a legacy slot"
jget 'warnings[0]' <<<"$OUT" >/dev/null || fail "init should warn how to relocate a legacy slot, got: $OUT"
# A registry path that is another repository's worktree is refused, not adopted.
mkdir -p "$TMP_WR/other"
( cd "$TMP_WR/other" && git init -q -b main . && git config user.email t@t && git config user.name t && echo o > o && git add o && git commit -q -m o )
cp "$TMP_WR/repo/.rota/workers.json" "$TMP_WR/workers.keep"
python3 - "$TMP_WR/repo/.rota/workers.json" "$TMP_WR/other" <<'PY'
import json, os, sys
p, wt = sys.argv[1:3]; d = json.load(open(p))
d["slots"][0]["worktree"] = os.path.realpath(wt); json.dump(d, open(p, "w"))
PY
RC=0; OUT="$(pool_init)" || RC=$?
[ "$RC" = "3" ] || fail "a slot registered at another repo's checkout must exit 3, got $RC: $OUT"
[ "$(jget ok <<<"$OUT")" = "false" ] || fail "refusal must be an error envelope, got: $OUT"
cp "$TMP_WR/workers.keep" "$TMP_WR/repo/.rota/workers.json"
# A legacy slot whose worktree is gone is rebuilt under the new root.
( cd "$TMP_WR/repo" && git worktree remove --force .claude/worktrees/rota-worker/w1 )
RC=0; pool_init >/dev/null || RC=$?
[ "$RC" = "0" ] || fail "init must rebuild a missing legacy slot, rc $RC"
[ "$(slot_wt)" = "$(cd "$TMP_WR/repo" && pwd -P)/.worktrees/w1" ] || fail "a rebuilt slot belongs under .worktrees/, got $(slot_wt)"
# Unignored root: warn, don't edit .gitignore.
: > "$TMP_WR/repo/.gitignore"; git -C "$TMP_WR/repo" commit -qam "drop ignore"
RC=0; OUT="$(pool_init)" || RC=$?
[ "$RC" = "0" ] || fail "init failed on an unignored root, rc $RC"
jget 'warnings[0]' <<<"$OUT" >/dev/null || fail "init must warn when .worktrees/ is not ignored, got: $OUT"
[ ! -s "$TMP_WR/repo/.gitignore" ] || fail "init must not edit .gitignore itself"
pass "worker pool init: slots go under .worktrees/; legacy slots keep their path; unignored root warns"

trap 'rm -rf "$TMP"' EXIT
pass "worktrees-root contract"
