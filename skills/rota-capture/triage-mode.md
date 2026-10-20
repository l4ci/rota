# Triage Mode (`--triage`)

For tracker issues filed by others: the first read of a raw issue, before a round picks it up. Triage leaves a label and a comment; `rota round candidates` is unchanged and does not read `needs-info`. Capture's duplicate check only covers items the maintainer types in; this runs the same checks on an existing issue. It creates no item. Never grill the reporter: one question template, posted as a comment, is the whole conversation.

Copy this checklist and track your progress:
```
- [ ] Step T1 — Read the issue
- [ ] Step T2 — Redundancy search
- [ ] Step T3 — Prior decision
- [ ] Step T4 — Reproduce (bugs only)
- [ ] Step T5 — Outcome
```

### Step T1 — Read the issue

Split `--triage` on commas. For each `<N>`: `gh issue view <N> --json title,body,labels,comments` (`glab issue view <N>` on GitLab). Read the whole thread, not just the body: a decision or answer may sit in a comment. Skip an issue that already carries `in-progress` or an open claim.

### Step T2 — Redundancy search

Run items 1 and 2 of the duplicate check (`duplicate-check.md`, same folder) on 2 to 4 terms from the issue, excluding `#<N>` itself (open and archived items, `rota item shipped`, a code grep). Report where you looked, hit or not: the queries and the places searched. A hit is a duplicate item or a location that already does what the issue asks.

### Step T3 — Prior decision

`rota decisions query <topic>` for the issue's topic. Cite a hit with its decision heading. A hit is a boundary that forbids or already settled the issue. No hit: say so.

### Step T4 — Reproduce (bugs only)

Skip for features and tasks. Take the reporter's steps and run them against the current tree, in a throwaway directory or sandbox, never the reporter's data. Record exactly one:

- **confirmed**: the steps produce the reported behavior; quote the command and output.
- **failed**: the steps run and the behavior does not occur; quote what happened instead.
- **insufficient detail**: the steps are missing, ambiguous or need an environment you lack; name what is missing.

A reproduce is bounded: stop at the first answer, never investigate the cause.

### Step T5 — Outcome

> **Manual gate — comment, label and close on another reporter's issue.** Posting a comment, adding `needs-info` and closing are externally visible on a tracker issue someone else filed. This step is **always manual** — never auto-invoked, regardless of `autonomy.level`.

Pick one, then show the findings from T2 to T4 and the chosen outcome before acting. One question per issue, option 1 marked `(Recommended)`:

| Findings | Outcome |
|----------|---------|
| Duplicate, already implemented or a decision settles it | **closed with the reason**: comment (`rota item comment add #<N> --kind feedback --body-file …`) naming the duplicate `#M`, the location or the decision heading, then `gh issue close <N> --reason "not planned"` (`glab issue close <N>` on GitLab; `rota issues close` is for shipped work) |
| Bug failed to reproduce, or insufficient detail | **needs-info**: post the question template below with `rota item comment add #<N> --kind question --body-file …`, then `rota issues label <N> --add needs-info` |
| Nothing found and, for a bug, confirmed | **ready**: no label change; say so. Run `rota item ready #<N>` and report its verdict; whether a round offers it stays `rota round candidates`' call |

Options: 1. the recommended outcome · 2. *"Ready anyway"* (when 1 is closed or needs-info; when 1 is already ready, offer *"Needs-info anyway"* instead) · 3. *"Leave untouched"*.

Question template (post as an issue comment; fill every blank, ask only what the reproduce or search left open):

```
Thanks for the report. To pick this up we still need:

- Expected behavior: <what should happen>
- Actual behavior and exact error output: <what is missing>
- Steps to reproduce from a clean state: <which step failed or is unclear>
- Version (`rota --version`) and OS: <if relevant>

Triage checked: <where searched, decision queried, reproduce result>.
```

Remove `needs-info` (`rota issues label <N> --remove needs-info`) when a later triage finds the answer and the outcome becomes ready.

Hits are heuristic: show them, never decide silently. External PRs are issues with attached code: triage them the same way, and leave adoption to the gate.
