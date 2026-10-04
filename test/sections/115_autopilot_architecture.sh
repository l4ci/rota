echo "autopilot tick runs the architecture trigger: both triggers, audit lines, off switches (#112)"

# File-mode solo rounds as section 109, one fixture per scenario because a
# review in flight blocks the next one. The tick is driven with round.autopilot
# on; no worker is ever dispatched (solo: the brief goes to the orchestrator).
APA_ENV="env -u HERDR_ENV -u HERDR_PANE_ID -u TMUX -u TMUX_PANE"
APA_HOLD=$$
apa_fixture() { # <dir>: a project with one slot, autopilot on, an area per review
  local d="$1"
  git init -q --bare "$d.git"
  mkdir -p "$d" && (
    cd "$d" && git init -q -b main . && git config user.email a@b && git config user.name n \
      && git commit -q --allow-empty -m init && git remote add origin "$d.git" && git push -q origin main \
      && mkdir -p .rota && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
  ) || fail "autopilot architecture fixture setup failed"
  APA_DIR="$d"
  printf 'stub worker contract\n' > "$d.contract.md"
  apa config set round.brief "$d.contract.md" >/dev/null
  apa config set round.scope open >/dev/null
  apa config set round.architectureAreas '["cli"]' >/dev/null
  apa config set round.autopilot true >/dev/null
  apa round start --holder-pid "$APA_HOLD" --slots 1 >/dev/null || fail "round start failed"
}
apa() { ( cd "$APA_DIR" && $APA_ENV ROTA_ROUND_HOLDER_PID="$APA_HOLD" "$ROTA_BIN" --json "$@" 2>/dev/null ); }
apa_feature() { # <id> <F-id>: one counted, closed feature
  apa item create --kind features --title "Feature $1" --body-file - <<<$'## Acceptance\n- [ ] works' >/dev/null
  ( cd "$APA_DIR" && echo "$1" > "$1.txt" && git add "$1.txt" && git -c user.email=a@b -c user.name=n commit -q -m "feat: add $1" )
  apa item complete "$2" --no-proof >/dev/null || fail "complete $2 failed"
}
apa_did() { python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(",".join(a["action"]+":"+a["target"] for a in d["did"] if a["action"] != "reconcile"))'; }
apa_audit() { [ -f "$APA_DIR/.rota/gate-audit.jsonl" ] && grep -c "\"verb\": \"round tick $1\"" "$APA_DIR/.rota/gate-audit.jsonl" || echo 0; }

# ── threshold ───────────────────────────────────────────────────────────────
APA1="$(mktemp -d "$TMP/apa1.XXXXXX")/proj"
apa_fixture "$APA1"
apa config set round.architectureEvery 2 >/dev/null
apa item create --kind features --title "Standing item" --body-file - <<<$'## Acceptance\n- [ ] works' >/dev/null
apa_feature one F02
apa_feature two F03
OUT=$(apa round tick) || fail "tick failed: $OUT"
DID="$(apa_did <<<"$OUT")"
case "$DID" in mint:*,assign:*) ;; *) fail "a tick over the threshold should mint then assign the review: $DID ($OUT)" ;; esac
[ "$(apa_audit mint)" = "1" ] && [ "$(apa_audit assign)" = "1" ] || fail "one audit line per mint and per assign: $(cat "$APA1/.rota/gate-audit.jsonl")"
case "$(cat "$APA1/.rota/BACKLOG.md")" in *"arch(cli): architecture review"*) ;; *) fail "the review item should be in the backlog" ;; esac
OUT=$(apa round tick)
case "$(apa_did <<<"$OUT")" in *mint:*) fail "a review in flight must not mint again: $OUT" ;; esac
pass "a tick at the threshold mints the review, assigns it ahead of the backlog and audits both"

# ── idle slot, nothing assignable ───────────────────────────────────────────
APA2="$(mktemp -d "$TMP/apa2.XXXXXX")/proj"
apa_fixture "$APA2"
apa config set round.architectureEvery 5 >/dev/null
apa_feature one F01
OUT=$(apa round tick) || fail "tick failed: $OUT"
case "$(apa_did <<<"$OUT")" in mint:*,assign:*) ;; *) fail "an idle slot with an empty queue should mint and assign a review: $OUT" ;; esac
pass "a tick with an idle slot and nothing assignable mints and assigns a review"

# ── off switches ────────────────────────────────────────────────────────────
APA3="$(mktemp -d "$TMP/apa3.XXXXXX")/proj"
apa_fixture "$APA3"
apa config set round.architectureEvery 0 >/dev/null
apa_feature one F01
apa_feature two F02
OUT=$(apa round tick) || fail "tick failed: $OUT"
case "$(apa_did <<<"$OUT")" in *mint:*) fail "round.architectureEvery 0 must never mint: $OUT" ;; esac
APA4="$(mktemp -d "$TMP/apa4.XXXXXX")/proj"
apa_fixture "$APA4"
apa config set round.architectureEvery 1 >/dev/null
apa_feature one F01
apa config set round.autopilot false >/dev/null
RC=0; OUT=$(apa round tick) || RC=$?
[ "$RC" = "4" ] && [ "$(apa_audit mint)" = "0" ] || fail "with round.autopilot off the tick refuses and mints nothing: rc=$RC $OUT"
case "$(cat "$APA4/.rota/BACKLOG.md")" in *"architecture review"*) fail "autopilot off must not mint" ;; esac
pass "round.architectureEvery 0 and round.autopilot off both leave the tick minting nothing"

rm -rf "${APA1%/proj}" "${APA2%/proj}" "${APA3%/proj}" "${APA4%/proj}" "$TMP"/apa?.contract.md
