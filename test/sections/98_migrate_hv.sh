echo "migrate hv — hv to rota (#236): dry-run, apply, noop, refusals, resume, hard stop"

# Every project and home here is a mktemp fixture. HOME and CLAUDE_CONFIG_DIR
# point at the fixture home, so the real skills roots and settings.json are
# never seen. No network, no forge.
TMP_MHV="$(mktemp -d)"
trap 'rm -rf "$TMP_MHV"' EXIT

mhv_skills_root() { # mhv_skills_root <root> <agent>: an hv install with one edited file and a user skill
  mkdir -p "$1/hv-work" "$1/hv-plan" "$1/mine"
  printf 'work' > "$1/hv-work/body.txt"
  printf 'edited by the user' > "$1/hv-plan/body.txt"
  printf 'user skill' > "$1/mine/body.txt"
  python3 - "$1" "$2" <<'PY'
import hashlib, json, sys
root, agent = sys.argv[1], sys.argv[2]
h = lambda s: hashlib.sha256(s.encode()).hexdigest()
m = {"schema": 1, "version": "4.0.0", "digest": "d", "agent": agent,
     "files": {"hv-work/body.txt": h("work"), "hv-plan/body.txt": h("original")}}
open(root + "/.hv-manifest.json", "w").write(json.dumps(m))
PY
}

mhv_fixture() { # mhv_fixture <project> <home>: a committed hv-era project and a home with hv installs
  local d="$1" h="$2"
  mkdir -p "$d/.hv/features" "$d/.hv/handoff" "$h/.claude" "$h/.agents"
  ( cd "$d" && git init -q && git config user.email t@t && git config user.name t )
  printf '# ── hv ──\n.hv/qa-runs/\n.hv/status.json\n.hv/**/*.lock\n.worktrees/\n' > "$d/.gitignore"
  cat > "$d/AGENTS.md" <<'MD'
# Project
<!-- hv-knowledge-start -->
## Project Knowledge
old
<!-- hv-knowledge-end -->
<!-- hv:decisions:start -->
old
<!-- hv:decisions:end -->
<!-- hv-skills-start -->
## hv
old
<!-- hv-skills-end -->
My own note stays.
MD
  echo '{"hvSkills":{"version":"4.9.0"},"work":{"accounts":[]}}' > "$d/.hv/config.json"
  printf '# TODO\n- row Detail: `.hv/features/F1.md`\n' > "$d/.hv/BACKLOG.md"
  printf '<!-- hv:fields 1/2 -->\nbody\n' > "$d/.hv/features/F1.md"
  printf '<!-- hv-handoff: orchestrator -->\nx\n' > "$d/.hv/handoff/main.md"
  cat > "$h/.claude/settings.json" <<'JSON'
{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"hv hook stop # hv-hook"}]}],"SessionStart":[{"matcher":"^(startup|clear)$","hooks":[{"type":"command","command":"hv hook session-start # hv-hook"}]}]},"statusLine":{"type":"command","command":"hv statusline dump --then 'echo x'","hvWrapped":"echo x"}}
JSON
  mhv_skills_root "$h/.claude/skills" claude
  mhv_skills_root "$h/.agents/skills" codex
  ( cd "$d" && git add -A && git commit -q -m init && git branch hv-worker/a-1 )
}

mh() { # mh <project> <home> <args...>: the verb with the fixture home; prints the envelope
  local d="$1" h="$2"; shift 2
  HOME="$h" CLAUDE_CONFIG_DIR="$h/.claude" "$ROTA_BIN" --json -C "$d" "$@"
}
mhv_snapshot() { # mhv_snapshot <dir>...: path and content hash of every file, git internals left out
  find "$@" -path '*/.git' -prune -o -type f -print | sort | python3 -c '
import hashlib, sys
for p in sys.stdin.read().split("\n"):
    if p: print(p, hashlib.sha256(open(p, "rb").read()).hexdigest())'
}

MHV_D="$TMP_MHV/proj"; MHV_H="$TMP_MHV/home"
mkdir -p "$MHV_D" "$MHV_H"
mhv_fixture "$MHV_D" "$MHV_H"

