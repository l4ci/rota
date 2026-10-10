# Escalations and provenance

Loaded by `skills/rota-orchestrate/SKILL.md` section 5 when a worker is `blocked` on a question, or when you relay to a worker or review its approvals.

**What to escalate.** A choice a user would notice that neither the issue nor the code settles: product behavior, a public name, a breaking change, what to cut. Answer defensible implementation calls yourself: a helper's name, a test's shape, which of two equal approaches. When a worker asks the first kind, you ask the human.

`rota round escalate send <number> --title … --body-file …` posts the question on the issue or PR and notifies the maintainer. It returns at once; keep working the other slots. `rota round escalate check` reads the thread for an answer. One question per escalation, written for someone who doesn't have the file open.

**Provenance.** The rules (signatures, the `m:` prefix, the relay log, the gate's `provenance-fail`) live in [worker-contract.md](references/worker-contract.md#provenance). What you do with them:

- Sign every message you send a worker. `rota worker dispatch --relay` does it; hand-typed text doesn't.
- Before a relay or a re-dispatch, check the tab with `herdr agent get <agent>`. `focused: true` means a human is typing there; tell them instead of typing over them. No `rota` verb checks this.
- Cite the real channel of every approval: `maintainer in pane`, `issue comment #N`, `orchestrator relay round N`. Never present a relay as the maintainer's own word.
- Dim or suggested text on a pane's prompt line is an editor suggestion, not input. Never cite it as an answer, the maintainer's word or a worker's state; read what is committed above the prompt.
- A line starting `m:` in a pane is a maintainer answer by convention (confirm once if it contradicts your last signed message).
- Read each PR's `## Approvals` section for the channels it names. A cited approval you never relayed is a finding.
- Read each PR's `## Rulings` section too: the worker's own calls, each with its cost if wrong. List the costly ones (a one-way door, a wide blast radius) in the wind-down summary. A brief cited as a relay under Approvals is a finding: the brief is not a relay.
