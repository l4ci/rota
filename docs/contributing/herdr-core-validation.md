# Herdr App/Core validation

This records the real compatibility run for #2584 on 2026-11-03. Setup and
recovery instructions are in [parallel rounds](../usage/parallel-rounds.md#herdr-app-and-core).

## Runtime and build

Herdr App 0.1.12 ran on the maintainer's Mac, viewing the existing adam machine.
Core 0.1.17 and Codex 0.166.0 ran on adam. Rota connected to the inherited local
Unix socket `/run/user/1001/herdr-core/default.sock`; it did not provision a
remote worker machine or start another Core/legacy Herdr server.

The final round exercised code commit `6734549d93dda10e4fe57f68cb619c08914fe6bd`,
tree `95de6e44381e9f41347a8bc58c6ce6a5178b86e7`. A clean `git archive` was built
with `go build -trimpath -buildvcs=false ./cmd/rota`; the external build manifest
records the commit/tree because the archive contains no Git metadata. Binary
SHA256: `888e3133ed86a35cd8fd00556a876b570acc63ab79481f94ccf0d7cbc4761672`.
Later documentation changes do not change that Go implementation.

Core identity stayed `00e08aace2e38f54ce844113e387974d`, with server PID 1117300.
These are validation identities, not values users should configure or hardcode.
Harness versions are informational; Rota probes capabilities instead of pinning
Codex versions.

## Results

| Acceptance area | Observed result |
| --- | --- |
| Normal launch | The base selected legacy `herdr/herdr-session` with Core markers. The branch's normal `rota orchestrate` ran keepalive and real Codex in owned App-associated terminal t12. |
| Native shell tools | Before any HC/Rota exports, the coordinator and both workers verified their own terminal/socket, actual supervisor holder and scoped config/state. The production launch supplies fixed per-run native environment settings; it does not forward all environment variables or change credentials/filter policy. |
| Two standing workers | Base app-associated standing-worker count: **0**. Branch clean-fixture count: **2**, ben/t13 and nia/t14, in separate worktrees. Both were independent terminals adopted with App `keep:true`. |
| Dispatch and drafts | Real signed assignments picked up. Earlier disposable-worker tests observed a busy relay queued then picked up without duplicate retry; blocked startup sent nothing; a retained draft caused exit 5 with nothing sent, and the same relay picked up once after clearing only the test draft. |
| Monitoring and completion | New Core subscriptions returned authoritative snapshots containing both workers. Native idle/done did not bypass proof, acceptance or review checks. The real train plan refused missing quality verdicts until the required reviews existed. |
| Orchestrator replacement | The real coordinator wrote a fresh `rota-pause` handoff. The parent terminated only Codex child 1040524. Keepalive restarted Codex as 1050413, retained supervisor/lease 1040339, recorded one restart and no progress failures, and the new agent consumed the handoff. Both worker owners, creation identities and process births stayed unchanged. |
| Review and landing | Two real fixture PRs received independent Spec/Quality PASS verdicts at their actual heads. The parent ran the normal train with `bash scripts/full.sh`, landed both PRs and verified fixture main `74be87d`. No Core worker performed a merge. |
| Wind-down | The replacement coordinator ran normal `rota round wind-down` through its native lease ancestry. Full verification ran; verdict was clean, drift 0, both merged slots parked. Both owned terminals finished/killed and all four recorded worker process births were gone. Unrelated t1/t2/t7/t9, coordinator t12 and the original shared Core remained live. |

The private disposable fixture was
[`l4ci/rota-herdr-core-validation-r48-20261103`](https://github.com/l4ci/rota-herdr-core-validation-r48-20261103).
Its two independent fixes landed as [PR 1](https://github.com/l4ci/rota-herdr-core-validation-r48-20261103/pull/1)
and [PR 2](https://github.com/l4ci/rota-herdr-core-validation-r48-20261103/pull/2).
The parent orchestrator inspected/published the fixture and owned its reviews,
gates, process control and merges. Workers implemented, recorded assertion
RED/PASS proof and opened PRs through the ordinary workflow.

## Human App observations

The maintainer supplied these observations through orchestrator round-48 relays;
they were not inferred from adam's API:

- Both earlier disposable W1/W2 workers were accessible in persistent App tabs.
- A typed `m: takeover check` received a response.
- Both remained accessible after closing/reopening only the Mac window.
- The maintainer identified the exact W2 output marker and closed that tab.
  Core t8 then finished/killed, all its recorded process births disappeared,
  and Rota reported unverifiable recovery rather than task completion.
- A later screenshot showed distinct native Agents titles, including `| w1`.
  Tab names still showed numbers; this does not establish distinct names in
  every view or automatic tab renaming.

The final clean fixture used the same Core and persistent-tab lifetime policy.
It verified worker adoption and lifecycle locally; the physical Mac steps were
not repeated for each new terminal. Actual SSH connection interruption was not
performed. Monitoring connection replacement and fresh snapshots were tested.

## Validation boundaries

The early blocked-startup, relay, draft and Mac checks used a candidate whose
build metadata recorded dirty source. Their artifacts are retained separately;
they are not presented as a clean final-commit build. The complete two-worker
round, native tool probes, replacement and wind-down used the exact clean build
identified above. Focused final-source tests cover those delivery guards too.

The tiny fixture used the file backlog. Explicit `AC-n` marker storage is
issue-backend-only: `plan pass` refused file items. After the behavioral criteria
passed Spec/Quality review and the full train, the parent recorded completion
with a documented `--unmet-reason` for that bookkeeping limitation. No behavioral
criterion was waived. This does not claim that file-backend acceptance marking
was implemented by the Core change. Real issue-backend adapter tests separately
retain proof, acceptance and PR gate authority.

Codex project trust was deliberately completed by the parent for the reviewed
fixture. An ignored fixture-local Codex sandbox setting handled this machine's
loopback sandbox restriction. Those setup actions did not change user accounts,
credentials, the Mac configuration or production permission policy.

Focused Go/legacy checks, frozen configuration records and `rota test run fast`
passed. The production repository's full gate/e2e suite is **not run:
merge-only by rule**; the parent owns that integration check. The fixture's
configured full check ran at landing and wind-down.

Runtime artifacts on adam are under `/tmp/rota-2584-live/` and the fixture's
ignored `.validation-evidence/`: exact build manifests, native tool probes,
before/after replacement snapshots, train and wind-down results, and independent
process-birth cleanup proof. Keep them until formal review completes.