echo "  hard stop: every verb but migrate hv and doctor says how to migrate"
RC=0; ERR="$(mh "$MHV_D" "$MHV_H" backlog list 2>&1 >/dev/null)" || RC=$?
[ $RC -eq 3 ] || fail "backlog list on an hv project should exit 3, got $RC"
case "$ERR" in *"run: rota migrate hv"*) ;; *) fail "hard stop lacks the hint: $ERR" ;; esac
RC=0; ERR="$(mh "$MHV_D" "$MHV_H" init 2>&1 >/dev/null)" || RC=$?
[ $RC -eq 3 ] || fail "init on an hv project should exit 3, got $RC"
[ ! -e "$MHV_D/.rota" ] || fail "the hard stop let init create .rota/"
RC=0; mh "$MHV_D" "$MHV_H" backlog list --help >/dev/null 2>&1 || RC=$?
[ $RC -eq 0 ] || fail "--help must not hit the hard stop, got $RC"

echo "  doctor fails on the leftover"
MHV_BIN="$TMP_MHV/bin"; mkdir -p "$MHV_BIN"; ln -s "$(command -v git)" "$MHV_BIN/git"; printf '#!/bin/sh\nexit 0\n' > "$MHV_BIN/jq"; chmod +x "$MHV_BIN/jq"
RC=0
OUT="$(HOME="$MHV_H" CLAUDE_CONFIG_DIR="$MHV_H/.claude" ROTA_TEST_DOCTOR_PATH="$MHV_BIN" "$ROTA_BIN" --json -C "$MHV_D" doctor 2>/dev/null)" || RC=$?
[ $RC -eq 1 ] || fail "doctor on an hv project should exit 1, got $RC: $OUT"
[ "$(jget data.checks[0].name <<<"$OUT")" = "state" ] || fail "doctor lacks the state check: $OUT"
case "$OUT" in *"run: rota migrate hv"*) ;; *) fail "doctor lacks the hint: $OUT" ;; esac

echo "  dry-run reports the plan and writes nothing"
BEFORE="$(mhv_snapshot "$MHV_D" "$MHV_H")"
RC=0; OUT="$(mh "$MHV_D" "$MHV_H" migrate hv --verbose 2>/dev/null)" || RC=$?
[ $RC -eq 0 ] || fail "dry-run should exit 0, got $RC: $OUT"
[ "$(jget data.applied <<<"$OUT")" = "false" ] || fail "dry-run applied: $OUT"
[ "$(jget data.projects[0].move <<<"$OUT")" = "true" ] || fail "dry-run plans no move: $OUT"
[ "$(jget data.skillRoots <<<"$OUT" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')" = 2 ] || fail "dry-run should list both skills roots: $OUT"
[ "$(jget data.workerBranches[0] <<<"$OUT")" = "hv-worker/a-1" ] || fail "worker branch not reported: $OUT"
AFTER="$(mhv_snapshot "$MHV_D" "$MHV_H")"
[ "$BEFORE" = "$AFTER" ] || fail "dry-run changed files"
[ -d "$MHV_D/.hv" ] && [ ! -e "$MHV_D/.rota" ] || fail "dry-run moved the state folder"

echo "  refuses a dirty tree"
echo x > "$MHV_D/stray.txt"
RC=0; OUT="$(mh "$MHV_D" "$MHV_H" migrate hv --apply 2>/dev/null)" || RC=$?
[ $RC -eq 4 ] || fail "dirty tree should exit 4, got $RC"
[ "$(jget data.blockedBy <<<"$OUT")" = "dirty-tree" ] || fail "dirty refusal data: $OUT"
[ -d "$MHV_D/.hv" ] || fail "a refusal moved .hv/"
rm "$MHV_D/stray.txt"

echo "  refuses while a lock is held"
MHV_REL="$TMP_MHV/release"; MHV_UP="$TMP_MHV/held"
python3 - "$MHV_D/.hv/status.json.lock" "$MHV_REL" "$MHV_UP" <<'PY' &
import fcntl, os, sys, time
f = open(sys.argv[1], "w"); fcntl.flock(f, fcntl.LOCK_EX)
open(sys.argv[3], "w").write("up")
for _ in range(300):
    if os.path.exists(sys.argv[2]): break
    time.sleep(0.1)
