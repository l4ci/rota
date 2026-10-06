# Iron Law stop

Read from `SKILL.md` when Step 2 or Step 6 routes here.

Three failed committed fixes: no further attempts. Print `rota debug counter summary` and suggest `/rota-pause` or reopening from a different angle. Further attempts are refused until a human runs `rota debug reset <ID> --reason "<why>"`, a manual gate (`references/manual-gates.md`): only after an `AskUserQuestion` yes, passing `--confirm --confirm-note "<their answer>"`, never on your own. Leave the branch and status entry; do not `rota item complete`; do not dispatch any continuation skill.
