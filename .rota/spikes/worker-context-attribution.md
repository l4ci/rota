---
name: worker-context-attribution
branch: spike/worker-context-attribution
status: done
finished: 2026-10-19
created: 2026-10-19
---

# spike/worker-context-attribution

## Question

Which source fills a Claude worker's context most (brief, KNOWLEDGE, subagent reports, tool output, skill text, prose), and which single trim comes first?

## What was tried

**Round measured: 25** (the latest full round; #883, #884, #885, #888, #890, #891; slots ben, dana, kit, all Claude on account `work`). Round 26 was one issue (#895); round 27 is still running, so neither is included. Round 25 had no other accounts or harnesses (`rota round` ledger, `.rota/ledger.jsonl`).

**Sessions.** Claude Code logs under `~/.claude-work/projects/-home-vo-dev-rota--worktrees-{ben,dana,kit}/*.jsonl` whose timestamps overlap the round's ledger window (17:02 to 17:46Z). 21 files: 7 worker sessions (>600 kB, 13 to 40 turns, 145 turns total) and 14 short sessions (<600 kB, 2 to 4 turns each; no `/rota-work` content, mostly Read/Grep and a 11% brief share, so they look like the orchestrator's review and gate runs, not worker work). The tables treat the 7 as the worker sample. No session carried `isSidechain` entries and no `subagents/` log existed, so nobody used a subagent.

**Method.**
1. Per assistant turn (deduplicated by `message.id`), `usage` gives the exact context: `input + cache_creation + cache_read`; `output_tokens` is exact too.
2. The first turn's context is the preamble. It is split by the logged first-turn attachments (`skill_listing`, `instructions` = CLAUDE.md + AGENTS.md, `deferred_tools_delta`, `agent_listing_delta`, `hook_additional_context`) at 3.6 chars/token. The remainder is the Claude Code system prompt and tool schemas, which are not in the log. The first user message is the brief.
3. Everything after turn 1 is split by source from the log entries: `tool_result` classified by the originating `tool_use` (Agent/Task = subagent report; Skill or a Read/cat/sed of `skills/`, `references/`, `SKILL.md` = skill or reference text; any command touching `rota knowledge|decisions|glossary` or KNOWLEDGE.md = KNOWLEDGE; otherwise the tool name), assistant text, thinking and tool_use input = model prose. Characters are scaled so that the shares sum to the session's measured growth (last context + last output − first context).
4. Two views. **Entered**: tokens that enter the context once. **Cache-read**: each turn's `cache_read` split across sources by their share of that turn's context. In the 7 worker sessions the logged usage totals are 11.28M cache-read, 0.43M uncached input plus cache write, 0.07M output: 95.7% / 3.7% / 0.6%, so the cache-read view is the one that matches where tokens go. (The 95.6% / 3.8% / 0.6% in the issue body is the same split over 14 days and 894 sessions.)

Limits: chars/3.6 is an estimate (error is spread across sources, preamble parts are the least exact); the remainder bucket depends on that estimate; one round, three slots, 145 turns. The script was a scratch file, not committed; the method above reproduces it.

## Findings

Share of cache-read tokens (11.28M across the 7 worker sessions). The `entered` column is tokens that enter context once (first-turn context plus everything added later plus output); in the 7 sessions it is 638k, of which the preamble is 380k (60%):

