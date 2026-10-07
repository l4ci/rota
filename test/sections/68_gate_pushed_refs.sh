echo "worker gate — pushed refs, PR identity, merge confirmed on base (github + gitlab)"
# The gate merges what was PUSHED, so it must judge origin/<base> vs origin/<branch>
# after a fetch, refuse a PR that is not the verified branch, and confirm the merge
# commit really sits on origin/<base>. Each case builds a fresh bare origin plus a
# gate checkout; a fake gh/glab (one python script) plays the forge and can lie in the
# ways a real one does: report success while merging nothing (glab auto-merge), or
# merge into another branch (a stacked PR).

TMP_GT="$(mktemp -d)"
trap 'rm -rf "$TMP_GT"' EXIT

GT_BIN="$TMP_GT/fakebin"; mkdir -p "$GT_BIN"
REAL_GIT="$(command -v git)"
cat > "$GT_BIN/forge.py" <<'PYEOF'
import json, os, subprocess, sys, tempfile

tool, args = os.environ["FORGE_TOOL"], sys.argv[1:]
dbp, mode = os.environ["FORGE_DB"], os.environ.get("FORGE_MODE", "ok")
db = json.load(open(dbp))
with open(os.environ["FORGE_LOG"], "a") as f:
    f.write(tool + " " + " ".join(args) + "\n")

def save(): json.dump(db, open(dbp, "w"))

def merge():
    # A real forge refuses a merge whose pin is missing or is not the PR's head.
    flag = "--match-head-commit" if tool == "gh" else "--sha"
    pin = args[args.index(flag) + 1] if flag in args else None
    if mode == "race":
        db["sha"] = "f" * 40  # someone pushed after the gate's check
    if pin != db["sha"]:
        print("simulated: head commit %s does not match the pin %s" % (db["sha"], pin), file=sys.stderr); sys.exit(1)
    if mode == "fail":
        print("simulated: merge blocked by branch protection", file=sys.stderr); sys.exit(1)
    if mode == "noop":
        return
    target = "stack" if mode == "elsewhere" else db["base"]
    with tempfile.TemporaryDirectory() as t:
        def g(*a): return subprocess.run(["git", "-c", "user.name=f", "-c", "user.email=f@f", *a],
                                         cwd=t, check=True, capture_output=True, text=True).stdout.strip()
        subprocess.run(["git", "clone", "-q", db["origin"], t], check=True)
        if mode == "elsewhere": g("checkout", "-q", "-b", "stack", "origin/" + db["base"])
        if mode == "ff":  # fast-forward method: no merge commit on the MR
            g("merge", "--ff-only", "-q", "origin/" + db["head"])
        else:
            g("merge", "--no-ff", "-q", "-m", "merge pr", "origin/" + db["head"])
            db["merge"] = g("rev-parse", "HEAD")
        g("push", "-q", "origin", target)
    db["state"] = "MERGED"; save()

if tool == "gh" and args[:2] == ["pr", "view"]:
    if "body" in args: print(db["body"])
    else: print(json.dumps({"headRefName": db["head"], "headRefOid": db["sha"], "baseRefName": db["base"],
                            "state": db["state"], "mergeCommit": {"oid": db["merge"]} if db["merge"] else None}))
elif tool == "gh" and args[:2] == ["pr", "merge"]:
    merge()
elif tool == "glab" and args[:1] == ["api"]:
    squash = mode == "squash"
    print(json.dumps({"source_branch": db["head"], "sha": db["sha"], "target_branch": db["base"],
                      "state": {"OPEN": "opened", "MERGED": "merged"}.get(db["state"], "closed"),
                      "description": db["body"],
                      "merge_commit_sha": None if squash or not db["merge"] else db["merge"],
                      "squash_commit_sha": db["merge"] if squash and db["merge"] else None}))
elif tool == "glab" and args[:2] == ["mr", "merge"]:
    merge()
