# Slow or regressed behaviour

Read from `SKILL.md` Step 3 when the bug is a slowdown or regression.

The red signal is a measurement: record a baseline (the last good commit, or a median of N runs), set a threshold, and assert against it (`elapsed <= baseline * 1.2`). Measure before the fix, then again after, on the same input. Prefer a debugger or profiler to log spam: one profile names the hot path, and print probes shift timing.
