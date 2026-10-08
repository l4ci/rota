# Review verdict routing

`/rota-review` ends with one of three verdicts, `PASS`, `CONCERNS` or `FAIL`, and records it with `rota verdict add`, as do the `/rota-ship` second opinion and `/rota-qa` (which adds `INFRA-FAIL`). Callers route on the recorded verdict with `rota verdict route`, never on a report's last line (exit 3 means none was recorded: rerun the producer). Recording flags, the producer JSON block and the refusal rules live in code (`internal/verdict`, contract section "B2: verdicts" in `docs/contributing/contract/verdicts.md`); this reference holds what the code does not: what each verdict means, the question text, and the labels. Any skill that gates on a pre-merge review consumes the same contract.

## Verdict semantics

| Verdict | Meaning |
|---------|---------|
| `PASS` | No concerns worth surfacing. The diff matches intent and respects conventions. |
| `CONCERNS` | The diff works, but flag these before merge: convention drift, suboptimal patterns, stale scaffolding. Not a regression. |
| `FAIL` | Merging would regress behavior, break intent, or violate a hard-boundary `DECISIONS.md` entry. The ship verbs refuse the branch until a newer verdict replaces the FAIL; the user fixes via `/rota-work` or `/rota-debug` and reruns the review. |

## Consumer routing

`data.next` from `rota verdict route` says what to do:

- **`continue`** (PASS): proceed to the next step silently.
- **`ask`** (CONCERNS): surface each concern inline, then ask:
    - **Header:** `"Concerns"`
    - **Question:** *"Review surfaced N concerns on `<branch>`. How should I proceed?"*
    - **Options** (single-select):
      1. *"Address via `/rota-work` (Recommended)"* — *"Route the concerns to `/rota-work` as a fix list; rerun the calling skill after."*
      2. *"Ship anyway"* — *"Proceed with the integration despite the concerns."*
      3. *"Stop"* — *"Leave the branch as-is; no integration now."*
- **`surface`** (an advisory gate: QA under `qa.gate: "advisory"`, or any QA `INFRA-FAIL`): surface the findings and continue. `data.advisory` is true.
- **`stop`** (FAIL): stop unconditionally. Surface the findings; never auto-route to ship/merge.

## Why "Ship anyway" never auto-picks

*"Address via /rota-work"* is the safe routing: it goes back through review on the next ship attempt and surfaces repeat concerns. *"Ship anyway"* is a user-volition gate: it overrides surfaced concerns and produces a public artifact (merge or PR) on the user's authority. It is an **acceptance-of-risk** answer, never auto-picked at any autonomy level. If a project genuinely wants concerns ignored, set `ship.review` to `false`.

## Producer-side relay (standalone `/rota-review` runs)

When `/rota-review` is invoked directly (not from `/rota-ship`), it relays the verdict to the user as the final product instead of routing on it:

- **`PASS`**: tell the user *"Ready to ship. Run `/rota-ship`."*
- **`CONCERNS`**: print the concerns inline and suggest the next move: *"Address via `/rota-work` and rerun `/rota-review`, or accept and ship via `/rota-ship`."*
- **`FAIL`**: tell the user the merge would regress and suggest fixing via `/rota-work` or `/rota-debug`. Don't route to `/rota-ship`.

When `/rota-review` is invoked from `/rota-ship`, the parent owns the routing: return the verdict and stop; do not run this relay.

The reviewer rubric (what makes a diff PASS, CONCERNS or FAIL) stays in each producer's brief; only the verdict-to-next-step mapping moved into code.

## Carrier-label override

When a non-canonical caller (e.g. `/rota-ship` Step 3.5 second-opinion gate, or any producer that emits the same PASS/CONCERNS/FAIL shape) surfaces concerns, it MAY label them with a carrier prefix, so the user can tell them from the primary `/rota-review` concerns in a session that runs both.

Convention: prefix surfaced concern lines with the producer's name, e.g. *"Second-opinion concerns:"* before the bullets. Routing (`rota verdict route`) is unchanged; only the label differs.
