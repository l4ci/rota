# Market review, October 2026

A gap analysis of rota v0.14.0 against what users of similar agent orchestrators ask for, and what practitioners complained about in September and October 2026. Written 2026-10-08 from a codebase audit, the open-issue trackers of twelve comparable projects, and Hacker News, Reddit and blog posts of the previous month. The ranked suggestions became backlog issues; the durable conclusions are in `.rota/KNOWLEDGE.md` under *Product: Market & competitors*.

Refresh this page before the next `/rota-vision` pass instead of re-running the research.

Refreshed 2026-10-27: gstack added as a competitor (see *gstack crew*), the competitor table updated, and new entrants listed under *New entrants*.

## Summary

rota's core bets are the ones the market asks for. Rate-limit-aware account switching is the single most upvoted request on gastown, GitLab support is the top request on vibe-kanban, and a durable knowledge layer is what beads and paperclip users want most. The test-gated merge matches the most repeated diagnosis of the month: generation is cheap, verification is the bottleneck.

The gaps are in what rota does not show and does not catch. It records no cost, token or duration data per issue or worker, and the gate audit log is written but never read back. Its overlap check works on file paths only, while the loudest recent thread argues that worktrees isolate files, not plans. The biggest strategic shift is external: Anthropic and OpenAI both shipped first-party parallel sessions in September, so rota's value has to sit above the worktree, not in creating it.

## What the market validates

