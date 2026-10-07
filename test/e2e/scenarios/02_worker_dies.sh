# A worker that dies mid-item: reconcile reports dead-tab; --apply marks the slot dead.
e2e_fixture w1 w2
stub slow w1; stub slow w2
RC=0; OUT="$(rota_j round reconcile)" || RC=$?
expect "$RC" 0 "reconcile with both workers alive"
expect "$(jget data.drift <<<"$OUT")" "[]" "no drift while both tabs are live"

stub exit w1                               # the agent process is gone, its window with it
RC=0; OUT="$(rota_j round reconcile)" || RC=$?
expect "$RC" 0 "reconcile reports, it does not fail"
expect "$(jget 'data.drift[0].kind' <<<"$OUT")" dead-tab "the dead worker's tab is drift"
expect "$(jget 'data.drift[0].slot' <<<"$OUT")" w1 "and it names the slot"
expect "$(jget data.changed <<<"$OUT")" false "plain reconcile writes nothing"
expect "$(slot_field w1 state)" busy "workers.json is untouched"

OUT="$(rota_j round reconcile --apply)" || e2e_fail "reconcile --apply failed: $OUT"
expect "$(slot_field w1 state)" dead "--apply marks the dead slot"
expect "$(slot_field w2 state)" busy "the live slot keeps its state"
e2e_pass "a worker that dies mid-item is a dead-tab; --apply marks only that slot dead"