else:
    print("fake forge: unsupported: %s" % args, file=sys.stderr); sys.exit(2)
PYEOF
for t in gh glab; do printf '#!/usr/bin/env bash\nFORGE_TOOL=%s exec python3 "%s/forge.py" "$@"\n' "$t" "$GT_BIN" > "$GT_BIN/$t"; chmod +x "$GT_BIN/$t"; done
# broken-check shim: a git whose merge-base always dies with 128
mkdir -p "$TMP_GT/brokengit"
printf '#!/usr/bin/env bash\n[ "$1" = merge-base ] && exit 128\nexec "%s" "$@"\n' "$REAL_GIT" > "$TMP_GT/brokengit/git"
chmod +x "$TMP_GT/brokengit/git"

gt_git() { git -c user.name=t -c user.email=t@t "$@"; }

# gt_case <name> <provider-url> — fresh origin + gate checkout (main) + pushed worker branch w1.
# Sets GT_DIR (gate checkout), FORGE_DB, FORGE_LOG.
gt_case() {
  GT_DIR="$TMP_GT/$1"; local origin="$GT_DIR.origin.git" worker="$GT_DIR.worker"
  git init -q --bare -b main "$origin"
  git clone -q "$origin" "$GT_DIR" 2>/dev/null
  ( cd "$GT_DIR" && git checkout -q -b main 2>/dev/null; echo seed > seed.txt; git add seed.txt
    gt_git commit -q -m seed && git push -q origin main ) || fail "gate case $1: seed failed"
  git clone -q "$origin" "$worker" 2>/dev/null
  ( cd "$worker" && git checkout -q -b w1 && echo work > work.txt && git add work.txt \
    && gt_git commit -q -m work && git push -q origin w1 ) || fail "gate case $1: worker push failed"
  mkdir -p "$GT_DIR/.rota"
  printf '{"test":{"full":["true"]}}' > "$GT_DIR/.rota/config.json"
  printf '{"slots":[{"name":"w1","branch":"w1","pr":"%s"}]}' "$2" > "$GT_DIR/.rota/workers.json"
  FORGE_DB="$GT_DIR.forge.json"; FORGE_LOG="$GT_DIR.forge.log"; : > "$FORGE_LOG"
  python3 - "$FORGE_DB" "$origin" "$(git -C "$worker" rev-parse HEAD)" <<'PYEOF'
import json, sys
json.dump({"origin": sys.argv[2], "head": "w1", "sha": sys.argv[3], "base": "main",
           "state": "OPEN", "merge": "", "body": ""}, open(sys.argv[1], "w"))
PYEOF
  GT_ORIGIN="$origin"; GT_WORKER="$worker"
}
# gt_gate [env...] -- <args> : run `rota --json worker gate <args>` in $GT_DIR, envelope to
# $GT_DIR.out, stderr to $GT_DIR.err, echo rc
gt_gate() {
  local rc=0
  ( cd "$GT_DIR" && env PATH="$GT_BIN:$PATH" FORGE_DB="$FORGE_DB" FORGE_LOG="$FORGE_LOG" ROTA_GATE_SHA_WAIT=0 \
      "$@" ) >"$GT_DIR.out" 2>"$GT_DIR.err" || rc=$?
  echo "$rc"
}
# gt_verdict : data.verdict of the last gate run
gt_verdict() { jget data.verdict <"$GT_DIR.out"; }
GH_URL="https://github.com/o/r/pull/7"
GL_URL="https://gitlab.com/o/r/-/merge_requests/7"
set_forge() { python3 - "$FORGE_DB" "$1" "$2" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1])); d[sys.argv[2]] = sys.argv[3]; json.dump(d, open(sys.argv[1], "w"))
PYEOF
}