PY
MHV_PID=$!
for _ in $(seq 1 100); do [ -e "$MHV_UP" ] && break; sleep 0.1; done
[ -e "$MHV_UP" ] || fail "the lock holder did not start"
RC=0; OUT="$(mh "$MHV_D" "$MHV_H" migrate hv --apply 2>/dev/null)" || RC=$?
touch "$MHV_REL"; wait "$MHV_PID"
[ $RC -eq 4 ] || fail "a held lock should exit 4, got $RC"
[ "$(jget data.blockedBy <<<"$OUT")" = "lock-held" ] || fail "lock refusal data: $OUT"
[ -d "$MHV_D/.hv" ] || fail "a lock refusal moved .hv/"
rm -f "$MHV_D/.hv/status.json.lock"

echo "  refuses when .hv/ and .rota/ both exist"
mkdir "$MHV_D/.rota"
RC=0; OUT="$(mh "$MHV_D" "$MHV_H" migrate hv --apply 2>/dev/null)" || RC=$?
[ $RC -eq 4 ] || fail "both dirs should exit 4, got $RC"
[ "$(jget data.blockedBy <<<"$OUT")" = "both-dirs" ] || fail "both-dirs refusal data: $OUT"
rmdir "$MHV_D/.rota"

echo "  apply moves, rewrites, reinstalls and stamps"
RC=0; OUT="$(mh "$MHV_D" "$MHV_H" migrate hv --apply 2>/dev/null)" || RC=$?
[ $RC -eq 0 ] || fail "apply should exit 0, got $RC: $OUT"
[ ! -e "$MHV_D/.hv" ] && [ -d "$MHV_D/.rota" ] || fail "state folder not moved"
BK="$(jget data.projects[0].backup <<<"$OUT")"
[ -f "$MHV_D/$BK/.hv/BACKLOG.md" ] || fail "backup lacks the original BACKLOG.md ($BK)"
AG="$(cat "$MHV_D/AGENTS.md")"
for want in "<!-- rota-knowledge-start -->" "<!-- rota-decisions-start -->" "<!-- rota-skills-start -->" "## rota" ".rota/KNOWLEDGE.md"; do
  case "$AG" in *"$want"*) ;; *) fail "AGENTS.md lacks '$want': $AG" ;; esac
done
case "$AG" in *"hv-knowledge"*|*"hv:decisions"*|*"hv-skills-start"*) fail "an hv marker is left in AGENTS.md: $AG" ;; esac
case "$AG" in *"My own note stays."*) ;; *) fail "text outside the blocks was touched" ;; esac
[ "$(cat "$MHV_D/.gitignore")" = "$(printf '# ── rota ──\n.rota/qa-runs/\n.rota/status.json\n.rota/**/*.lock\n.worktrees/')" ] || fail ".gitignore not rewritten: $(cat "$MHV_D/.gitignore")"
grep -q '<!-- rota:fields 1/2 -->' "$MHV_D/.rota/features/F1.md" || fail "item marker not rewritten"
grep -q 'Detail: `.rota/features/F1.md`' "$MHV_D/.rota/BACKLOG.md" || fail "BACKLOG detail link not rewritten"
grep -q '^<!-- rota-handoff: orchestrator -->' "$MHV_D/.rota/handoff/main.md" || fail "handoff marker not rewritten"
[ "$(jget rota.version < "$MHV_D/.rota/config.json")" != "" ] || fail "rota.version not stamped"
grep -q hvSkills "$MHV_D/.rota/config.json" && fail "hvSkills key left in config.json"
for root in "$MHV_H/.claude/skills" "$MHV_H/.agents/skills"; do
  [ ! -e "$root/hv-work" ] && [ ! -e "$root/.hv-manifest.json" ] || fail "hv install left in $root"
  [ -f "$root/hv-plan/body.txt" ] || fail "the user-edited file was removed from $root"
  [ "$(cat "$root/mine/body.txt")" = "user skill" ] || fail "a user skill in $root did not survive"
  [ -d "$root/rota-work" ] && [ -f "$root/.rota-manifest.json" ] || fail "rota skills not installed in $root"
