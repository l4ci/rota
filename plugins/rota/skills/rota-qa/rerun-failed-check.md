# Re-run a failed check alone (Step 4)

Loaded by `SKILL.md` Step 4 when a check is about to be recorded `met: false`.

**Re-run a failed check alone before recording it.** Parallel runners contend for one box, so time budgets fail on load, not truth. Before writing `met: false` for a check that timed out, blew a duration budget or hit a connection error, re-run it with nothing else in flight and put both `uptime` readings in `evidence`. A timeout means the assertion never ran: read the runner's output before theorizing about the code. Never raise the budget; if the check is too slow, cut its work in `.rota/qa/<target>.md`.

Connection-refused and address-in-use errors across many checks at once are infrastructure, not findings: re-run serially before reporting. A check still `met: false` after its lone re-run is a finding: record it, don't loop on it.

- **Runner subagent timeout** — re-run that check alone. Passes solo: the red was contention; record `met: true` with both `uptime` figures in `evidence`. Times out solo too: `met: false` with `evidence: "timeout after Ns at load <figure>, reproduced alone at load <figure>"`. QA continues either way; the verdict reflects the confirmed result.
