# Manual gates

Certain operations are **manual gates**: no `autonomy.level` (`"off"` or `"auto"`) may pass them on its own. They produce externally-visible state or commit the project to a hard boundary. Auto mode chains routing steps (one hop toward done) but never answers an acceptance-of-risk question (commit on the user's authority).

The registry lives in code. `rota gate list` prints every gate, whether a verb enforces it, the verbs and skills involved, and the state it creates. There are two kinds.

## Enforced gates: the verb refuses

| Gate | Verb | Skill site |
|------|------|------------|
| `tag-push` | `rota release push` | `/rota-release` Steps 10 and 11b |
| `release-publish` | `rota release publish` | `/rota-release` Step 11 |
| `public-filing` | `rota tracker suggest-upstream` | none (no skill files upstream issues) |
| `merge-approval` | `rota ship merge`, `rota ship pr-merge`, `rota worker gate`, when `ship.mergeApproval` covers the merge (`all`, or `paths` matching `ship.mergeApprovalPaths`) | `/rota-ship` Step 6b, `/rota-review --queue`, `/rota-work` gate step |
| `debug-reset` | `rota debug reset <ID> --reason <why>` (starts an item's failed-fix count again after the Iron Law halted it) | `/rota-debug` Step 9.5 |

The verb exits 4 with `data.blockedBy: "manual gate"` and `data.gate` unless it gets `--confirm --confirm-note "<answer>"`, at every autonomy level. `merge-approval` adds `data.paths`, the changed files that matched (`worker gate` reports `data.verdict: "approval-required"`). A cleared gate appends one line to `.rota/gate-audit.jsonl` (gitignored): gate, verb, target, time, the quoted answer and the autonomy level.

The skill's side:

- **Ask first, in an `AskUserQuestion` that is never auto-picked.** An earlier question counts when it names the action: `/rota-release` Step 6 asks about the notes *and* says yes pushes and publishes, so Steps 10 and 11 reuse its answer.
- **Pass the answer verbatim** in `--confirm-note`. Never invent one, and never pass `--confirm` without a human answer behind it.
- **On exit 4 with `blockedBy: "manual gate"`, ask and re-run.** Nothing changed on the refusal, so the re-run is safe.

### Merge approval in an unattended round

When nobody is at the prompt (an orchestrator driving herdr workers), `merge-approval` goes through the escalation channel instead of `AskUserQuestion`. `rota worker gate` and `rota ship pr-merge` take `--escalate`: on the refusal they post the approval request on the PR thread (or the slot's issue) with `rota round escalate send`, and `data.escalation.id` names it; a pending request on that thread is reused, never posted twice. Note the id against the slot, keep working other slots, and poll with `rota round escalate check`. Once it reports `answered`, re-run with `--approval <id>`. The verb itself decides whether the reply approves (first word `approve`, `approved`, `yes`, `lgtm`, or `ship it`) and audits the reply verbatim. Exit 4 `approval declined` means the human held the merge: surface `data.answer` and hold the slot, never retry. Exit 4 `approval pending` means `check` has not seen an answer yet. `rota ship merge` has no thread and keeps the `--confirm` path.

Call sites show the flags and the exit-4 handling; they don't restate the rule, which the verb now enforces.

## Skill-only gates: the callout holds the line

Closing upstream issues stays out of code (maintainer ruling, B1), and some gates have no verb to put the check in. These keep the inline callout immediately before the action, per the authoring convention *"Imperative rules in autonomy-aware steps must live inline at every dispatch point"* (see `references/authoring-conventions.md`, autonomy-rule-must-stay-inline). A reference cite cannot replace it.

The canonical callout shape (block-quote) is:

```
> **Manual gate — <one-line artifact name>.** <One sentence on what externally-visible state this creates.> This step is **always manual** — never auto-invoked, regardless of `autonomy.level`. <Optional: how prior approval feeds this step.>
```

Sites with multi-paragraph prose may use the *inline* form, a `**always manual** — never auto-invoked, regardless of \`autonomy.level\`` sentence embedded in the step's body. Both shapes are accepted; the block-quote is preferred for single-action steps. A prior step may collect the approval (pre-approved elsewhere); the gate at the action site then runs *because of* that approval.

| Gate | Skill | Step | Externally-visible state |
|------|-------|------|--------------------------|
| `decision-write` | `/rota-decide` | Step 5 (Confirmation) | Commits a hard boundary to `.rota/DECISIONS.md`; future implementation choices are constrained until the entry is amended. |
| `pr-open` | `/rota-ship` | Step 6a | Pushes the branch and creates a public PR or MR. |
| `issue-close` | `/rota-ship` | Step 6c (Direct-push close) | Posts a tracking comment and closes upstream issues after a direct merge. |
| `issue-close` | `/rota-release` | Step 13 | Closes upstream issues still open for shipped items. |

`/rota-ship` Step 3's *"Ship anyway"* option (in the CONCERNS-routing AskUserQuestion) is manual-shaped too; see `references/review-verdict-routing.md` for why it is never auto-picked. Acceptance of risk is the user's choice.

## Why not auto-invoke?

Auto mode only chains routing steps that move the work forward without committing to anything irreversible. A manual gate IS the irreversible commit: a public PR, a release tag, a `DECISIONS.md` entry that constrains future code. Auto-picking these would replace the user on questions that need human judgment about reputation, external coordination, or long-term project shape.

The skip-route is configuration. If a project wants concerns ignored on every ship, set `ship.review` to `false`; if it wants no human on merges, leave `ship.mergeApproval` at `none`.

## See also

- `references/authoring-conventions.md` rule *"Imperative rules in autonomy-aware steps must live inline at every dispatch point"*: why a skill-only callout cannot be replaced by a reference cite.
- `references/review-verdict-routing.md`: *"Ship anyway"* is a manual-shaped option inside the CONCERNS-routing question; it is never auto-picked.
