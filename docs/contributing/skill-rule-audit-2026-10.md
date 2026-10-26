# Skill-rule enforcement audit, October 2026

The [complete inventory](skill-rule-inventory-2026-10.md) finds 387 matching source lines: 12 fully verb-enforced rows, 362 prose-only rows and 13 descriptive matches. Most prose-only rows require judgment or include an obligation beyond the associated verb's guard. The strongest documented enforcement gaps concern reviewer workspaces and developer config, filed as [#2144](https://github.com/l4ci/rota/issues/2144) and [#2145](https://github.com/l4ci/rota/issues/2145).

This is the audit requested by [#2125](https://github.com/l4ci/rota/issues/2125), not an implementation of those new execution boundaries.

## Scope and method

The snapshot is commit `dd371b42a8472d95ef34a2910a8fc0a6112a4ddc`, before this PR's prose cuts. It contains 133 regular files under `skills/`: 129 Markdown files, including 20 `SKILL.md` files and 37 shared reference files. Per-skill `references` symlinks point to that shared directory and are counted once at their canonical paths. The issue's approximate 2,900 top-level lines was close: the snapshot has 2,912 `SKILL.md` lines, but all canonical Markdown totals 7,067 lines.

Extraction scans every canonical regular file for case-insensitive whole-word `never` or `must`, including front matter, tables, examples and per-skill references. It deliberately does not expand into every `do not`, `required`, or equivalent imperative. Each matching physical line has an inventory row with its exact text and an immutable source link; follow that link for wrapped continuation. Multiple obligations sharing a line share a row, and repeated obligations remain separate occurrences. Counts describe rows, not unique atomic rules.

Tags mean:

- **verb-enforced (hard):** the supported verb rejects the prohibited action or implements the stated invariant, in the scope named in the row. Its code location is cited. This is not a claim that raw shell commands cannot bypass rota.
- **prose-only (soft):** some part of the obligation still depends on the agent. A mixed row stays soft, with its partial guard explained. A warning, an optional hook, a lint that pins wording, or a schema that cannot establish the truth of a report does not enforce the complete instruction.
- **not-a-rule:** a descriptive occurrence or example, such as “must-haves” or a generated UI suggestion that was “never typed.” These remain visible so the extraction is auditable.

For example, the gate requires an alternate-harness review record, but cannot prove a reviewer actually ran or that its result was not guessed. Confirmation flags cannot establish that a human supplied the note. The inventory retains those semantic instructions. The 12 hard rows are R018, R037, R039, R050, R120, R141, R167, R248, R263, R305, R306 and R318.

## Evidence and counting

We inspected the shared checkout's first 3,918 ledger rows, its 16 bug-report records, the relevant curated knowledge entries, and linked issue/PR bodies and threads. Local review packages supplied pointers rather than a normalized findings database. The ledger records workflow events, not instruction violations; a red gate is not automatically evidence of a prose-rule breach.

The table below preserves the event identities used for ranking. The counts are **documented historical behavior failures**, not failure rates. Some incidents predate today's wording: they justify guarding that behavior, not a claim that this exact version of the instruction was ignored. Repeated mentions of an event count once. The full historical review corpus was not exhaustively hand-coded; absence means **not observed in the reviewed evidence**, never “agents obey this rule.”

| Event | Documented behavior | Evidence |
|---|---|---|
| E1 | Reviewer changed live worktrees during a gate, round 11, PR #607 | [#1293's incident list](https://github.com/l4ci/rota/issues/1293), corroborated by the [curated isolation learning](https://github.com/l4ci/rota/blob/dd371b42a8472d95ef34a2910a8fc0a6112a4ddc/.rota/KNOWLEDGE.md#L79) |
| E2 | Reviewer changed live worktrees during a gate, round 12, PR #659 | [#1293](https://github.com/l4ci/rota/issues/1293); ledger row 193: `2026-10-09T06:29:50Z`, gate, round 12, issue 635, PR 659, `verify-failed` |
| E3 | Reviewer changed live worktrees/checkout during a gate, round 19, PR #823 | [#1293](https://github.com/l4ci/rota/issues/1293); ledger row 680: `2026-10-16T19:54:38Z`, gate, round 19, issue 821, PR 823, `verify-failed` |
| E4 | Reviewer wrote a test into the live checkout after archive extraction failed | [#1293](https://github.com/l4ci/rota/issues/1293), third incident bullet; no distinct PR identity recorded there |
| E5 | A round-36 Spec reviewer ran a mutating config migration against real HOME, overwriting global config without backup | [Curated “Reviewer binary probes run in a scratch HOME” record](https://github.com/l4ci/rota/blob/dd371b42a8472d95ef34a2910a8fc0a6112a4ddc/.rota/KNOWLEDGE.md#L96); no public incident thread located |
| E6 | Reviewer suites contributed to round-35 host overload; the gate of PR #1799 took 1,944 seconds | [#1815](https://github.com/l4ci/rota/issues/1815), including its explicit exclusion of reviewer exports from the test-lock fix; [curated reviewer-test learning](https://github.com/l4ci/rota/blob/dd371b42a8472d95ef34a2910a8fc0a6112a4ddc/.rota/KNOWLEDGE.md#L98). One round, not an invented count of individual test runs |
| E7a / E7b | A “behaviour-neutral” assignment narrowed #1696; a “dedup row” assignment left #1636 partial until a relay | [Review finding #1734](https://github.com/l4ci/rota/issues/1734), [PR #1691](https://github.com/l4ci/rota/pull/1691), and the [curated brief-scope record](https://github.com/l4ci/rota/blob/dd371b42a8472d95ef34a2910a8fc0a6112a4ddc/.rota/KNOWLEDGE.md#L152) |
| E8 | An orchestrator-session maintainer answer was misattributed as a relay, PR #214 | [PR #214](https://github.com/l4ci/rota/pull/214) and the [curated provenance record](https://github.com/l4ci/rota/blob/dd371b42a8472d95ef34a2910a8fc0a6112a4ddc/.rota/KNOWLEDGE.md#L160) |
| E9 | A worker cited its assignment brief as a relay, PR #225; corrected before completion | [PR #225](https://github.com/l4ci/rota/pull/225) and the [same provenance record](https://github.com/l4ci/rota/blob/dd371b42a8472d95ef34a2910a8fc0a6112a4ddc/.rota/KNOWLEDGE.md#L160) |

The ledger rows corroborate a gate failure and identity, not its cause; the incident reports provide the attribution. All local ledger line numbers refer to `/home/vo/dev/rota/.rota/ledger.jsonl` at collection time. Durable event fields above avoid requiring access to that ignored file.

The bug-report records supplied no additional verified agent-rule incidents. For example, [#2073](https://github.com/l4ci/rota/issues/2073) describes a gate allowing a blocking finding: an enforcement defect, not an agent ignoring review instructions. Bug counts, bounce counts and gate failures are not used as denominators or added to the ranking.

## Ranking the prose-only rows

The inventory's observation column ranks every soft row by its documented count. Rows with the same count tie; source ID orders ties for lookup only. The same event can substantiate multiple overlapping obligations, so the row counts are not additive.

| Rank | Rows / behavior | Observed incidents | Current protection and disposition |
|---|---|---|---|
| 1 | R315: reviewer does not edit, commit or stage | 5, E1–E5 | No universal reviewer write boundary. Split the mechanical protections into workspace preparation [#2144](https://github.com/l4ci/rota/issues/2144) and probe config isolation [#2145](https://github.com/l4ci/rota/issues/2145). Source isolation covers four events; config isolation addresses the separate high-impact event. |
| 2 | R326 and R327: do not change live checkout/worktrees or write probes into the repo | 4 each, E1–E4 | The gate's worktree guard detects registry changes after the fact; it does not prevent them or cover arbitrary file writes. [#1293](https://github.com/l4ci/rota/issues/1293) shipped prose and a wording test, not a managed execution boundary. Follow-up #2144 targets the remaining mechanical gap. |
| 3 | R137: record approvals under the real channel | 2, E8–E9 | The observed false-relay attribution is now checked by `worker gate` (`internal/worker/provenance.go:150`). The whole row remains soft because the gate cannot establish every actual approval/channel. Keep the semantic instruction; no duplicate enforcement issue for these already-checked mistakes. |
| 4 | R138: assignment brief is not a relay | 1, E9 | Same existing provenance check; it does not require the literal word `None` in every assignment-only body. No new issue for the observed failure. |
| Tied remainder | Every other prose-only inventory row | 0 observed | No attributable incident in this evidence set. Unknown actual frequency; no evidence-based ordering within this group and no new enforcement ticket justified solely by imperative wording. |

E6's reviewer test-budget instruction and E7's prohibition on assignment-scope narrowing are adjacent evidence outside this literal never/must inventory. They remain visible here, but are not miscounted as breaches of a different worker or cross-harness rule. The existing [test-lock work](https://github.com/l4ci/rota/issues/1815) and [scope finding](https://github.com/l4ci/rota/issues/1734) were read before filing follow-ups. Future observation should attach a source row or rule family, attempt/PR identity and evidence when an incident occurs; otherwise the ledger cannot answer this question quantitatively.

## Follow-ups filed

- [#2144 — Prepare reviewer workspaces through a fail-closed verb](https://github.com/l4ci/rota/issues/2144): own archive preparation, the pinned workspace, extraction failure and cleanup in the managed review path. A scratch cwd alone must not be advertised as an OS sandbox.
- [#2145 — Run reviewer binary probes with isolated config state](https://github.com/l4ci/rota/issues/2145): execute probes with temporary HOME/config roots, refuse setup failures and demonstrate that a mutating probe preserves the real config. This does not move harness credentials or claim to prevent arbitrary absolute-path access.

## Reproduce inventory coverage

Run from the repository root. This checks every source occurrence against the table, including literal text, descriptive rows and duplicate detection; it does not automate the classification judgment.

```python
import html
import re
import subprocess
from pathlib import Path

base = "dd371b42a8472d95ef34a2910a8fc0a6112a4ddc"
paths = subprocess.check_output(
    ["git", "ls-tree", "-r", "--name-only", base, "--", "skills/"], text=True
).splitlines()
expected = {}
for path in paths:
    text = subprocess.check_output(["git", "show", f"{base}:{path}"], text=True)
    for line, value in enumerate(text.splitlines(), 1):
        if re.search(r"\b(never|must)\b", value, re.I):
            expected[path, line] = value.strip()
inventory = Path("docs/contributing/skill-rule-inventory-2026-10.md").read_text()
rows = re.findall(
    r"^\| R\d+ \[(skills/[^:]+):(\d+)\]\([^)]*\) \| <code>(.*?)</code> \|",
    inventory, re.M,
)
actual = {(path, int(line)): html.unescape(value) for path, line, value in rows}
assert len(rows) == len(actual), "duplicate source rows"
assert actual == expected, (expected.keys() - actual.keys(), actual.keys() - expected.keys())
print(f"PASS: {len(actual)} source occurrences covered")
```

## Covered prose removed

The inventory preserves the pre-cut snapshot. Main was fetched before cutting and still resolved to the snapshot commit, so no base merge was needed. Five covered occurrences were removed in four small, local edits; semantic duties, commands, manual gates and recovery steps remain.

| Inventory rows | Removed repetition | Existing implementation |
|---|---|---|
| R018 | Repeated capability-membership validation sentence | `milestone check`: `internal/milestone/check.go:101`, `internal/cli/milestone.go:569` |
| R120 | Optional worktree count and mutually exclusive repo flags restated beside the command | `status add`: `internal/cli/backlog.go:492`, `:510`; the command signature remains |
| R167 | Item-removal effects, default preview, archive preservation and nondecreasing counters restated above the workflow | `item rm`: `internal/backlog/rm.go:331`, `:364`, `:409`; R2 still runs and shows the preview, R4 still selects the apply/archive mode, and the contract pointer remains |
| R305–R306 | “Never creates a second release” and “branch never leads binaries” explanations | `release publish`: `internal/cli/release_gate.go:173`; `release push --branch-only`: `internal/release/flow.go:187`, `:203`; failure/recovery instructions remain |

The cuts avoid the concurrent work, decide/learn, QA and review skill edits called out in the assignment. They remove 104 whitespace-delimited words (0.117%) and 666 bytes from canonical skill Markdown. Top-level SKILL.md files stay unchanged; the reduction is in their loaded references.

| Measure | Base | Branch |
|---|---:|---:|
| Canonical skill Markdown lines | 7,067 | 7,066 |
| Canonical skill Markdown words | 89,204 | 89,100 |
| Canonical skill Markdown bytes | 579,495 | 578,829 |
| Matching never/must source lines | 387 | 382 |
| Top-level SKILL.md words | 33,504 | 33,504 |

These measurements compare the snapshot to the checked-out branch and exclude the new audit documentation. The following read-only command reproduces them:

```python
import re
import subprocess
from pathlib import Path

base = "dd371b42a8472d95ef34a2910a8fc0a6112a4ddc"
paths = subprocess.check_output(
    ["git", "ls-tree", "-r", "--name-only", base, "--", "skills/"], text=True
).splitlines()
paths = [path for path in paths if path.endswith(".md")]
for label in ("base", "branch"):
    blobs = [subprocess.check_output(["git", "show", f"{base}:{path}"])
             if label == "base" else Path(path).read_bytes() for path in paths]
    print(label, "lines", sum(len(blob.splitlines()) for blob in blobs),
          "words", sum(len(blob.split()) for blob in blobs),
          "bytes", sum(len(blob) for blob in blobs),
          "matches", sum(bool(re.search(rb"\b(never|must)\b", line, re.I))
                         for blob in blobs for line in blob.splitlines()))
```
