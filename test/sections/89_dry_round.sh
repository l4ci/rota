echo "dry round: rota-orchestrate's verbs in the skill's order on a fixture repo, plus a verb-existence lint (#63)"
# #63 acceptance: "a dry round on a fixture repo runs end to end using only the
# new skill and rota". A shell script cannot be the skill, so this section (a) runs
# the verbs in the order rota-orchestrate/SKILL.md describes (doctor, start,
# candidates, assign, wait, status/reconcile, gate with an escalated approval,
# wind-down, reap) and (b) lints that every `rota <group> <verb>` the skill and
# docs/usage/parallel-rounds.md name exists. Everything runs against FAKES: herdr
# is test/fakes/herdr (behind a wrapper that adds --version and `api snapshot`),
# gh is test/fakes/gh (issue and PR threads) with `pr view|merge` routed to
# test/fakes/fake_forge.py (one PR over a real bare origin). Nothing reaches a
# real forge, herdr or tmux. The section number is provisional.
#
# No ROTA_TEST_ROUND_EVENTS replay hook exists in this tree, and `round wait` on
# herdr always subscribes on HERDR_SOCKET_PATH, so step 5 needs a socket: a
# minimal fake server (same protocol as section 81's, which keeps its own inline)
# flips the slot to done and sends the one status event.

TMP_DY="$(mktemp -d)"
SRV=""
trap 'kill "$SRV" 2>/dev/null || true; rm -rf "${TMP_DY:?}"' EXIT
FK="$TMP_DY/fake"
mkdir -p "$FK/bin" "$FK/herdr" "$FK/doctor"

# ── fakes ───────────────────────────────────────────────────────────────────
# herdr: test/fakes/herdr plus the two calls it lacks. `api snapshot` prints
# $FK/snapshot.json; --version reports a supported minor.
cat > "$FK/bin/herdr" <<SH
#!/usr/bin/env bash
case "\$1" in
  --version) echo "herdr 0.9.3"; exit 0 ;;
  api) [ "\$2" = snapshot ] && { cat "$FK/snapshot.json"; exit 0; } ;;
esac
exec bash "$TESTDIR/fakes/herdr" "\$@"
SH
# gh: pr view|merge go to the one-PR forge fake (real git underneath), the rest
# (issue and PR threads, pr create) to the tracker fake.
cat > "$FK/bin/gh" <<SH
#!/usr/bin/env bash
case "\$1 \$2" in
  "pr view"|"pr merge") FORGE_TOOL=gh exec python3 "$TESTDIR/fakes/fake_forge.py" "\$@" ;;
esac
exec bash "$TESTDIR/fakes/gh" "\$@"
SH
chmod +x "$FK/bin/herdr" "$FK/bin/gh"
: > "$FK/herdr/pane.txt"

# doctor's tool lookup: its own PATH, so only these fakes (and real git) exist.
ln -s "$(command -v git)" "$FK/doctor/git"
cat > "$FK/doctor/herdr" <<'SH'
#!/bin/sh
case "$1 $2" in
  "--version "*) echo "herdr 0.9.3" ;;
  "integration status") echo "claude: current (v10) (/x/hook.sh)"; echo "codex: current (v8) (/x/codex.sh)" ;;
  *) exit 99 ;;
esac
SH
chmod +x "$FK/doctor/herdr"
mkdir -p "$TMP_DY/acct"
echo '{}' > "$TMP_DY/acct/.credentials.json"

# ── fixture repo: bare origin, project clone, one milestone, one item ──────
# The standing brief `round assign` hands over by pointer: assign refuses
# ("brief missing") without it, so the fixture carries the real one.
# white-box-begin: A9 #53 doclint
WORKER_CONTRACT="$REPO/references/worker-contract.md"
# white-box-end
ORIGIN="$TMP_DY/origin.git"
DY="$TMP_DY/proj"
git init -q --bare -b main "$ORIGIN"
git clone -q "$ORIGIN" "$DY" 2>/dev/null
(
  cd "$DY" && git checkout -q -b main 2>/dev/null
  git config user.email t@t && git config user.name t
  mkdir -p .rota/milestones references
  printf '.rota/\n.worktrees/\n' > .gitignore
  cp "$WORKER_CONTRACT" references/worker-contract.md
  printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
  printf -- '---\nid: M01\ntitle: "m"\nstatus: active\ndepends: []\n---\n' > .rota/milestones/M01.md
  # Doctor runs on the config a fresh herdr project has: one account, herdr dispatch.
  printf '{"work":{"dispatch":"herdr","accounts":[{"name":"a","configDir":"%s"}]},"refactor":{"verifyCommands":[]}}\n' "$TMP_DY/acct" > .rota/config.json
  git add .gitignore references && git commit -q -m seed && git push -q origin main
) || fail "dry round: fixture repo setup failed"