| session | turns | entered | cache-read | built-in prompt+tools | skill, deferred-tool, agent listings | CLAUDE+AGENTS+hook | brief | KNOWLEDGE | subagent | skill/ref text | Bash | Read/Grep | prose |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ben 37ac40a2 | 26 | 96k | 2093k | 44% | 17% | 5% | 1% | 4% | 0 | 15% | 8% | 0 | 6% |
| ben 8a142fbd | 14 | 89k | 1017k | 45% | 16% | 6% | 0 | 0 | 0 | 13% | 7% | 9% | 4% |
| dana 70149187 | 40 | 109k | 3477k | 39% | 15% | 5% | 1% | 0 | 0 | 11% | 22% | 0 | 6% |
| dana 872c48ec | 15 | 96k | 1169k | 44% | 20% | 5% | 0 | 5% | 0 | 9% | 5% | 0 | 3% |
| kit 6f2920f3 | 13 | 77k | 874k | 49% | 21% | 6% | 0 | 0 | 0 | 13% | 8% | 0 | 2% |
| kit 7f1e062d | 21 | 89k | 1551k | 44% | 22% | 6% | 1% | 0 | 0 | 10% | 11% | 0 | 4% |
| kit 8a7c6735 | 16 | 81k | 1099k | 45% | 20% | 6% | 1% | 0 | 0 | 12% | 12% | 0 | 3% |
| **7 sessions** | 145 | 638k | 11280k | **43%** | **18%** | 6% | 1% | 1% | 0 | **12%** | **13%** | 1% | 5% |

(Listings = skill listing 10.3% + deferred-tool list 6.4% + agent list 1.3% = 17.97% of reads; the per-part figures round to 17. Rows do not sum to 100: tool results other than Bash/Read/Grep and later user text, about 1 to 8%, are left out. The 14 short sessions add 225k entered and 486k cache-read, 52% built-in, and are not in the totals.)

- **Largest source: the fixed preamble.** The preamble is 60% of the tokens that enter a worker's context (380k of 638k entered across 7 sessions, about 52 to 58k per session; an earlier draft said 76%, which came from a growth miscalculation) and 66% of cache-read is re-reading it. Sessions are short (13 to 40 turns) and growth after turn 1 is only 20 to 30k tokens, so the preamble is re-read on every turn.
- **Inside the preamble, ~30k tokens (43% of reads) is Claude Code's own system prompt and tool schemas; rota cannot trim it.** The part a worker launch could influence, if the harness can scope them per worker (unverified), is the user-global skill listing (10% of reads, 30 kB of text), deferred-tool names (6%), agent list (1%): 18% of reads in total. CLAUDE.md + AGENTS.md + the SessionStart hook is another 6%.
- **Largest source rota's own text and tools control: Bash output (13%) and skill/reference text (12%).** Bash output is source reading (`sed -n`, `grep`, `gh issue view`), which is the work. Skill/reference text is the contract and `/rota-*` skill reads, 9 to 15% in every session.
- **The suspected trims are small.** Brief 0.7%, KNOWLEDGE and DECISIONS query output 1.3% (one worker pulled 13.8k chars once), subagent reports 0.04% (none were used, despite the contract's delegation rule), model prose 4.9% of reads. A caveman-style output rule can only touch the last number, so its token saving is under 5% of reads even if prose halves.
- Output is 0.6% of tokens by count. This spike did not measure price, so the cost ranking of model prose versus the listings is unmeasured.

## Decision

depends-on-X: the data supports one trim, but whether it can be applied depends on the harness (below). Not verified: that a launch flag or settings can actually drop the listings from a worker.

## Recommended approach

First trim: **launch workers with a pruned skill, deferred-tool and agent listing** (an allowlist of the rota skills, no personal skills such as nano-banana-pro, obsidian-vault, the Anthropic and Atlassian lists). Expected saving: 18% of cache-read tokens, about 2.0M of 11.3M in round 25, plus roughly 10k tokens per worker session of first-turn cache write. Per turn that is about 10 to 11k tokens of the ~52k preamble. Quality risk is low: those listings are the user's global config, not rota content.

Why this and not a text trim: brief, KNOWLEDGE and subagent reports total about 2% and model prose under 5%, so no wording change can beat it. The next candidate is the 12% skill/reference text (the contract and `/rota-*` skills read in full each session), a separate spike on progressive loading.

Open before building: (1) find the Claude Code setting or launch flag that scopes the skill and deferred-tool listing per worker (not checked here); (2) confirm the effect on one worker with `usage` at turn 1; (3) the 14 short sessions look like review runs and carry a 52% built-in share; they were not attributed.
