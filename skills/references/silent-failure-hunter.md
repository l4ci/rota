# Silent failure hunter

Shared rubric invoked by `/rota-review` Step 7 (the reviewer brief) and bundled into the `/rota-ship` Step 3 review pass. Detects when work reports *complete* but didn't actually move the system — the most insidious failure mode because everything looks fine from the outside.

Not a separate dispatch step or helper — it lives entirely as an additional rubric item that the existing reviewer evaluates alongside intent match, convention compliance, obvious quality, and stale scaffolding.

## What "silent failure" means

A silent failure leaves no surface error. The build is green, the test reports `OK`, the smoke section prints `PASS` — but the change didn't actually do the thing it claimed to do. The recurring shapes:

- **Tests pass but feature broken.** The new assertion targets the wrong path, or doesn't exercise the changed branch. Adding `assert True` would also pass.
- **Build succeeds but artifact missing.** Compiler exits 0; the binary, generated file, or deployable bundle isn't where downstream expects to find it.
- **Smoke OK but code path never exercised.** The asserted command runs; the new branch is guarded behind a flag the smoke fixture didn't flip.
- **Helper output asserted on the wrong shape.** A helper emits JSON; the test greps stdout for a substring that happens to occur in *every* run.
- **Idempotent helper called, state didn't move.** The no-op path runs (already-stamped, already-resolved, already-archived) and nothing visible distinguishes that from real work.
- **Mock returned the expected value before the code ran.** The test asserts on a fixture, not on a real call result.
- **CI runs in isolation but not in the real pipeline.** Local-only env vars, a missing service dependency, or a different working directory in CI keeps the new path dark.
- **A retrying assertion outwaits the thing it denies.** Any matcher that polls until a deadline cannot assert the *absence* of something transient: it waits out the toast, the spinner, the temp file, the lock, and reports success on the state that follows. The test then passes with the regression it exists to catch fully present. Compare the matcher's retry window against the transient's own lifetime — if the window is longer, the assertion cannot fail. The fix is an instantaneous read sampled inside the transient's lifetime, plus a positive arrival proof so a broken producer can't satisfy the absence by never producing. Note the asymmetry: asserting a transient *does* go away is a positive wait and wants the generous retry.
- **An empty or absent value coerces into a passing one.** A threshold applied to text that might be blank (`Number("")` is `0`, not `NaN`), a count over a collection that failed to load, a comparison against a default-initialized field — the assertion is satisfied by the *unpopulated* state, before the thing under test ever happened. Assert the shape (`/^\d+$/`, non-empty, expected key present) before comparing the value.
- **A vacuous wait, hardened with a cleverer predicate over the same state, is still vacuous.** Tightening `x !== undefined` into a multi-field conjunction usually describes the wrong state just as well as the right one. Take the barrier from a different layer than the one you're unsure of — a URL, a server-assigned id, a log line, an exit code — not from a sharper predicate over the same object.

## Rubric — apply to every verification claim in the diff

For each test, smoke section, or assertion the diff adds or relies on, answer the four questions below. If any answer is *no* or *unclear*, mark the claim `SILENT-FAIL` with file:line evidence and a one-sentence explanation.

1. **What does this verify, concretely?** Name the specific behavior the change introduces, not the general feature. ("Asserts `rota ship merge` prints the merge hash on stdout" beats "tests the merge verb.")
2. **Is the asserted-on thing the same thing the real consumer reads?** Output shape, file path, exit code, env var, JSON key — all must match where the rest of the system actually looks for that signal.
3. **Was the new code path exercised?** Trace: did the fixture set the flag, hit the route, or pass the input that triggers the new branch the diff added? If the new branch lives behind an `if config.featureX:` check, did the test enable `featureX`?
4. **If you deleted the new code, would the assertion still pass?** If yes, the assertion is not actually testing the change. Flag.

**Before flagging, check what the assertion actually reads.** The shapes above are recognized by pattern, and a pattern match is a candidate, not a verdict — two assertions using the same matcher can differ entirely in whether they're defective, because they read different state. A matcher reading a property the transient *restores* when it self-hides is broken; the same matcher reading a property the transient leaves behind is fine. Flagging on the pattern alone churns correct assertions into weaker ones, which is worse than the bug: the rewrite loses the regression the original caught, and it passes, so nothing says so. Trace what the assertion reads, and prefer a flag that names one concrete failing scenario over a flag that names a suspicious idiom.

## Output contract

For each `SILENT-FAIL` flag, surface one bullet under a `### Silent failure check` section in the reviewer's verdict block:

```
### Silent failure check
- test/sections/08_ship.sh:42 — asserts `grep "merged" output` but `rota ship merge` prints "Merged" with a capital M; assertion would pass before the change too.
- references/foo.md:17 — claims "auto mode stops cleanly on empty backlog" but no smoke section flips autonomy.level to "auto" with an empty backlog.
```

`SILENT-FAIL` flags route as `CONCERNS` in the verdict — they don't break the build by themselves, but the user sees them before merging. A diff that gets `PASS` on intent + convention + quality but has `SILENT-FAIL` flags becomes `CONCERNS`. A diff that's already at `FAIL` stays `FAIL` (regression beats silence).

## Invocation contract

`/rota-review` Step 7 carries the rubric as the silent-failure check item ("Silent failure check — apply the four-question rubric from `references/silent-failure-hunter.md` to every verification claim in the diff"). The reviewer prompt links to this file so the full text is one Read away when needed. It is item 5 of the reviewer rubric.

`/rota-ship` Step 3 inherits the check automatically through `/rota-review` — no separate dispatch. The point of bundling is that the reviewer already has the diff and the intent loaded; running a second adversarial pass for silent-failure-only would double the cost without changing the verdict shape.

`/rota-ship` does **not** call this rubric when `ship.review: false` — opting out of review opts out of the hunter too. The rubric is a review-quality augmentation, not a separate gate.

## See also

- `references/review-verdict-routing.md` — how `CONCERNS` (including silent-failure flags) routes through `/rota-ship` per `autonomy.level`.
- `rota-review/SKILL.md` Step 7 — the reviewer brief that carries the rubric inline.