# dy <cmd...> runs in the project with the fakes first on PATH; dyj is rota --json
# with stderr dropped. DYHOLD is the lease holder: this shell, which outlives
# every call.
DYHOLD=$$
dy() {
  ( cd "$DY" && env PATH="$FK/bin:$PATH" FAKE_HERDR="$FK/herdr" FAKE_TRACKER_DB="$TMP_DY/gh.json" \
      FORGE_DB="$TMP_DY/forge.json" FORGE_LOG="$TMP_DY/forge.log" ROTA_GATE_SHA_WAIT=0 \
      HERDR_ENV=1 HERDR_WORKSPACE_ID=w9 HERDR_SOCKET_PATH="$FK/herdr.sock" "$@" )
}
dyj() { dy "$ROTA_BIN" --json "$@" 2>/dev/null; }
[ "$(dy bash -c "command -v gh")" = "$FK/bin/gh" ] || fail "dry round: gh must resolve to the fake, got $(dy bash -c "command -v gh")"
[ "$(dy bash -c "command -v herdr")" = "$FK/bin/herdr" ] || fail "dry round: herdr must resolve to the fake"

# ── 1. doctor ───────────────────────────────────────────────────────────────
rc=0; OUT="$(ROTA_TEST_DOCTOR_PATH="$FK/doctor" CLAUDE_CONFIG_DIR="$TMP_DY/acct" "$ROTA_BIN" --json -C "$DY" doctor 2>/dev/null)" || rc=$?
[ "$rc" = "0" ] || fail "dry round: doctor should pass on the fixture, got $rc: $OUT"
[ "$(jget data.ok <<<"$OUT")" = "true" ] || fail "dry round: doctor data.ok: $OUT"
[ "$(jget data.checks[1].name <<<"$OUT")" = "host" ] && [ "$(jget data.checks[1].status <<<"$OUT")" = "pass" ] || fail "dry round: doctor host check: $OUT"
# A failing check carries its hint and flips the exit code (the skill: fix every fail first).
rm "$FK/doctor/herdr"
rc=0; OUT="$(ROTA_TEST_DOCTOR_PATH="$FK/doctor" "$ROTA_BIN" --json -C "$DY" doctor 2>/dev/null)" || rc=$?
[ "$rc" = "1" ] && [ "$(jget data.ok <<<"$OUT")" = "false" ] || fail "dry round: doctor without herdr should exit 1: rc=$rc $OUT"
pass "doctor: healthy on the fixture, exit 1 and ok false once herdr is gone"

# ── 2. start ────────────────────────────────────────────────────────────────
# The issue goes in before start so start lists it; the config now also names the
# forge and the merge policy the later steps need.
printf '{"work":{"dispatch":"herdr","accounts":[{"name":"a","configDir":"%s"}]},"refactor":{"verifyCommands":[]},"issues":{"provider":"github","retryWaitSeconds":0},"autonomy":{"level":"loop"},"ship":{"mergeApproval":"all"}}\n' "$TMP_DY/acct" > "$DY/.rota/config.json"
dyj item create --kind features --title First --milestone M01 --body-file - <<<$'## Acceptance\n- [ ] works\nTouches internal/a.go' >/dev/null \
  || fail "dry round: item create failed"
printf '{"id":"cli","result":{"snapshot":{"agents":[]}}}\n' > "$FK/snapshot.json"
rc=0; OUT="$(dyj round start --holder-pid "$DYHOLD" --slots 1)" || rc=$?
[ "$rc" = "0" ] || fail "dry round: start exit $rc: $OUT"
[ "$(jget data.round <<<"$OUT")" = "1" ] || fail "dry round: first start is round 1: $OUT"
[ "$(jget data.slots[0].name <<<"$OUT")" = "ben" ] && [ "$(jget data.slots[0].branch <<<"$OUT")" = "park/ben" ] || fail "dry round: slot ben on park/ben: $OUT"
[ "$(jget data.lease.state <<<"$OUT")" = "live" ] || fail "dry round: start should hold a live lease: $OUT"
[ "$(jget data.drift <<<"$OUT")" = "0" ] || fail "dry round: a fresh round has no drift: $OUT"
[ "$(jget data.candidates[0].id <<<"$OUT")" = "F01" ] || fail "dry round: start lists F01: $OUT"
[ -d "$DY/.worktrees/ben" ] || fail "dry round: slot worktree missing"
pass "start: lease held, slot ben parked, drift 0, F01 listed"

