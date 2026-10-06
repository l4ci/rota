# A slot watcher (round wait) returns the slot whose worker prints ROTA-DONE,
# with the PR as evidence, and records it in workers.json.
e2e_fixture w1 w2
stub slow w1; stub slow w2                # both panes keep moving: nobody needs attention
RC=0; OUT="$(rota_j round wait --settle 0 --timeout 2)" || RC=$?
expect "$RC" 1 "a round of busy workers times out"
expect "$(jget data.timedOut <<<"$OUT")" true "timeout is reported"

stub done w1 https://github.com/o/r/pull/7
RC=0; OUT="$(rota_j round wait --settle 0 --timeout 20)" || RC=$?
expect "$RC" 0 "wait exits 0 once a worker is done"
expect "$(jget data.slot <<<"$OUT")" w1 "the finished slot comes back"
expect "$(jget data.state <<<"$OUT")" done "its state"
expect "$(jget data.evidence <<<"$OUT")" https://github.com/o/r/pull/7 "the ROTA-DONE argument is the evidence"
expect "$(slot_field w1 state)" done "workers.json records the slot as done"
expect "$(slot_field w1 pr)" https://github.com/o/r/pull/7 "workers.json records the PR"
e2e_pass "round wait returns the slot that printed ROTA-DONE and records its PR"

stub blocked w2 "which flag name do you want?"
RC=0; OUT="$(rota_j round wait --settle 0 --timeout 20)" || RC=$?
expect "$RC" 0 "wait exits 0 for a blocked worker"
expect "$(jget data.slot <<<"$OUT")" w2 "the blocked slot comes back (w1 was already returned)"
expect "$(jget data.state <<<"$OUT")" blocked "its state"
e2e_pass "round wait returns a ROTA-BLOCKED slot with its question"