# (a) freshness is judged on PUSHED refs: the worker's local branch has merged main,
# but what it pushed has not, so the PR is stale. Local refs would say FRESH. Main
# moves on work.txt, which the worker added too, so the merge conflicts and the
# gate bounces it (a clean, disjoint merge is merged by the gate, see section 104).
gt_case a "$GH_URL"
( cd "$GT_WORKER" && git checkout -q main && echo more > work.txt && git add work.txt \
  && gt_git commit -q -m "main moves" && git push -q origin main ) || fail "gate (a): advancing main failed"
( cd "$GT_DIR" && git fetch -q origin && git checkout -q -b w1 origin/w1 && gt_git merge -q -X ours origin/main -m sync && git checkout -q main ) \
  || fail "gate (a): local w1 should merge origin/main cleanly"
( cd "$GT_DIR" && git merge-base --is-ancestor origin/main w1 ) || fail "gate (a): fixture is vacuous, local w1 is not fresh"
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main --check-only)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = stale ] || fail "gate (a): pushed-stale branch must be verdict stale, exit 1, got $RC: $(cat "$GT_DIR.out")"
[ "$(jget data.changed <"$GT_DIR.out")" = false ] || fail "gate (a): a stale check must not report changed: $(cat "$GT_DIR.out")"
pass "freshness is judged on origin/* after a fetch, not on local refs"

# (c) the PR must be the verified branch: head SHA, head branch, target branch, state
gt_case c "$GH_URL"
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main --check-only)"
[ "$RC" = 0 ] && [ "$(gt_verdict)" = fresh ] || fail "gate (c): matching PR must be fresh exit 0, got $RC: $(cat "$GT_DIR.out")"
set_forge sha 0000000000000000000000000000000000000000
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main --check-only)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = pr-mismatch ] || fail "gate (c): head SHA mismatch must be pr-mismatch (rc=$RC): $(cat "$GT_DIR.out")"
set_forge sha "$(git -C "$GT_WORKER" rev-parse HEAD)"
set_forge base stack
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main --check-only)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = pr-mismatch ] || fail "gate (c): stacked PR (other base) must be pr-mismatch (rc=$RC): $(cat "$GT_DIR.out")"
set_forge base main; set_forge head other
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main --check-only)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = pr-mismatch ] || fail "gate (c): wrong head branch must be pr-mismatch (rc=$RC): $(cat "$GT_DIR.out")"
set_forge head w1; set_forge state MERGED
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main --check-only)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = pr-mismatch ] || fail "gate (c): a non-open PR must be pr-mismatch (rc=$RC): $(cat "$GT_DIR.out")"
pass "PR head SHA, head branch, base and state are checked against the verified branch"

# (d) github merge: pinned to the verified SHA, confirmed on origin/main, then re-verified
gt_case d "$GH_URL"
printf '{"test":{"full":["test -f work.txt"]}}' > "$GT_DIR/.rota/config.json"
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 0 ] && [ "$(gt_verdict)" = pass ] || fail "gate (d): clean github merge must pass (rc=$RC): $(cat "$GT_DIR.out")"
[ "$(jget data.changed <"$GT_DIR.out")" = true ] && [ "$(jget 'data.verified[0]' <"$GT_DIR.out")" = "test -f work.txt" ] \
  || fail "gate (d): a pass must report changed and the verified command: $(cat "$GT_DIR.out")"
grep -q "^gh pr merge 7 --merge --match-head-commit $(git -C "$GT_WORKER" rev-parse HEAD)$" "$FORGE_LOG" \
  || fail "gate (d): gh merge must be pinned to the verified SHA: $(cat "$FORGE_LOG")"
git -C "$GT_ORIGIN" merge-base --is-ancestor "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["merge"])' "$FORGE_DB")" main \
  || fail "gate (d): merge commit not on origin/main"
[ -f "$GT_DIR/work.txt" ] || fail "gate (d): local main was not fast-forwarded"
pass "github: merge pinned to the verified SHA, confirmed on origin/main, local base fast-forwarded"

