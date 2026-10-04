# Debug escalate — fresh-context handoff on repeated hypothesis failures

Shared reference for `/rota-debug` Step 7.5: when the cycle-counter trips at 3 refuted hypotheses, the orchestrator's context carries enough refuted-hypothesis weight that dispatching another agent on the same transcript yields diminishing returns. This reference holds the *for-next-agent* brief template and the surrounding choreography.

## When the escalation fires

Fires only when Step 6's cycle-counter check trips (`counter >= 3`, single-hypothesis mode). The orchestrator's context now carries 2+ refuted hypotheses; the marginal value of dispatching another agent on the same context is low. Hand off to a fresh-context subagent instead.

## For-next-agent brief

Synthesize a brief — every refuted hypothesis goes in, every file inspected without finding the root cause goes in, plus a one-line orchestrator read on why the loop did not converge:

```
Bug [B##]: <title>

**Symptom:**
<reproducer output — current as of last attempt>

**Reproducer (self-rerunnable):**
<exact command or test name from Step 5>

**Hypotheses we tried and ruled out:**
1. <claim 1> — verified at <file:line>, refuted by <evidence>
2. <claim 2> — verified at <file:line>, refuted by <evidence>

**Files inspected without finding root cause:**
- <path>:<line range> — <one-line note on what we saw>

**Suspected blockers (gut feel):**
<one-line orchestrator read — e.g. "the symptom isn't where we're looking", "an interaction between subsystems we haven't traced">

Read the code organically with a fresh perspective. Do not anchor to our prior hypotheses unless evidence forces you back to them. Return: a single best hypothesis with causal chain, file:line evidence, and a verification probe.
```

## Dispatch mechanics

Dispatch a fresh subagent via `Agent` (`subagent_type: general-purpose`, model: `models.worker` from `.rota/config.json`) with the brief above and nothing else. The subagent has no transcript of the failed cycles — that is the point.

When it returns, reset the cycle counter, carry its hypothesis into **Step 7** for verification, and continue the normal flow.

## Hard stop after a failed fresh-context attempt

If the fresh hypothesis also fails verification, do **not** loop a second fresh-context attempt — surface to the user instead:

> *"3 hypothesis rounds + 1 fresh-context attempt all failed on [B##]. Bug needs human triage — share more context, sharpen the reproducer, or pair on it."*

## Cited by

- `/rota-debug` Step 7.5 — *Escalate on Repeated Hypothesis Failures*
