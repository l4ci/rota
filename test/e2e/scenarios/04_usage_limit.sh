# A worker that prints a usage-limit line is LIMITED, never done or dead.
e2e_fixture w1 w2
stub limit w1; stub slow w2
OUT="$(rota_j worker poll --settle 0)" || e2e_fail "worker poll failed: $OUT"
expect "$(jget 'data.slots[0].state' <<<"$OUT")" limited "the limited slot"
expect "$(jget 'data.slots[1].state' <<<"$OUT")" busy "the other slot is unaffected"
expect "$(slot_field w1 state)" limited "workers.json records it"
e2e_pass "a usage-limit line classifies the slot LIMITED"
