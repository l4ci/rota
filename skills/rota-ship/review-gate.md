# Review gate routing (Step 3, and the table Steps 3.5 and 3.75 reuse)

Loaded by `SKILL.md` Step 3 when the verdict does not route `continue`, or when the review depth is `none`.

Depth `none` (`ship.review: none`, legacy `false`, or a policy label mapped to `none`) skips Step 3 and `REVIEW_CHOICE` stays unset. A `light` review records only the Standards verdict; route it the same way.

The rubric `rota-review` carries is `references/silent-failure-hunter.md`; `SILENT-FAIL` flags arrive as CONCERNS. The gate is a loop: review, fix via `/rota-work` on a stop, rerun, and go on only once the verdict routes `continue`, `surface` or an accepted `ask`. Act on `data.next`:

| `data.next` | Meaning | Do |
|---|---|---|
| `continue` | PASS | Go on silently. |
| `ask` | CONCERNS | Surface each concern, then `AskUserQuestion` with the options in `references/review-verdict-routing.md`: Address via `/rota-work` (Recommended) / Ship anyway / Stop. |
| `surface` | Advisory gate (QA under `qa.gate: advisory`, any QA `INFRA-FAIL`, advisory second opinion) | Surface the findings, continue. A missing dev server or credentials never blocks a ship. |
| `stop` | FAIL | Stop. Surface the findings; the user fixes via `/rota-work` or `/rota-debug` and reruns `/rota-ship`. |

Label surfaced concerns by producer (carrier labels in `references/review-verdict-routing.md`): "Second-opinion concerns", "QA concerns".

Remember a CONCERNS answer as `REVIEW_CHOICE` (`address`, `ship-anyway`, `stop`); Steps 3.5, 3.75 and 9 read it. A review FAIL also makes `rota ship pr` and `rota ship merge` refuse (exit 4, `data.blockedBy: "verdict"`), but stop here.

Step 9 report: if `REVIEW_CHOICE == ship-anyway`, append a one-line list of the concerns the user proceeded through.
