# Post-cycle trigger gate

Used by `/rota-work` Step 13 (the one-line learn nudge), `/rota-ship` Step 8.5 (Learn) + Step 8.6 (Docs After-Work) + Step D-A1 (Docs Mode Trigger Gate), and `/rota-qa` after-work (via `qa.afterWork`). Both the trigger condition AND the nudge-or-dispatch choreography live here as the single source of truth; each call site cites this file and supplies only its own parameters (config flag, trigger override, nudge line, target skill, brief contents).

## The condition

A post-cycle nudge or auto-dispatch fires when **at least one** of:

- **2+ items resolved** in the cycle (counted as distinct closed items across the cycle: bracketed IDs such as `[B07]` in its commit messages, or issue closures, i.e. `Closes #N` / `Fixes #N` / `Resolves #N` in its commits or the PR body; `rota ship body` and `rota ship pr --items` emit the `Closes #N` lines).
- **≥5 files touched** in the cycle (counted by the cycle's diff against its base — for `/rota-ship`, use the scope JSON's `touchedFiles` field).
- A **hard bug** that took 2 or more debug cycles to root-cause (signaled by 2+ `/rota-debug` invocations within the same `/rota-work` session, or by the bug carrying a `Detail:` pointer to a `.rota/bugs/<id>.md` file with 2 or more hypothesis entries).

## When the gate does NOT fire

- Single-item fixes — one item resolved (one bracketed ID or one `Closes #N`), fewer than 5 files touched, no debug-cycle escalation.
- Pure mechanical changes — bulk rename, dependency bump, formatting sweep, generated-file refresh. The diff size doesn't capture intent; an author can use judgment to skip the nudge when there's clearly nothing durable to learn or document.
- The same nudge has already fired in the current session for the current trigger (don't repeat).

## Why a gate at all

The downstream nudges (Learn, Docs After-Work) cost user attention or token budget. Firing them on every single-item fix trains the user to dismiss the nudge, which loses the signal when something genuinely worth capturing happens. The thresholds are calibrated so that a cycle big enough to trip any one of them is plausibly worth a post-cycle pass.

## The choreography

Every post-cycle nudge step runs the same sequence. The call site supplies the **bolded parameters**; this reference supplies the sequence.

1. **Config flag** *(only when the site names one, e.g. `docs.afterWork`)* — read it from `.rota/config.json` (default `false`). `false` → skip the step entirely. The flag is opt-in: users enable it via `rota config set` or the owning skill's first-run / manual-toggle flow.
2. **Trigger** — apply *The condition* above, subject to *When the gate does NOT fire*. When the site names a **trigger override** (a site's own counts), apply that instead of the default condition; the don't-repeat exclusion applies to every trigger, override or not. Not triggered → skip silently.
3. **Branch on `autonomy.level`:**
   - `"off"` → emit the site's **nudge line**, placed where the site says (standalone message, or appended to the cycle's final report).
   - `"auto"` → dispatch the site's **target skill** via the `Skill` tool immediately — no prompt, no confirmation, no "want me to" question. Each site restates this imperative inline beside its target, per the inline-at-dispatch-point rule in `references/authoring-conventions.md`. Pass a **brief** naming the cycle's resolved item IDs and touched files (plus anything else the site names) so the dispatched skill has the right context.

### Inline variant — `/rota-ship` Step 8.6

One site deliberately diverges from step 3 above: `/rota-ship` Step 8.6 does not branch on `autonomy.level` and does not dispatch a skill. When its flag (`docs.afterWork`) is on and the trigger fires, it inline-runs Docs Mode's after-work flow (Steps D-A1 through D-A6 in `rota-ship/SKILL.md`), passing the resolved item IDs and touched files from the cycle's scope JSON as context; the after-work flow's own approval gate (Step D-A5) supplies the user checkpoint the nudge arm would otherwise provide. `/rota-work` Step 13 only nudges and never dispatches.

### Manual entry

Manual invocation of a gated flow (e.g. the user running `/rota-ship --docs` by hand) bypasses this gate — the invocation is itself the trigger. The owning skill states the rule at its entry step (`/rota-ship` Step D1).

## See also

- `references/authoring-conventions.md` — the inline-at-dispatch-point rule: each site keeps the dispatch imperative and target inline; this file owns the shared sequence around it.
- `references/manual-gates.md` — for the orthogonal *"always manual, never auto-invoked"* gates (`/rota-decide`, `/rota-ship` Step 6a, `/rota-release` Steps 6, 10, 11 and 13). Those are separate from this trigger — they fire even when the trigger does, and never auto-pick.
