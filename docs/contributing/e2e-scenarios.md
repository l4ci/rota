# E2E scenarios

The `test.e2e` tier runs scripted stub-worker scenarios against the round machinery: slot watchers, worker death, slow output, usage limits and train bisect. Nothing in them calls a model, a forge or a real host, so they cost no credits and need no network.

## Run them

```
bash test/e2e/run.sh            # every scenario; exits non-zero if any fails
bash test/e2e/run.sh dead 05    # only scenarios whose file name contains an argument
rota test run e2e               # the same, through the configured tier
```

`test.e2e` in `.rota/config.json` is `bash test/e2e/run.sh`, so the merge gate and the merge train run it on the merged tree after `test.full`. Another check, such as #378's, can call that one command and read its exit code. Workers do not run it before a PR.

## How it works

- `test/fakes/stub_worker` plays one worker's side of a round on the fake tmux host (`test/fakes/tmux`). Cues: `done`, `blocked`, `limit`, `slow`, `silent`, `exit` and `commit`. Each prints what an agent would print on its pane and updates the fake host's state to match. The pane never ends in a prompt line, so a host `Send` sees no human draft.
- The fake tmux keeps one pane per window when `pane.<window>` exists, so several stubs can show different text.
- `test/e2e/lib.sh` builds the fixture (a git repo, a worktree and a tmux window per slot, `.rota/workers.json`) and gives scenarios `rota_j`, `stub`, `slot_field`, `jget` and `expect`.
- `test/e2e/run.sh` builds `rota` once (or uses `ROTA_BIN`), puts poison stand-ins for real `tmux`, `herdr`, `gh` and `glab` on the path, and runs each scenario in its own bash and sandbox.

A scenario asserts public surfaces only: verb exit codes, `--json` output and `.rota/workers.json`. It never reads internal packages.

## Add a scenario

1. Add `test/e2e/scenarios/NN_name.sh`. One file per case, named for the behavior.
2. Start with `e2e_fixture <slot>...`, steer the workers with `stub <cue> <slot>`, drive `rota_j <verb>` and check the result with `expect <got> <want> <what>`.
3. End each case with `e2e_pass "<what held>"`.
4. Run `bash test/e2e/run.sh NN`. To prove the scenario can fail, break the behavior it covers and watch it go red.

A new cue goes into `stub_worker` and its header comment. Keep a stub off `test/fakes/fake_tracker.py`; forge state belongs to the existing forge fakes.
