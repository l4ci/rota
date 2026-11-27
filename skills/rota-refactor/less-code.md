# Less-code lens

Loaded by Step 2 of `SKILL.md`. The explorer applies it in the same pass as the depth heuristics, under the same 8 to 12 finding cap. There is no flag and no second cap. A less-code finding is a normal finding: it needs a concrete case and the Step 4 checks.

Look for code to remove or shrink, not only seams to deepen. Each finding gets one kind. The test after each kind is the bar: a finding that fails it is dropped.

## Kinds

- **Delete**: dead code, unused flags, options and config. Test: nothing outside the code itself reads it, after the unused-code search in Step 4.
- **Reuse**: an existing helper already does this. Test: name the helper and show it covers every case the copy handles.
- **Standard library**: hand-written code that the Go standard library or a dependency already in `go.mod` covers. Test: name the call that replaces it, with the same behaviour on the inputs the code sees.
- **Collapse**: an interface or wrapper with one implementation and no test seam that needs it. Test: no test or caller swaps the implementation, and deleting the layer moves no complexity to the callers.
- **Merge**: two near-copies. Test: diff them and list every difference; each difference is either dropped or kept as a parameter.
- **Split by job**: one module doing two jobs, cut along the jobs. Test: the two jobs change for different reasons. Never split because a file is long.

## Net line estimate

Every finding carries an estimated net line change from its cited spans: `~-N` when code goes away, `~+N` when it grows. It is the explorer's estimate. No tool computes it.

## Impact tier

The tier says what skipping the finding costs. Strength stays a separate grade of how sure the finding is.

- **must**: skipping it leaves a wrong result, a hazard or a block for other work.
- **should**: skipping it keeps real cost: drift between copies, slower change, harder tests.
- **nice**: skipping it costs only tidiness.

Each finding adds one line: `If skipped: <what happens>`.

## Small findings

Small findings are batched into one issue per package directory. The rule is in `SKILL.md` Step 3.