# ── 3. candidates ───────────────────────────────────────────────────────────
OUT="$(dyj round candidates)"
[ "$(jget data.candidates[0].id <<<"$OUT")" = "F01" ] && [ "$(jget data.candidates[0].ready <<<"$OUT")" = "true" ] || fail "dry round: F01 should be ready: $OUT"
pass "candidates: F01 ready (criteria, dependencies, overlap)"

# ── 4. assign (tier above the default needs a reason) ───────────────────────
echo working > "$FK/herdr/status"
rc=0; OUT="$(dyj round assign F01 --check-only --tier heavy --holder-pid "$DYHOLD")" || rc=$?
[ "$rc" = "2" ] || fail "dry round: heavy without --tier-reason should exit 2, got $rc: $OUT"
rc=0; OUT="$(dyj round assign F01 --tier heavy --tier-reason "dry round exercises the heavy path" --holder-pid "$DYHOLD")" || rc=$?
[ "$rc" = "0" ] || fail "dry round: assign exit $rc: $OUT"
[ "$(jget data.agent <<<"$OUT")" = "ben" ] && [ "$(jget data.branch <<<"$OUT")" = "ben/f01-first" ] || fail "dry round: assign names ben and its branch: $OUT"
[ "$(jget data.tier <<<"$OUT")" = "heavy" ] && [ "$(jget data.tierReason <<<"$OUT")" = "dry round exercises the heavy path" ] || fail "dry round: assign reports tier and reason: $OUT"
[ "$(git -C "$DY/.worktrees/ben" symbolic-ref --short HEAD)" = "ben/f01-first" ] || fail "dry round: assign should cut the slot's branch"
grep -q '^agent start' "$FK/herdr/log" || fail "dry round: assign should have started the worker through herdr: $(cat "$FK/herdr/log")"
OUT="$(dyj round status)"
[ "$(jget data.slots[0].tier <<<"$OUT")" = "heavy" ] || fail "dry round: status shows the slot tier: $OUT"
pass "assign: tier heavy needs a reason, claims F01, cuts ben/f01-first, dispatches through fake herdr"

# The worker's side, simulated with plain git and the fake forge: it commits,
# pushes, opens a PR on the fake forge and prints ROTA-DONE. Nothing here is a rota
# verb the orchestrator calls.
(
  cd "$DY/.worktrees/ben" && echo work > work.txt && git add work.txt \
    && git -c user.email=t@t -c user.name=t commit -q -m "F01: do the thing" && git push -q origin ben/f01-first
) || fail "dry round: simulated worker push failed"
PRURL="$(dy gh pr create --title "F01 First" --body "## Approvals
none" --base main --head ben/f01-first)"
PRN="${PRURL##*/}"
python3 - "$TMP_DY/forge.json" "$ORIGIN" "$(git -C "$DY/.worktrees/ben" rev-parse HEAD)" <<'PY' || fail "dry round: could not seed the fake forge"
import json, sys
json.dump({"origin": sys.argv[2], "head": "ben/f01-first", "sha": sys.argv[3], "base": "main",
           "state": "OPEN", "merge": "", "body": "## Approvals\nnone"}, open(sys.argv[1], "w"))
PY
: > "$TMP_DY/forge.log"
# The host still reports ben working; the socket server flips it to done one
# second after the subscription and sends the event.
printf '{"id":"cli","result":{"snapshot":{"agents":[{"agent":"claude","agent_status":"done","cwd":"%s/.worktrees/ben","name":"ben","tab_id":"w9:t7"}]}}}\n' "$DY" > "$FK/snapshot.json"
cat > "$FK/server.py" <<'PY'
import json, os, socket, sys, time
sock, herdr, pr = sys.argv[1], sys.argv[2], sys.argv[3]
srv = socket.socket(socket.AF_UNIX); srv.bind(sock); srv.listen(4)
held = []
while True:
    c, _ = srv.accept()
    held.append(c)
    req = json.loads(c.makefile().readline())
    c.sendall(b'{"id":"%s","result":{"type":"subscription_started"}}\n' % req["id"].encode())
    time.sleep(1)
    open(os.path.join(herdr, "pane.txt"), "w").write("ROTA-DONE ben %s\n" % pr)
    open(os.path.join(herdr, "status"), "w").write("done\n")
    pane = req["params"]["subscriptions"][0]["pane_id"]
    try:
        c.sendall((json.dumps({"event": "pane.agent_status_changed", "data": {
            "pane_id": pane, "workspace_id": "w9", "agent_status": "done"}}) + "\n").encode())
    except OSError:
        pass
