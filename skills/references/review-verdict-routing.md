# Review verdict routing

`/rota-review` ends with one of three verdicts — `PASS`, `CONCERNS`, or `FAIL` — and records it with `rota verdict add`, as do the `/rota-ship` second opinion and `/rota-qa` (which adds `INFRA-FAIL`). Callers route on the recorded verdict with `rota verdict route`, never on a report's last line (exit 3 means none was recorded: rerun the producer). Recording flags, the producer JSON block and the refusal rules live in code (`internal/verdict`, contract section "B2: verdicts" in `docs/design/contract/verdicts.md`); this reference holds what the code does not: what each verdict means, the question text, and the labels. Any future skill that gates on a pre-merge review consumes the same contract.

## Verdict semantics

| Verdict | Meaning | Caller should |
|---------|---------|---------------|
| `PASS` | No concerns worth surfacing. The diff matches intent and respects conventions. | Continue silently. The reviewed work is integration-ready. |
| `CONCERNS` | The diff works, but surfaces should be flagged before merge — convention drifts, suboptimal patterns, or stale scaffolding. Not a regression. | Surface each concern, then ask how to proceed (see Consumer routing below). |
| `FAIL` | Merging would regress behavior, break intent, or violate a hard-boundary `DECISIONS.md` entry. | Stop. Surface findings. The ship verbs refuse the branch until a newer verdict replaces the FAIL. The user fixes via `/rota-work` or `/rota-debug` and reruns the review. |

## Consumer routing

`data.next` from `rota verdict route` says what to do:

- **`continue`** (PASS) — proceed to the next step silently. No surfacing needed.
- **`ask`** (CONCERNS) — surface each concern inline, then use `AskUserQuestion`:
    - **Header:** `"Concerns"`
    - **Question:** *"Review surfaced N concerns on `<branch>`. How should I proceed?"*
    - **Options** (single-select):
      1. *"Address via `/rota-work` (Recommended)"* — *"Route the concerns to `/rota-work` as a fix list; rerun the calling skill after."*
      2. *"Ship anyway"* — *"Proceed with the integration despite the concerns."*
      3. *"Stop"* — *"Leave the branch as-is; no integration now."*
- **`surface`** (an advisory gate: QA under `qa.gate: "advisory"`, or any QA `INFRA-FAIL`) — surface the findings and continue. `data.advisory` is true.
- **`stop`** (FAIL) — stop unconditionally. Surface the findings; do not auto-route to ship/merge.

## Why "Ship anyway" never auto-picks

*"Address via /rota-work"* is the safe routing — it goes back through review on the next ship attempt and surfaces repeat concerns to the user. *"Ship anyway"* is a user-volition gate: it overrides surfaced concerns and produces a public artifact (merge or PR) on the user's authority. It is an **acceptance-of-risk** answer, never auto-picked at any autonomy level. If a project genuinely wants concerns ignored, set `ship.review` to `false`.

## Queue routing (`/rota-review --queue`, issue mode)

The queue loop is the consumer (`rota verdict route --for queue`). It routes per PR / MR and always posts the verdict as a `feedback` comment on each linked item and on the PR.

| `data.next` | Verdict | Action |
|---------|---------|--------|
| `ask` | `PASS` | `AskUserQuestion` merge / skip / stop; merge runs `rota ship pr-merge <pr> --confirm --confirm-note "<answer>"` (exit 4 = not merged) |
| `request-changes` | `CONCERNS` or `FAIL` | findings as feedback, `rota item state <ID> --to changes-requested`; no merge |

Exit 3 / 4 from any verb stops the queue. Label lifecycle: `references/issue-mode.md`.

## Producer-side relay (standalone `/rota-review` runs)

When `/rota-review` is invoked directly (not from `/rota-ship`), it relays the verdict to the user as the final product instead of routing on it:

- **`PASS`** — tell the user *"Ready to ship. Run `/rota-ship`."*
- **`CONCERNS`** — print the concerns inline and suggest the next move: *"Address via `/rota-work` and rerun `/rota-review`, or accept and ship via `/rota-ship`."*
- **`FAIL`** — tell the user the merge would regress. Suggest fixing via `/rota-work` or `/rota-debug`. Don't route to `/rota-ship`.

When `/rota-review` is invoked from `/rota-ship`, the parent owns the routing — return the verdict and stop; do not run this relay.

The reviewer rubric (what makes a diff PASS, CONCERNS or FAIL) stays in each producer's brief; only the verdict-to-next-step mapping moved into code.

## Carrier-label override

When a non-canonical caller of this routing (e.g. `/rota-ship` Step 3.5 second-opinion gate, or any future producer that emits the same PASS/CONCERNS/FAIL verdict shape) surfaces concerns, the caller MAY label them with a carrier prefix so the user can distinguish them from the primary `/rota-review` concerns in a session that runs both.

Convention: prefix surfaced concern lines with the producer's name and a dash, e.g. *"Second-opinion concerns:"* before listing the bullets. The routing (`rota verdict route`) is unchanged — only the prose label differs.

## See also

- `references/manual-gates.md`: *"Ship anyway"* as a user-volition gate alongside the other manual gates.
