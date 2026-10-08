# Market review, October 2026

A gap analysis of rota v0.14.0 against what users of similar agent orchestrators ask for, and what practitioners complained about in September and October 2026. Written 2026-10-08 from a codebase audit, the open-issue trackers of twelve comparable projects, and Hacker News, Reddit and blog posts of the previous month. The ranked suggestions became backlog issues; the durable conclusions are in `.rota/KNOWLEDGE.md` under *Product: Market & competitors*.

Refresh this page before the next `/rota-vision` pass instead of re-running the research.

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
| Stall detection, reclaim, reap, `work.envSetup` | buildwork #15 stranded branches; vibe-kanban #765 auto cleanup (+8); claude-squad [#260](https://github.com/smtg-ai/claude-squad/issues/260) setup hook (+8) | covered, partly |

## Where rota falls short

Verified against the v0.14.0 tree, not only the research reports.

- No metrics at all. No verb records tokens, cost, wall time or pass rate per issue, worker, account or round. `round status` shows live slots only. The gate writes `.rota/gate-audit.jsonl` but nothing reads it.
- Overlap is file-path only. `round candidates` compares a `## Files` section and issue paths. Two issues that touch the same API contract or schema with different files pass the check.
- Slot isolation stops at the filesystem. `work.envSetup` runs a command per worktree, but there is no per-slot port range, database name or env template.
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
14. **Linear backend** (L). Third `backlog.backend`. Conductor and Zuse lead with Linear; weaker demand than everything above.

### Not recommended

- A GUI dashboard. September's flood of Show HN launchers and dashboards mostly scored under 10 points; rota's terminal stance and `--ui` screens are a position.
- WIP limits as a feature. Slots already are the limit; make them quota-aware (5) instead.
- Automatic knowledge capture. The one data point available says machine-written project memory hurts.

## Competitor snapshot

Top requests by thumbs-up on open issues, pulled 2026-10-08. Reaction counts in parentheses.

| Project | What it is | Stars | Top requests |
|---|---|---|---|
| [vibe-kanban](https://github.com/BloopAI/vibe-kanban) | Kanban UI running agents in worktrees | 28.3k | self-hosted GitLab (+26), Ralph mode (+14), Kiro CLI (+11), worktree cleanup (+8), WIP limits |
| [gastown](https://github.com/steveyegge/gastown) | Multi-agent "town" with mayor and polecats | 18.3k | rate-limit-aware instance swap (+7), limit resets (+3), silent-success bugs, nested repo confusion |
| [claude-squad](https://github.com/smtg-ai/claude-squad) | TUI for parallel agents in worktrees | 8.6k | worktree setup hook with ports (+8), multi-repo (+6), prompt lost before CLI ready |
| [orca](https://github.com/stablyai/orca) | ADE for fleets of agents | 87.4k | multi-repo diff (+42), Jujutsu workspaces (+43), multi-repo groups (+21) |
| [superpowers](https://github.com/obra/superpowers) | Skills methodology | 296.5k | agent teams (+94), slowness (+32), Kiro (+23), skip review for easy tasks (+17) |
| [paperclip](https://github.com/paperclipai/paperclip) | Agent "company" manager | 98.6k | local LLMs (+68), Copilot CLI (+44), OpenRouter (+35), knowledge layer (+16) |
| [beads](https://github.com/steveyegge/beads) | Git/Dolt-backed agent issue tracker | 27.7k | memory beads, versioned history, Dolt migration crashes |
| [ralph](https://github.com/snarktank/ralph) | Loop runner with prd.json | 21.9k | progress visibility (+10), auto-wait on limits (+5), Codex support |
| superset | IDE for 100+ parallel agents | 15.0k | choose worktree and branch names (+13), offline mode (+11) |
| [Backlog.md](https://github.com/MrLesk/Backlog.md) | Markdown board in git | 7.0k | search closed tasks (+5), per-epic ID prefix (+3), decision record flags |
| Conductor | Mac app, Claude/Codex in worktrees | closed | no public tracker; ships state dashboard, diff review, Linear |
| claude-flow | Swarm framework | 74.1k | issues are mostly bot-authored epics; near-zero reactions, weak signal |

## Community signals, September to October 2026

- Semantic conflicts that worktrees don't catch: Foremerge (HN, 45 points) has agents declare scope up front; the author argues a conflict found in a diff is found too late.
- First-party orchestration: Anthropic's projects redesign (2026-09-17) runs a coordinator with parallel cloud sessions; Codex CLI 0.156 (2026-09-22) made managed worktrees the default; Claude Code has an agent view, agent teams and `/batch`.
- Usage limits: the 2026-09-14 change replaced the summer boost with a permanent 125% of baseline, a net cut of about 17% for heavy users.
- Verification as the bottleneck: review time up, no-review merges up, agentic PR pickup slower (secondary data, unverified).
- Token burn: "ten agents in parallel uses your quota roughly ten times as fast"; the forgekit fix was a parallel cap of two, fresh subagents and model routing.
- Praised practices: one worktree per task plus an orchestrator; two to five sessions; an intent lease logged before coding; Ralph loops with fresh context per iteration; plan approval and human-curated AGENTS.md.

## What the research got wrong

Three subagent claims did not survive a check against the tree.

- The audit reported 16 untested packages including merge-critical ones. The real count is 6, all utilities.
- The competitor sweep listed a worktree setup hook, overlap checks, stall detection, multi-repo and merge approval as missing. rota has `work.envSetup`, the candidates overlap pass, `round.stallMinutes`, umbrella mode and `ship.mergeApproval`.
- The audit referred to legacy `hv-` helper names that no longer exist.

Coverage caveats: X, YouTube and most Reddit threads were not reachable, so social claims rest on titles and Hacker News. Conductor and herdr have no public trackers. Competitor requests come from issue titles and reaction counts, not issue bodies. The "441% review time" and "3% AGENTS.md" figures are each from one secondary source.

## Sources

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