# (e) success reported, nothing on base: no merge commit, or a merge into another branch
gt_case e "$GL_URL"
RC="$(gt_gate env FORGE_MODE=noop "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = not-merged ] || fail "gate (e): a merge that merged nothing must be not-merged (rc=$RC): $(cat "$GT_DIR.out")"
gt_case e2 "$GH_URL"
RC="$(gt_gate env FORGE_MODE=elsewhere "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = not-on-base ] || fail "gate (e): a merge into another branch must be not-on-base (rc=$RC): $(cat "$GT_DIR.out")"
gt_case e3 "$GH_URL"
RC="$(gt_gate env FORGE_MODE=fail "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = merge-failed ] || fail "gate (e): a refused merge must be merge-failed (rc=$RC): $(cat "$GT_DIR.out")"
[ "$(jget data.changed <"$GT_DIR.out")" = false ] || fail "gate (e): a refused merge changed nothing: $(cat "$GT_DIR.out")"
grep -q "branch protection" "$GT_DIR.out" "$GT_DIR.err" || fail "gate (e): a refused merge must show the CLI output: $(cat "$GT_DIR.out" "$GT_DIR.err")"
pass "a merge that landed nothing, landed elsewhere, or was refused is reported, not passed"

# (f) gitlab: glab with auto-merge off and a pinned sha; a squash puts the sha in squash_commit_sha
gt_case f "$GL_URL"
RC="$(gt_gate env FORGE_MODE=squash "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 0 ] && [ "$(gt_verdict)" = pass ] || fail "gate (f): clean gitlab merge must pass (rc=$RC): $(cat "$GT_DIR.out")"
[ "$(jget data.verifySkipped <"$GT_DIR.out")" = false ] || fail "gate (f): a set test.full must verify the merged tree: $(cat "$GT_DIR.out")"
grep -q "^glab mr merge 7 --yes --auto-merge=false --sha $(git -C "$GT_WORKER" rev-parse HEAD)$" "$FORGE_LOG" \
  || fail "gate (f): glab merge must disable auto-merge and pin the sha: $(cat "$FORGE_LOG")"
grep -q "^glab api projects/:id/merge_requests/7" "$FORGE_LOG" || fail "gate (f): glab must read the MR through the API"
gt_case f2 "$GL_URL"
set_forge body $'## Approvals\n- x: orchestrator relay round 2\n'
printf '{"slots":[{"name":"w1","branch":"w1","pr":"%s","relays":[]}]}' "$GL_URL" > "$GT_DIR/.rota/workers.json"
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main --check-only)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = provenance-fail ] || fail "gate (f): provenance must read the MR description via glab (rc=$RC): $(cat "$GT_DIR.out")"
pass "gitlab: glab path with auto-merge off, pinned sha, squash sha fallback, MR-description provenance"

# (h) a recorded PR with no origin remote is refused, never merged locally
gt_case h "$GH_URL"
git -C "$GT_DIR" remote remove origin
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = check-broke ] || fail "gate (h): PR without origin must be check-broke (rc=$RC): $(cat "$GT_DIR.out")"
[ ! -f "$GT_DIR/work.txt" ] && ! grep -q "merge" "$FORGE_LOG" || fail "gate (h): nothing may be merged"
pass "a recorded PR with no origin remote is refused, not merged locally"

# (i) gitlab fast-forward method: merged MR, no merge or squash sha; the verified sha must be on base
gt_case i "$GL_URL"
RC="$(gt_gate env FORGE_MODE=ff "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 0 ] && [ "$(gt_verdict)" = pass ] || fail "gate (i): a fast-forward-merged MR must pass (rc=$RC): $(cat "$GT_DIR.out")"
[ -f "$GT_DIR/work.txt" ] || fail "gate (i): local base not fast-forwarded"
pass "gitlab fast-forward merge (no merge_commit_sha) falls back to the verified sha on base"