| rota feature | External demand | Signal |
|---|---|---|
| Account balancing and `limit watch` | gastown [#232](https://github.com/steveyegge/gastown/issues/232) "rate-limit-aware instance swapping"; ralph [#82](https://github.com/snarktank/ralph/issues/82) auto-wait for reset; Anthropic's 2026-09-14 limit change hit daily-loop users hardest | top ask, 2 projects |
| Gate verifies the merged tree, re-verifies main | Addy Osmani: "the bottleneck is no longer generation, it's verification"; review-time and no-review-merge data (secondary) | consensus |
| `.rota/` knowledge, decisions, handoff | beads #5877 "Memory Beads"; paperclip #1858 company-level knowledge layer; a wave of memory MCP launches | 3 projects |
| GitLab and self-hosted trackers | vibe-kanban [#1697](https://github.com/BloopAI/vibe-kanban/issues/1697), its most upvoted open issue | +26 |
| Umbrella mode | orca #1099 multi-repo diff (+42), #7568 multi-repo groups (+21); claude-squad #56 | +63 combined |
| Stall detection, reclaim, reap, `round.envSetup` | buildwork #15 stranded branches; vibe-kanban #765 auto cleanup (+8); claude-squad [#260](https://github.com/smtg-ai/claude-squad/issues/260) setup hook (+8) | covered, partly |

## Where rota falls short

Verified against the v0.14.0 tree, not only the research reports.

- No metrics at all. No verb records tokens, cost, wall time or pass rate per issue, worker, account or round. `round status` shows live slots only. The gate writes `.rota/gate-audit.jsonl` but nothing reads it.
- Overlap is file-path only. `round candidates` compares a `## Files` section and issue paths. Two issues that touch the same API contract or schema with different files pass the check.
- Slot isolation stops at the filesystem. `round.envSetup` runs a command per worktree, but there is no per-slot port range, database name or env template.
- Review comments are orchestrator-driven. A PR with reviewer comments waits for a bounce or transfer; the worker does not pick them up itself.
- Two harnesses. Claude Code and Codex only, while competitors add Kiro, Copilot CLI, OpenCode and goose.
- Knowledge lookup is strict. `knowledge query` needs the exact topic name, and no verb lists topics.
- Smoke scaffolding can hide failures. `OUT=$(rota ...)` under `set -e` aborts a section silently; this broke main once (#461).

## Suggestions

Ranked by demand signal times fit with rota's design. Size: S under a day, M a few days, L a round or more.

### Do now

1. **Round ledger and `rota round report`** (M). Record tokens, wall time, bounces, gate verdicts and merge time per issue, slot and account; read `gate-audit.jsonl` back as a per-PR timeline; print a report at `wind-down`. Sources: forgekit [#187](https://github.com/CodeWithJuber/forgekit/pull/187) (about 98% of 857M tokens spent re-reading context), ralph [#88](https://github.com/snarktank/ralph/issues/88), Copilot's per-session credit display.
2. **Scope overlap, not just file overlap** (M). Issues declare touched symbols, endpoints, schemas and migrations; `candidates` and `assign` compare declared scopes. Sources: [Foremerge](https://news.ycombinator.com/item?id=49789356), ["worktrees isolate files, not plans"](https://naw103.substack.com/p/parallel-coding-agents-without-the).
3. **Per-slot service isolation** (S). Export `ROTA_SLOT`, `ROTA_PORT_BASE`, `ROTA_DB_SUFFIX` into each worktree. Sources: claude-squad [#260](https://github.com/smtg-ai/claude-squad/issues/260), [Upsun](https://developer.upsun.com/posts/ai/git-worktrees-for-parallel-ai-coding-agents), [Signadot](https://www.signadot.com/blog/ai-generated-code-crisis/).
4. **Worker-side review loop** (M). A worker addresses PR review comments before the gate, within `round.maxBounces`. Sources: GitHub Copilot coding agent; review fatigue as the cap on agent count ([lowcode.agency](https://www.lowcode.agency/blog/claude-code-parallel-agents)).
5. **Quota-aware concurrency and Codex limit detection** (S). Active slot count follows account headroom; Codex limit messages park and reroute the slot. Sources: [Sep 14 limit change](https://explainx.ai/blog/anthropic-claude-code-limits-17-percent-cut-september-2026-august-2026), [bro #183](https://github.com/ThePlenkov/bro/pull/183).

### Next

6. **Fail-loud verb audit** (S). State-writing verbs re-read what they wrote and refuse destructive steps after a failed read; smoke template asserts exit codes. Sources: gastown [#4527](https://github.com/steveyegge/gastown/issues/4527), #4633; rota #461.
7. **Adopt workers rota did not launch** (L). `rota worker adopt` registers a Codex managed worktree, a Claude agent-team worktree or a cloud session branch as a slot so the gate, overlap and knowledge apply. Sources: [Codex CLI 0.156 refresh](https://www.remio.ai/post/openai-codex-0-154-0-turns-the-cli-into-a-parallel-working-system), [Anthropic projects redesign](https://claude.com/blog/projects-redesigned), [Claude Code agent view](https://code.claude.com/docs/en/agent-view).
8. **Review depth by issue size** (S). `ship.review` becomes a policy keyed on diff size and labels. Sources: superpowers [#1120](https://github.com/obra/superpowers/issues/1120) (+17), #743 (+32).
9. **Knowledge discovery** (S). Partial, case-insensitive topic matching and a topic listing. Keep writes human-curated: Osmani reports AI-written AGENTS.md lowered task success about 3% ([single source](https://addyosmani.com/blog/code-agent-orchestra/)).
10. **Merge order proposal for the train** (S). `worker train` orders green PRs to minimise predicted conflicts and prints the reason. Source: buildwork-skill [#15](https://github.com/dbhq-uk/buildwork-skill/issues/15).

### Later

11. **More harness adapters** (L). OpenCode and Copilot CLI first, then Kiro and goose. Sources: paperclip #2092 (+44), vibe-kanban #1708 (+11), superpowers #503 (+23). Do after the ledger so the data shows which harness earns it.
12. **Codex skill alignment audit** (S). Make skill wording harness-neutral and run one skill under Codex in smoke.
13. **Tests for the six untested packages** (S). `artifact`, `counter`, `gittest`, `golden`, `itembody`, `mapqa`.
14. **Linear backend** (L). Third issue source (`issues.source`). Conductor and Zuse lead with Linear; weaker demand than everything above.

### Not recommended

- A GUI dashboard. September's flood of Show HN launchers and dashboards mostly scored under 10 points; rota's terminal stance and `--ui` screens are a position.
- WIP limits as a feature. Slots already are the limit; make them quota-aware (5) instead.
- Automatic knowledge capture. The one data point available says machine-written project memory hurts.

## Competitor snapshot

Top requests by thumbs-up on open issues, pulled 2026-10-08 and refreshed 2026-10-27 (stars and requests are the 10-27 figures, the 10-08 star count in brackets). Reaction counts in parentheses.

| Project | What it is | Stars | Top requests | Since 10-08 |
|---|---|---|---|---|
| [gstack](https://github.com/garrytan/gstack) | Role-based skill suite (office hours, plan reviews, review, QA, ship) that added a crew controller, merge trains and a pre-gate in October; see *gstack crew* below | 137.8k (new) | not pulled | `/captain` (10-20), ship-queue trains (10-17), pregate (10-11); several releases a day |
| [orca](https://github.com/stablyai/orca) | Electron IDE for fleets of agents, coordinator/worker protocol over SQLite | 107k [94k on 10-16] | multi-repo diff (+48), Jujutsu workspaces (+47), LSP (+41), multiple windows (+40); see *Orca orchestration* below | near-daily releases: Jira boards and sprints, several PRs per workspace, usage meters, auto-permission modes, Kiro/Hermes/Kimi agents, mobile companion |
| [paperclip](https://github.com/paperclipai/paperclip) | Agent "company" manager | 107.6k [98.6k] | local LLMs (+68), Copilot CLI (+44), chat with agents (+40), OpenRouter (+35) | an exact cost ledger, a "Paperclip Review" GitHub status check, own model gateway, Codex runs that survive capacity errors |
| [superpowers](https://github.com/obra/superpowers) | Skills methodology | 305.6k [296.5k] | agent teams (+94), slowness (+32), Kiro (+24), "v7 brainstorm questions are annoying" (+24, new), OpenClaw (+20) | v7.0 rebuilt brainstorming (open questions first, play-back, builder check); users pushed back on the question load |
| [OpenSpec](https://github.com/Fission-AI/OpenSpec) | Spec-driven workflow: living `specs/`, delta change folders, propose/apply/archive, 30+ tool integrations; rota borrowed its delta model in #853 | 73.1k [72.7k on 10-21] | custom `openspec/` path (+32), specs from code (+16), configurable `specsPath` (+12) | v1.15: standalone binary, `update --dry-run --json`; archive warns before dropping delta text |
| [ruflo](https://github.com/ruvnet/ruflo) (was claude-flow) | Swarm framework | 79.5k [74.1k] | issues are mostly bot-authored; weak signal | about 20 releases in 5 days; Claude Managed Agents tools with a mandatory budget cap |
| [vibe-kanban](https://github.com/BloopAI/vibe-kanban) | Kanban UI running agents in worktrees | 28.5k [28.3k] | self-hosted GitLab (+26), keep the legacy board (+15 twice), Ralph mode (+14) | no release since April; near dormant |
| [beads](https://github.com/gastownhall/beads) (moved from steveyegge) | Git/Dolt-backed agent issue tracker | 28.2k [27.7k] | codex hook (+8), opencode skills (+5); low counts | v1.3.2, data-loss fixes in purge and backup |
| [ralph](https://github.com/snarktank/ralph) | Loop runner with prd.json | 22.1k [21.9k] | README setup (+6), auto-wait on limits (+5) | dormant since February |
| [gastown](https://github.com/gastownhall/gastown) (moved from steveyegge) | Multi-agent "town" with mayor and polecats | 18.6k [18.3k] | Beads fragility (+15), multi-provider engine (+13), monorepo support (+9), rate-limit-aware instance swap (+7) | no release since June; refinery merge-strategy request (+4) |
| [Paradev](https://github.com/ParadevAI/Paradev) (was superset) | IDE for 100+ parallel agents | 15.6k [15.0k] | Linux (+16), name workspace or branch at creation (+15), Windows (+14), offline and no sign-in (+11) | v2.0, cloud workspaces, paid tier, sign-in push; local-first users object |
| [claude-squad](https://github.com/smtg-ai/claude-squad) | TUI for parallel agents in worktrees | 8.7k [8.6k] | worktree setup hook with ports (+8), multi-repo (+6) | no release since August |
| [Backlog.md](https://github.com/MrLesk/Backlog.md) | Markdown board in git | 7.2k [7.0k] | search closed tasks (+5), decision-create flags (+3), cross-repo view (+3, new) | v1.54 archive keeps IDs and dependencies; keeps unknown frontmatter |
| Conductor | Mac app, Claude/Codex in worktrees | closed | no public tracker | 0.92 remote control from iOS, "actions", routines; Codex usage-limit request to an admin |

No competitor shipped a test-gated merge train, quota-based account switching or a knowledge layer like rota's between 10-08 and 10-27, except gstack's trains (below). Paperclip is the one moving into rota's metrics and review gaps.

## Orca orchestration, checked 2026-10-16

A code read of stablyai/orca at its 2026-10-16 head (MIT, TypeScript, about 14.8k source files, 8.5k open issues), done for the orchestration layer only. Orca is a desktop IDE; orchestration is one feature among browser, design mode, SSH/WSL placement, a mobile companion and twelve agent backends. Its coordinator is the user's own agent following a skill, with `orca orchestration` verbs over a Run, Task DAG and Dispatch model held in SQLite (`src/main/runtime/orchestration/`).

What it lacks that rota has: no tracker link into the task model, no gate that runs tests on a worker's output (`worker_done --outcome` is self-reported), no merge queue or PR flow ("compare the results and merge the winner"), no knowledge layer that the code read showed. On issue-to-PR it does not compete, as far as a code read without a run can tell.

What rota took from it, filed 2026-10-16:

| Orca mechanism | rota gap | Issue |
|---|---|---|
| Workers push typed `worker_done`, heartbeat, `ask` and escalation messages to a durable mailbox; the coordinator runs `check --ack --wait --types ...` | only `done` has a push path; `ROTA-BLOCKED`, questions and review replies are regex-parsed from the pane | #819 |
| Liveness is `live`, `unverifiable` or `exited`; only positive proof of exit permits stop, abandon or retry | contact loss reads as `dead`; "read the pane first" is prose, not a verb rule | #820 |
| The installed skill is a stub; `orca skills get orchestration` prints the guide matched to the binary | installed SKILL.md copies drift from the binary after `rota update` | #821 |
| Every mutation carries an idempotent receipt; recovery names `failedStage` and `residualResources` | lock sidecars guard concurrent writers, not a retried `assign` after a flaky `gh` call | #822 |

Not taken: the Task DAG (rota's unit is the issue), federation and remote placement, decision gates (`round escalate` covers it), the GUI and mobile app. Nothing was run; the mailbox and liveness rules are read from `skill-guides/orchestration.md` and its references, and no end-to-end test of the coordinator loop was found.

## gstack crew, checked 2026-10-26

A read of garrytan/gstack at v1.91.257 (MIT, TypeScript and Bun, 137.8k stars). gstack is a suite of about 60 skills that cast the agent as a team: `/office-hours` and four plan reviews (CEO, design, DX, eng), `/review`, `/qa` with a real browser, `/cso` security audits, `/ship`, plus design, iOS and browser tools. It runs on about ten hosts (Claude Code fully, Codex, OpenCode, Copilot, Kiro and others as experimental). rota borrowed from it once before, in May: the debug Iron Law, the second-opinion review, knowledge tiers and browser QA.

What changed: in October gstack moved into rota's lane. A pre-gate (10-11) runs cheap checks and import-graph-selected tests before paid work. A ship queue (10-17) builds merge trains. `/captain` (10-20) runs a crew: lanes on their own branches send a READY block (pinned head, CI state, pre-gate and ship receipts) and never merge; the controller fills a train at 3 READY PRs or one hour, merges each slot with `--match-head-commit`, checks base's tree against the gated prefix, bisects a red train, and freezes the queue when a backstop gate on base goes red. Its recommended setup pairs it with Capy.

What it lacks that rota has: rules enforced by a binary (gstack's controller is an agent reading fields), autopilot, a worker health model with reclaim and reap, overlap checks by files and `## Touches`, quota-aware accounts, GitLab, and a reviewed knowledge and decisions layer. Its review loop cap and head pinning rota already has (`round.maxBounces`, `--match-head-commit`). Its version-stamp receipts solve a problem rota does not have.

What rota took from it, filed 2026-10-27:

| gstack mechanism | rota gap | Issue |
|---|---|---|
| Backstop gate on base and a freeze record; trains and merges refuse until a repair lands | stop-on-red is prose in `reading-failures.md` | #2170 |
| A failing case reruns once; a pass is a logged flake, a second failure is real; a daily pass fixes the worst flake | train notes `TRANSIENT` only; no flake log | #2171 |
| `/retro` factory scorecard: lead time p50/p90, red-train and flake rates, each with its counting rule, `unavailable` never 0 | `round summary` has wall time and bounces only | #2172 |
| Train fill at N READY PRs or T since the oldest | train nudge fires at two, no wait bound | #2173 |
| Captain DIGEST with a "Needs you" section, ages and default replies | maintainer asks spread over several verbs | #2174 |

Not taken: a destructive-command and edit-boundary hook (rota's `rota hook guard`, #702, already guards workers), model overlays, the multi-host generator, telemetry, gbrain, and the design, browser and iOS skills. Ideas noted but not filed: provenance markers on `/rota-pause` items, a Taste class for decisions made in auto mode, per-question "never ask", a security review using `/cso`'s rubric. Nothing was run; the queue, receipt and pre-gate logic was read from docs and help text, not from the TypeScript.

## New entrants, September to October 2026

Found 2026-10-26. Star counts from the GitHub API that day; most are small and their numbers self-reported.

| Project | What it is | Stars | Overlap | Worth noting |
|---|---|---|---|---|
| [Kander](https://github.com/dualface/kander) | Kanban plus rule-driven multi-agent pipeline: worktrees, independent review agents, delivery gates, crash recovery | 159 | high | publishes production numbers (1,726 tasks in 68 days; 49% of review batches had a must-fix finding) |
| [Shipyard](https://github.com/cosscom/shipyard) | Remote-first fleet manager; agents run on a VPS, the desktop app is only a view | 327 | high, no merge gate | per-worktree ports and env from a shareable "kit" |
| [SVRF](https://github.com/nybarius/SVRF) | Merge queue for agent swarms: fold PRs, gate the folded tree once, bisect, land the rest | 2 | gate and train | checks base equals the gated tree after each merge |
| [claude-code-merge-queue](https://github.com/funador/claude-code-merge-queue) | Local queue serialising land, build and test across parallel agents | 131 | gate serialisation | treats shared-resource flakiness as the problem |
| [pitwall](https://github.com/aontwit/pitwall) | Claude Code plugin loop: interview, one task per round, verdict from a verifier that did not write the code, PR | 3 | issue to PR | all questions asked once, up front |
| [porch](https://github.com/CodeAlive-AI/pragmatic-orchestration) | Any harness orchestrates other vendors' agents; review, delegate, quota modes | 45 | cross-vendor workers, quota | strong model plans, cheaper executors build |
| [sno-station](https://github.com/sno-ai/sno-station) | Squad of agents across harnesses with one encrypted shared memory | 895 | knowledge, cross-agent review | nightly memory consolidation |
| [agent-memory](https://github.com/tigerless-labs/agent-memory) | Markdown-first memory shared by Claude Code and Codex, curated by a background job | 4.7k (not checked for organic growth) | knowledge layer | curation separate from capture |
| [okf-agent-memory](https://github.com/okf-memory/okf-agent-memory) | Git-native memory in Go on the OKF spec, MCP server, BM25 search | 841 | knowledge layer | progressive disclosure to cut tokens |

Also seen: pi-herdsman and pi-cowboy, herdr-native orchestrators for the Pi agent, which share rota's substrate.

First-party, from changelog aggregators (not primary sources): Codex CLI 0.162 to 0.164 added worktree tools, mid-response steering by default and presets; Claude Code 2.1.30x made subagents background by default in the desktop app and added `agent.spawn` and idle states to agent teams. No verified October change from Copilot, Cursor or Jules.

## Community signals, September to October 2026

- Semantic conflicts that worktrees don't catch: Foremerge (HN, 45 points) has agents declare scope up front; the author argues a conflict found in a diff is found too late.
- First-party orchestration: Anthropic's projects redesign (2026-09-17) runs a coordinator with parallel cloud sessions; Codex CLI 0.156 (2026-09-22) made managed worktrees the default; Claude Code has an agent view, agent teams and `/batch`.
- Usage limits: the 2026-09-14 change replaced the summer boost with a permanent 125% of baseline, a net cut of about 17% for heavy users.
- Verification as the bottleneck: review time up, no-review merges up, agentic PR pickup slower (secondary data, unverified).
- Token burn: "ten agents in parallel uses your quota roughly ten times as fast"; the forgekit fix was a parallel cap of two, fresh subagents and model routing.
- Praised practices: one worktree per task plus an orchestrator; two to five sessions; an intent lease logged before coding; Ralph loops with fresh context per iteration; plan approval and human-curated AGENTS.md.
- Added 2026-10-27. Merge queues rebuilt by individuals: approved PRs go stale after every merge and GitHub's queue needs an organisation ([alatas.dev](https://alatas.dev/blog/ai-agents-made-my-personal-github-repo-need-a-merge-queue.html)); SVRF, openmerge and gstack's trains answer the same complaint.
- Independent verification is the common pitch: Kander, pitwall and SVRF all sell protection against false greens.
- Too many questions backfire: superpowers v7's question-first brainstorming drew a +24 complaint within a week. Keep `/rota-brainstorm` questions few and pre-answered.
- Quota anxiety continues (HN 50222093, a 5-hour limit hit after $1.37 of usage); porch and paperclip ship cost reporting.

## What the research got wrong

Three subagent claims did not survive a check against the tree.

- The audit reported 16 untested packages including merge-critical ones. The real count is 6, all utilities.
- The competitor sweep listed a worktree setup hook, overlap checks, stall detection, multi-repo and merge approval as missing. rota has `round.envSetup`, the candidates overlap pass, `round.stallMinutes`, umbrella mode and `ship.mergeApproval`.
- The audit referred to legacy `hv-` helper names that no longer exist.

Coverage caveats: X, YouTube and most Reddit threads were not reachable, so social claims rest on titles and Hacker News. Conductor and herdr have no public trackers. Competitor requests come from issue titles and reaction counts, not issue bodies. The "441% review time" and "3% AGENTS.md" figures are each from one secondary source.

## Sources

- [gstack](https://github.com/garrytan/gstack): `captain/SKILL.md.tmpl`, `docs/captain.md`, `docs/ship-policy.md`, `docs/pregate.md`, `retro/SKILL.md`
- [paperclip v2026.1023.0](https://github.com/paperclipai/paperclip/releases/tag/v2026.1023.0), [superpowers v7.0.0](https://github.com/obra/superpowers/releases/tag/v7.0.0), [Conductor changelog](https://www.conductor.build/changelog.md)
- [Foremerge](https://github.com/naw103/foremerge) and its [HN thread](https://news.ycombinator.com/item?id=49789356)
- [Worktrees isolate files, not plans](https://naw103.substack.com/p/parallel-coding-agents-without-the)
- [Anthropic: projects redesigned](https://claude.com/blog/projects-redesigned)
- [Claude Code agent view](https://code.claude.com/docs/en/agent-view) and [agents](https://code.claude.com/docs/en/agents)
- [Codex CLI parallel refresh](https://www.remio.ai/post/openai-codex-0-154-0-turns-the-cli-into-a-parallel-working-system)
- [September 14 limit change](https://explainx.ai/blog/anthropic-claude-code-limits-17-percent-cut-september-2026-august-2026)
- [forgekit #187](https://github.com/CodeWithJuber/forgekit/pull/187)
- [CloudZero on agent cost](https://www.cloudzero.com/blog/claude-code-agents/)
- [Osmani: code agent orchestra](https://addyosmani.com/blog/code-agent-orchestra/)
- [Review bottleneck data](https://www.flowverify.co/blog/ai-code-review-bottleneck-2026-data)
- [buildwork-skill #15](https://github.com/dbhq-uk/buildwork-skill/issues/15)
- [claude-code-plugin #8](https://github.com/AndreyBegma/claude-code-plugin/issues/8)
- [Copilot credit display](https://github.blog/changelog/2026-07-08-github-copilot-in-visual-studio-code-june-2026-releases/)
- [Brief history of Ralph](https://www.humanlayer.dev/blog/brief-history-of-ralph)
- [Upsun worktree guide](https://developer.upsun.com/posts/ai/git-worktrees-for-parallel-ai-coding-agents)
- [Conductor review](https://madewithlove.com/blog/conductor-running-multiple-ai-coding-agents-in-parallel/)