PY
python3 "$FK/server.py" "$FK/herdr.sock" "$FK/herdr" "$PRURL" >/dev/null 2>&1 &
SRV=$!
for _ in $(seq 50); do [ -S "$FK/herdr.sock" ] && break; sleep 0.1; done
[ -S "$FK/herdr.sock" ] || fail "dry round: fake herdr socket never came up"

# ── 5. wait ─────────────────────────────────────────────────────────────────
BEFORE="$(sha256sum "$DY/.rota/workers.json")"
rc=0; OUT="$(dyj round wait --settle 0 --timeout 20)" || rc=$?
[ "$rc" = "0" ] || fail "dry round: wait exit $rc: $OUT"
[ "$(jget data.slot <<<"$OUT")" = "ben" ] && [ "$(jget data.state <<<"$OUT")" = "done" ] || fail "dry round: wait should return ben done: $OUT"
case "$(jget data.evidence <<<"$OUT")" in *"/pull/$PRN") ;; *) fail "dry round: wait evidence should carry the PR: $OUT" ;; esac
[ "$(sha256sum "$DY/.rota/workers.json")" = "$BEFORE" ] || fail "dry round: wait wrote the registry"
[ "$(jget data.source <<<"$OUT")" = "herdr-event" ] || fail "dry round: ben should come back through the event: $OUT"
pass "wait: returns ben as done through the herdr event with the PR as evidence, writes nothing"

kill "$SRV" 2>/dev/null || true; SRV=""

# worker poll reads the ROTA-DONE line and records the PR on the slot.
dyj worker poll ben >/dev/null || fail "dry round: worker poll failed"
[ "$(python3 -c 'import json,sys; print([s for s in json.load(open(sys.argv[1]))["slots"] if s["name"]=="ben"][0].get("pr"))' "$DY/.rota/workers.json")" = "$PRURL" ] \
  || fail "dry round: worker poll should record the PR on the slot: $(cat "$DY/.rota/workers.json")"

