# Flaky bugs

Read from `SKILL.md` Step 3 when the bug is intermittent.

Run the reproducer N times (start at 20), record `fails/N`, and raise the rate (parallel runs, tighter timing, fixed seed, injected load) until one run is informative. Step 6 re-runs at the same N.