done
SET="$(cat "$MHV_H/.claude/settings.json")"
case "$SET" in *"rota hook stop # rota-hook"*"rota hook session-start # rota-hook"*"rota statusline dump --then"*"rotaWrapped"*) ;; *) fail "settings not rewritten: $SET" ;; esac
case "$SET" in *"hv "*|*"hv-hook"*|*hvWrapped*) fail "an hv entry is left in settings: $SET" ;; esac
# hv had no prompt hook, so the first install adds exactly that one (#81); the next is a noop.
RC=0; HOUT="$(mh "$MHV_D" "$MHV_H" hook install --scope user 2>/dev/null)" || RC=$?
[ $RC -eq 0 ] && [ "$(jget data.changed <<<"$HOUT")" = "true" ] || fail "rota hook install after the migration should add the prompt hook: $HOUT"
RC=0; HOUT="$(mh "$MHV_D" "$MHV_H" hook install --scope user 2>/dev/null)" || RC=$?
[ $RC -eq 0 ] && [ "$(jget data.changed <<<"$HOUT")" = "false" ] || fail "a second rota hook install is not a noop: $HOUT"

echo "  a second apply is a noop"
BEFORE="$(mhv_snapshot "$MHV_D" "$MHV_H")"
RC=0; OUT="$(mh "$MHV_D" "$MHV_H" migrate hv --apply 2>/dev/null)" || RC=$?
[ $RC -eq 0 ] || fail "second apply should exit 0, got $RC: $OUT"
[ "$(jget data.noop <<<"$OUT")" = "true" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "second apply was not a noop: $OUT"
[ "$BEFORE" = "$(mhv_snapshot "$MHV_D" "$MHV_H")" ] || fail "second apply changed files"
RC=0; mh "$MHV_D" "$MHV_H" backlog list >/dev/null 2>&1 || RC=$?
[ $RC -ne 3 ] || fail "a migrated project still hits the hard stop"

echo "  a run killed after the move resumes"
MHV_D2="$TMP_MHV/proj2"; MHV_H2="$TMP_MHV/home2"; mkdir -p "$MHV_D2" "$MHV_H2"
mhv_fixture "$MHV_D2" "$MHV_H2"
mv "$MHV_D2/.hv" "$MHV_D2/.rota"
RC=0; OUT="$(mh "$MHV_D2" "$MHV_H2" migrate hv --apply 2>/dev/null)" || RC=$?
[ $RC -eq 0 ] || fail "resume should exit 0, got $RC: $OUT"
[ "$(jget data.noop <<<"$OUT")" = "false" ] || fail "resume found nothing to do: $OUT"
grep -q '<!-- rota:fields 1/2 -->' "$MHV_D2/.rota/features/F1.md" || fail "resume did not rewrite the markers"
[ -d "$MHV_H2/.claude/skills/rota-work" ] || fail "resume did not reinstall the skills"
RC=0; OUT="$(mh "$MHV_D2" "$MHV_H2" migrate hv --apply 2>/dev/null)" || RC=$?
[ "$(jget data.noop <<<"$OUT")" = "true" ] || fail "second run after the resume was not a noop: $OUT"

echo "  --skip-skills leaves the skill installs alone"
MHV_D3="$TMP_MHV/proj3"; MHV_H3="$TMP_MHV/home3"; mkdir -p "$MHV_D3" "$MHV_H3"
mhv_fixture "$MHV_D3" "$MHV_H3"
BEFORE="$(mhv_snapshot "$MHV_H3/.claude/skills" "$MHV_H3/.agents/skills")"
RC=0; OUT="$(mh "$MHV_D3" "$MHV_H3" migrate hv --apply --skip-skills 2>/dev/null)" || RC=$?
[ $RC -eq 0 ] || fail "--skip-skills apply should exit 0, got $RC: $OUT"
[ -d "$MHV_D3/.rota" ] || fail "--skip-skills did not migrate the project"
[ "$BEFORE" = "$(mhv_snapshot "$MHV_H3/.claude/skills" "$MHV_H3/.agents/skills")" ] || fail "--skip-skills touched the skills roots"

rm -rf "$TMP_MHV"
trap 'rm -rf "$TMP"' EXIT
pass "migrate hv — hard stop, doctor, dry-run, refusals, apply, noop, resume, --skip-skills"