# (j) the forge enforces the pin: a push after the gate's check makes the merge refuse
gt_case j "$GH_URL"
RC="$(gt_gate env FORGE_MODE=race "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = merge-failed ] || fail "gate (j): a moved PR head must make the merge fail (rc=$RC): $(cat "$GT_DIR.out")"
grep -q "does not match the pin" "$GT_DIR.out" "$GT_DIR.err" || fail "gate (j): the forge's refusal must be shown: $(cat "$GT_DIR.out" "$GT_DIR.err")"
pass "a PR head that moves after the check is refused by the pinned merge"

# (k) merged remotely but local base diverged: distinct verdict, never 'not merged'
gt_case k "$GH_URL"
( cd "$GT_DIR" && echo local > local.txt && git add local.txt && gt_git commit -q -m "unpushed local work" ) || fail "gate (k): local commit failed"
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = merged-remotely ] || fail "gate (k): diverged local base after a remote merge must be merged-remotely (rc=$RC): $(cat "$GT_DIR.out")"
[ "$(jget data.changed <"$GT_DIR.out")" = true ] || fail "gate (k): merged-remotely must report changed true: $(cat "$GT_DIR.out")"
[ "$(jget data.sha <"$GT_DIR.out")" = "$(git -C "$GT_ORIGIN" rev-parse --short=7 main)" ] \
  || fail "gate (k): merged-remotely must report the remote merge as data.sha: $(cat "$GT_DIR.out")"
[ "$(jget data.verifySkipped <"$GT_DIR.out")" = true ] || fail "gate (k): merged-remotely must say the merged tree was not verified: $(cat "$GT_DIR.out")"
git -C "$GT_ORIGIN" merge-base --is-ancestor "$(git -C "$GT_WORKER" rev-parse HEAD)" main || fail "gate (k): the PR should be on origin/main"
pass "a remote merge whose local fast-forward fails is merged-remotely, not unmerged"

# (g) verify output is kept: the failing command's output reaches stderr and a log survives
gt_case g "$GH_URL"
printf '{"test":{"full":["echo boom-marker; exit 1"]}}' > "$GT_DIR/.rota/config.json"
RC="$(gt_gate env TMPDIR="$TMP_GT" "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 1 ] && [ "$(gt_verdict)" = verify-failed ] && [ "$(jget data.changed <"$GT_DIR.out")" = true ] \
  || fail "gate (g): a failed verify must be verify-failed with changed true (rc=$RC): $(cat "$GT_DIR.out")"
[ "$(jget data.sha <"$GT_DIR.out")" = "$(git -C "$GT_DIR" rev-parse --short=7 HEAD)" ] \
  || fail "gate (g): verify-failed must report the merge commit as data.sha: $(cat "$GT_DIR.out")"
grep -q "boom-marker" "$GT_DIR.err" || fail "gate (g): a failed verify must show its output: $(cat "$GT_DIR.err")"
grep -q "boom-marker" "$TMP_GT"/rota-gate-verify-* 2>/dev/null || fail "gate (g): the verify log must be kept on failure"
pass "verify output is shown on failure and the log is kept"

# (l) a merging gate run with the base branch not checked out is a resolution error
# (exit 3) and merges nothing.
gt_case nobase "$GH_URL"
( cd "$GT_DIR" && git checkout -q -b elsewhere ) || fail "gate (l): checkout failed"
RC="$(gt_gate "$ROTA_BIN" --json worker gate w1 --base main)"
[ "$RC" = 3 ] || fail "gate (l): base not checked out must exit 3, got $RC: $(cat "$GT_DIR.out")"
if grep "pr merge" "$FORGE_LOG" >/dev/null; then fail "gate (l): must not merge with the base not checked out: $(cat "$FORGE_LOG")"; fi
pass "a merging gate refuses (exit 3) when the base branch is not checked out"

trap 'rm -rf "$TMP"' EXIT
