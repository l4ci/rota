# Listing rota on awesome-claude-code

Prepared draft for [#604](https://github.com/l4ci/rota/issues/604). Nothing has been submitted. The maintainer files this by hand: the list's CONTRIBUTING.md requires a human to fill the web issue form, and the form cannot be submitted through `gh`.

Outcome: pending

Form read on 2026-10-09: `hesreallyhim/awesome-claude-code`, `.github/ISSUE_TEMPLATE/recommend-resource.yml`, plus `CONTRIBUTING.md`.

## Read before filing

1. **Eligibility date.** The form takes a resource with 14 days of active development since the first commit on the default branch, or 100 stars. rota's first commit is 2026-10-04 and it has 2 stars. A filing before 2026-10-18 is closed automatically. After that date, new commits since the first day are also expected.
2. **The ticket's categories are gone.** The issue names "Workflows or Tooling". The form now has neither. The closest fits are Agent Orchestration and Multi-Purpose (see below).
3. **"Specific to Claude Code" checkbox.** rota also runs on Codex. Claude Code is the primary host and the README says so, but the maintainer should decide whether to tick the box.
4. **One resource at a time.** Do not file another submission while this one is open.
5. **Style rule.** Descriptions state what the software does: no sales pitch, no addressing the reader, one line, no emoji.

## Form answers

| Field | Answer |
| --- | --- |
| Display Name | rota |
| Category | Agent Orchestration |
| Link | https://github.com/l4ci/rota |
| Author Name | l4ci |
| Author Link | https://github.com/l4ci |
| Description | see below |

Description (10-500 characters, 2 sentences, 213 characters):

```
rota turns a to-do list into reviewed, merged work. Skills for Claude Code and Codex capture items, build them in small commits, review the branch and merge it, with parallel multi-agent rounds for larger batches.
```

The first sentence is the README's opening sentence, word for word, as the ticket requires.

Checklist boxes: tick the first five. Leave the last box unchecked, as the form instructs. Tick "specific to Claude Code" only if the maintainer agrees (point 3).

### Why Agent Orchestration

The other candidates fit worse. rota's distinct feature is parallel rounds: an orchestrator assigns issues to standing workers, each in its own git worktree, and merges their PRs. That is agent orchestration. "Multi-Purpose" is the fallback if the maintainer reads rota as a single-agent workflow tool. The single-agent path is what the README leads with, so this is a real judgement call.

### Does the README's opening sentence work as a listing?

Mostly. "rota turns a to-do list into reviewed, merged work." is plain and descriptive, but it never says Claude Code, which this list cares about. The second sentence of the draft adds that. The README is unchanged.

## After filing

- Record the issue URL here and on #604.
- Replace `Outcome: pending` with accepted, or rejected plus the reason, and close #604 then.
