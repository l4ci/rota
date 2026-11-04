# The runner's isolation holds (#2629): a parent global config and ssh agent
# (seeded by run.sh) never reach the scenario; fixture settings still apply.
e2e_fixture w1
case "$HOME" in "$E2E_TMP"/*) ;; *) e2e_fail "HOME $HOME is not scenario-owned" ;; esac
case "$XDG_CONFIG_HOME" in "$E2E_TMP"/*) ;; *) e2e_fail "XDG_CONFIG_HOME $XDG_CONFIG_HOME is not scenario-owned" ;; esac
[ -z "${SSH_AUTH_SOCK:-}${SSH_AGENT_PID:-}${ROTA_CONFIG_DIR:-}" ] || e2e_fail "ssh agent or ROTA_CONFIG_DIR leaked into the scenario"
[ -s "$E2E_HOSTILE/config.json" ] || e2e_fail "runner did not seed the hostile parent config"

OUT="$(rota_j config show round.stallMinutes)" || e2e_fail "config show failed: $OUT"
expect "$(printf '%s' "$OUT" | jget data.entries[0].value)" 30 "omitted key reads the default, not the parent config"
expect "$(printf '%s' "$OUT" | jget data.entries[0].source)" default "source of the omitted key"

printf '{"git":{"baseBranch":"main"},"round":{"dispatch":"tmux","stallMinutes":7}}\n' >"$E2E_ROOT/.rota/config.json"
OUT="$(rota_j config show round.stallMinutes)" || e2e_fail "config show failed: $OUT"
expect "$(printf '%s' "$OUT" | jget data.entries[0].value)" 7 "fixture setting still applies"
e2e_pass "parent config and ssh agent stay out; fixture config works"