# ── 6. status and reconcile ─────────────────────────────────────────────────
rc=0; OUT="$(dyj round status)" || rc=$?
[ "$rc" = "0" ] && [ "$(jget data.host <<<"$OUT")" = "herdr" ] || fail "dry round: status exit $rc / host: $OUT"
[ "$(jget data.slots[0].name <<<"$OUT")" = "ben" ] && [ "$(jget data.slots[0].hostState <<<"$OUT")" = "done" ] || fail "dry round: status shows ben done on the host: $OUT"
rc=0; OUT="$(dyj round reconcile)" || rc=$?
[ "$rc" = "0" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "dry round: reconcile reads only: rc=$rc $OUT"
[ "$(jget data.clean <<<"$OUT")" = "true" ] && [ "$(jget data.drift <<<"$OUT")" = "[]" ] && [ "$(jget data.unavailable <<<"$OUT")" = "[]" ] \
  || fail "dry round: reconcile should be clean with the host and forge reachable: $OUT"
pass "status and reconcile: ben done on the host, clean, no drift, nothing written"

# ── 7. merge under mergeApproval=all: escalate, answer, approve ─────────────
rc=0; OUT="$(dyj worker gate ben --base main --escalate)" || rc=$?
[ "$rc" = "4" ] || fail "dry round: gate under mergeApproval all should exit 4, got $rc: $OUT"
[ "$(jget data.verdict <<<"$OUT")" = "approval-required" ] || fail "dry round: verdict approval-required: $OUT"
[ "$(jget data.escalation.id <<<"$OUT")" = "e1" ] || fail "dry round: the refusal should carry escalation e1: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "dry round: a refused gate changes nothing: $OUT"
COMMENTS="$(dy gh api --paginate "repos/o/r/issues/$PRN/comments")"
grep -q 'Merge approval' <<<"$COMMENTS" || fail "dry round: the approval request should be on the PR thread: $COMMENTS"
# Re-gating posts nothing new.
dyj worker gate ben --base main --escalate >/dev/null || true
[ "$(python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' <<<"$(dy gh api --paginate "repos/o/r/issues/$PRN/comments")")" = "1" ] || fail "dry round: a re-gate must reuse the pending request"
# Not answered yet: held, pending.
rc=0; OUT="$(dyj worker gate ben --base main --approval e1)" || rc=$?
[ "$rc" = "4" ] && [ "$(jget data.blockedBy <<<"$OUT")" = "approval pending" ] || fail "dry round: an unanswered request holds the merge: rc=$rc $OUT"
# The maintainer answers on the thread; escalate check reads it.
dy gh api -X POST "repos/o/r/issues/$PRN/comments" -f body="approve" >/dev/null
rc=0; OUT="$(dyj round escalate check)" || rc=$?
[ "$rc" = "0" ] && [ "$(jget data.answered <<<"$OUT")" = "1" ] || fail "dry round: escalate check should see the answer: rc=$rc $OUT"
[ "$(jget data.escalations[0].answer.body <<<"$OUT")" = "approve" ] || fail "dry round: the stored answer is the reply: $OUT"
rc=0; OUT="$(dyj worker gate ben --base main --approval e1)" || rc=$?
[ "$rc" = "0" ] || fail "dry round: an approved gate should pass, got $rc: $OUT"
[ "$(jget data.verdict <<<"$OUT")" = "pass" ] && [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "dry round: pass and changed: $OUT"
python3 -c '
import json, sys
line = json.loads(open(sys.argv[1]).read().splitlines()[-1])
sys.exit(0 if line["gate"] == "merge-approval" and line["note"] == "approve" and line["escalation"] == "e1" else 1)' "$DY/.rota/gate-audit.jsonl" \
  || fail "dry round: the audit line should quote the answer and name e1: $(cat "$DY/.rota/gate-audit.jsonl")"
[ "$(git -C "$DY" rev-parse HEAD)" = "$(git -C "$ORIGIN" rev-parse main)" ] || fail "dry round: the merged base should match origin/main"
pass "gate: --escalate exits 4 and posts once, pending then answered, --approval merges and the audit line quotes the answer"

# ── 8. wind-down, then reap ─────────────────────────────────────────────────
printf '{"id":"cli","result":{"snapshot":{"agents":[]}}}\n' > "$FK/snapshot.json"
rc=0; OUT="$(dyj round wind-down --holder-pid "$DYHOLD")" || rc=$?
[ "$rc" = "0" ] || fail "dry round: wind-down exit $rc: $OUT"
[ "$(jget data.verdict <<<"$OUT")" = "clean" ] || fail "dry round: wind-down should be clean: $OUT"
[ "$(git -C "$DY/.worktrees/ben" symbolic-ref --short HEAD)" = "park/ben" ] || fail "dry round: wind-down should park ben"
LEASE="$(git -C "$DY" rev-parse --path-format=absolute --git-common-dir)/rota/round-lease.json"
[ ! -f "$LEASE" ] || fail "dry round: wind-down should release the lease"
pass "wind-down: clean, ben parked on park/ben, lease released"

# What is left (the skill: reconcile, then reap): wind-down clears the slot's handle,
# so nothing is left for reconcile to report.
OUT="$(dyj round reconcile)"
[ "$(jget data.clean <<<"$OUT")" = "true" ] && [ "$(jget data.drift <<<"$OUT")" = "[]" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] \
  || fail "dry round: reconcile after wind-down should be clean and write nothing: $OUT"
pass "reconcile: clean after wind-down, no dead tab left behind"

printf '{"workspaces":[],"agents":[],"processes":[]}\n' > "$TMP_DY/host.json"
rc=0; OUT="$(ROTA_TEST_REAP_HOST="$TMP_DY/host.json" "$ROTA_BIN" --json -C "$DY" reap 2>/dev/null)" || rc=$?
[ "$rc" = "0" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "dry round: reap preview exits 0 and changes nothing: rc=$rc $OUT"
KINDS="$(python3 -c 'import json,sys; print(",".join(c["id"] for c in json.load(sys.stdin)["data"]["candidates"]))' <<<"$OUT")"
case ",$KINDS," in *,tab:*|*,process:*|*,worktree:*) fail "dry round: reap lists a live or parked thing: $KINDS" ;; esac
[ -d "$DY/.worktrees/ben" ] || fail "dry round: a reap preview removed the slot"
pass "reap preview: lists no tab, process or worktree, removes nothing (candidates: ${KINDS:-none})"

# ── 9. lint: every `rota <group> <verb>` the skill and the docs name exists ───
# A verb resolves when `rota <words> --help` descends the tree: each group's help
# lists its Commands and the next word must be one of them; a leaf ends the walk
# (later words are positional arguments). Extracted words are the run of
# lowercase [a-z-] tokens after `rota` in an inline code span or a fenced line;
# flags, placeholders, quotes and `rota-*` skill names end or never match.
LINT="$TMP_DY/lint.py"
cat > "$LINT" <<'PY'
import re, subprocess, sys
rota = sys.argv[1]
docs = sys.argv[2:]

def commands(path):
    out = subprocess.run([rota] + path + ["--help"], capture_output=True, text=True)
    if out.returncode != 0:
        return None
    names, on = [], False
    for line in out.stdout.splitlines():
        if line.startswith("Commands:"):
            on = True
        elif on and line.strip():
            names.append(line.split()[0])
        elif on:
            break
    return names

found = {}
for doc in docs:
    fence = False
    for n, line in enumerate(open(doc), 1):
        if line.lstrip().startswith("```"):
            fence = not fence
            continue
        for span in ([line.strip()] if fence else re.findall(r"`([^`]+)`", line)):
            toks = span.split()
            if not toks or toks[0] != "rota":
                continue
            words = []
            for t in toks[1:]:
                if not re.fullmatch(r"[a-z][a-z-]*", t):
                    break
                words.append(t)
            if words:
                found.setdefault(tuple(words), "%s:%d" % (doc, n))

bad, ok = [], 0
for words, where in sorted(found.items()):
    path = []
    verdict = "ok"
    for w in words:
        cmds = commands(path)
        if cmds is None:
            verdict = "no help for rota %s" % " ".join(path)
            break
        if not cmds:          # a leaf: the rest are arguments
            break
        if w not in cmds:
            verdict = "rota %s has no command %r" % (" ".join(path) or "<root>", w)
            break
        path.append(w)
    if verdict == "ok" and not path:
        verdict = "names no verb"
    verb = " ".join(path) if verdict == "ok" else " ".join(words[:2])
    if verdict != "ok":
        bad.append("%s: rota %s: %s" % (where, " ".join(words), verdict))
    else:
        ok += 1
for b in bad:
    print("MISSING " + b)
print("RESOLVED %d" % ok)
sys.exit(1 if bad else 0)
PY
# white-box-begin: A9 #53 doclint
rc=0; OUT="$(python3 "$LINT" "$ROTA_BIN" "$REPO/rota-orchestrate/SKILL.md" "$REPO/docs/usage/parallel-rounds.md" 2>&1)" || rc=$?
[ "$rc" = "0" ] || fail "dry round: verbs named in the skill or docs that do not exist: $OUT"
case "$OUT" in *"RESOLVED "*) ;; *) fail "dry round: the verb lint resolved nothing: $OUT" ;; esac
# The lint must be able to fail: a doc naming a verb that is not there is caught.
printf 'Run `rota round waitt` and `rota worker gate <slot>`.\n' > "$TMP_DY/bad.md"
rc=0; BAD="$(python3 "$LINT" "$ROTA_BIN" "$TMP_DY/bad.md" 2>&1)" || rc=$?
[ "$rc" = "1" ] && grep -q 'MISSING .*rota round waitt' <<<"$BAD" || fail "dry round: the lint should reject a made-up verb: rc=$rc $BAD"
# The C10 verbs resolve for real now, and a made-up neighbour is still caught.
printf 'Run `rota round reclaim ben`, `rota round return ben` and `rota round transfer 5 --to dana`.\n' > "$TMP_DY/c10.md"
rc=0; GOOD="$(python3 "$LINT" "$ROTA_BIN" "$TMP_DY/c10.md" 2>&1)" || rc=$?
[ "$rc" = "0" ] && grep -q 'RESOLVED 3' <<<"$GOOD" || fail "dry round: the C10 verbs should resolve: rc=$rc $GOOD"
printf 'Run `rota round reclaim ben` and `rota round bounce`.\n' > "$TMP_DY/c10b.md"
rc=0; BAD="$(python3 "$LINT" "$ROTA_BIN" "$TMP_DY/c10b.md" 2>&1)" || rc=$?
[ "$rc" = "1" ] && grep -q 'MISSING .*rota round bounce' <<<"$BAD" || fail "dry round: a made-up round verb should fail: rc=$rc $BAD"
pass "lint: every rota verb in rota-orchestrate/SKILL.md and docs/usage/parallel-rounds.md resolves ($(grep -o 'RESOLVED [0-9]*' <<<"$OUT"))"
# white-box-end

trap 'rm -rf "$TMP"' EXIT
rm -rf "${TMP_DY:?}"
