## A7: workers, hosts, accounts

### rota worker pool init
rota worker pool init --slots <n> [--base <branch>] [--session <name>]
repo: none
data: {"session": string, "base": string, "slots": []slot, "changed": bool}
exit: 2 when --slots is missing or not a positive integer; 3 when the base branch cannot be resolved or does not exist, or a registered slot's worktree belongs to another repository; 5 when git fails
old: hv-worker-pool init --slots <n> [--base <branch>] [--session <name>]
shim: runs the old helper, then reads `.rota/workers.json` for `data`; `changed` is false when the registry file is byte-identical before and after.
note: --base defaults to the current branch and --session to `rota`, as the old helper does.
note: `warnings` gets one entry when `.worktrees/` is not gitignored, and one per registered slot outside `.worktrees/` that is kept in place (old `warning:` and `note:` stderr lines).

### rota worker pool list
rota worker pool list
repo: none
data: {"session"?: string, "round"?: number, "slots": []slot}
exit: implied only
old: hv-worker-pool list --json
shim: passes the registry through; `{"session":null,"slots":[]}` (no registry) becomes `{"slots": []}`. Text mode prints one line per slot: `name state branch worktree`.

### rota worker pool reap
rota worker pool reap (<slot>... | --all)
repo: none
data: {"reaped": []string, "changed": bool}
exit: 2 when neither a slot nor --all is given, or both; 5 when git fails
old: hv-worker-pool reap --slot <slot>  (once per slot)  |  hv-worker-pool reap --all
shim: loops over the slots, one old call each; `reaped` lists the slots that were in the registry before the call, `changed` is true when it is non-empty. An unknown slot is a no-op, as before.
note: slots are variadic positionals instead of the old `--slot <name>`.

### rota worker reset
rota worker reset <slot> [--task <id>] [--check-only]
repo: none
data: {"slot": string, "clean": bool, "retained": bool, "branch"?: string, "base"?: string, "sha"?: string, "dirty"?: []string, "unmerged"?: []string, "changed": bool}
exit: 1 with --check-only when the slot holds work (`clean: false`); 3 when the pool, the slot, its worktree or its base is missing; 4 without --check-only when the slot holds work (uncommitted changes or commits not on the slot's base) and the reset is refused (`blockedBy: "slot holds work"`); 5 when git cannot cut the new branch
old: hv-worker-reset --slot <slot> [--task <id>] [--check-only]
shim: `clean` is true when the old helper exits 0 without a `retry:` line. `retained` is true on the `retry: <slot> keeps its work ...` line (same task on its own branch; exit 0, nothing reset). `branch`, `base`, `sha` come from `reset: <slot> on <branch> at <sha7>` and are absent with --check-only and on retention. `dirty` and `unmerged` come from the `REFUSED` stderr block, and the failure `data` carries them on exit 1 and on exit 4 (what blocked the reset). `changed` is true only after a real reset. Old exit 3 is split by its message: work held becomes 1 or 4, the rest stay 3 or become 5.
note: `--check-only` makes this a check verb (1); without it the verb mutates and declines with 4 (rule 7).
note: each `dirty` entry is one `git status --porcelain` line (`?? scratch.txt`); each `unmerged` entry is `<sha7> <subject>` for a commit not on the slot's base.

### rota worker dispatch
rota worker dispatch <slot> --body-file <path|-> [--task <id>] [--relay] [--round <n>] [--boot-timeout <s>] [--kind <claude|codex>]
repo: none
data: {"slot": string, "handle": string, "task"?: string, "round"?: number, "relay": bool, "kind": string, "changed": bool}
exit: 2 when --body-file is missing, --round is not a number, --kind is not a kind, or the launch command (work.workerCommand, or work.codexCommand for codex) cannot be parsed; 3 when the pool, the slot, its worktree or the body file is missing, or --relay finds no running session; 4 when the reset guard finds the slot holding work (`blockedBy: "reset guard"`), the launch command carries a resume flag (-c, -r, --continue, --resume, or codex's `resume`/`fork` subcommands; `blockedBy: "resume flag"`), or `codex --help` lacks a flag the launch line uses (`blockedBy: "codex flags"`, E1); 5 when the host is unavailable or not usable (herdr outside a pane, a launch command whose binary is not the kind's under herdr, a codex worker under tmux, codex not on PATH, the slot's Codex home not logged in, an unrecognised startup dialog), the old session will not close, a dialog refused input (inspect the pane before resending), or the slot's prompt line holds a human draft (nothing was typed; resend once it is submitted or cleared); 6 when the brief was never submitted (safe to resend)
old: hv-worker-dispatch --slot <slot> --brief-file <path> [--task <id>] [--relay] [--round <n>] [--boot-timeout <s>]
shim: `--body-file` is passed as the old `--brief-file`; `-` is spooled to a temp file first. `data` is built from the `dispatched: <slot> (<handle>)` line plus the flags. `changed` is always true on success. Old exit 3 is split by its message (slot holds work becomes 4, host failure becomes 5, missing pool/slot/session stays 3); old exit 4 (brief never submitted) becomes 6 and old exit 5 (dialog open) becomes 5.
note: `<slot>` replaces the old `--slot`. Fresh-session dispatch is the default; `--relay` injects into the running session and appends to `relays`.
note: (#391) before typing, both hosts read the prompt line and wait about 4s (3 reads, 2s apart) for a human draft to clear; if text that is not rota's own unsent brief or a placeholder is still there, dispatch exits 5 (`unavailable`, same as a dialog) and types nothing. A refused relay is not logged in `relays`. The read keeps styling (`herdr agent read --format ansi`, `tmux capture-pane -e`) and drops faint (SGR 2) and gray-foreground runs first, so Claude Code's dim ghost suggestion is not a draft; if the styled read fails, plain text is used.
note: "never submitted" (safe to resend) and "dialog open" (inspect first) get distinct `error.code` values. The conventions fix `error.code` to the exit-table name, so per-verb codes are not allowed, and the verb uses two exits instead: `retry` (6) for a brief that was never submitted, `unavailable` (5) for an open dialog. The message and hint still say which.
note: `--kind` (E1, #68) defaults to the slot's recorded `kind`, else `claude`; a relay ignores it. A codex dispatch is specified under "E: Codex workers". hv-codex-verify is not absorbed: #158 retired it.

### rota worker prompt-check
rota worker prompt-check --key <path>
repo: none
data: none; the verb speaks Codex's hook protocol, not the envelope, and `--json` is refused
exit: 0 and no output when the prompt on stdin (Codex's hook JSON, field `prompt`) is signed with the key or starts with `m:`; 2 with the reason on stderr for everything else, including a missing `--key`, an unreadable key, input that is not JSON, and `--json` (fail closed)
old: none (new in #3)
note: `rota worker dispatch` starts a codex worker with this verb as its `UserPromptSubmit` hook and signs every payload it sends with a trailing `--- ROTA-SIG <hmac> ---` line. The key lives in the slot's Codex home (`rota-prompt.key`) and rotates on every task dispatch. The MAC ignores white space, so a pane that rewraps text still verifies.
note: a codex task dispatch exits 5 when `work.codexCommand` lacks `--dangerously-bypass-hook-trust` (Codex skips the hook without it); a codex relay exits 5 when the key file is gone.

### rota worker poll
rota worker poll [<slot>] [--settle <seconds>] [--lines <n>]
repo: none
data: {"slots": [{"name": string, "state": string, "evidence": string}], "changed": bool}
exit: 3 when the named slot is not in the pool, including when there is no registry; 5 when the host is unavailable
old: hv-worker-poll [--slot <slot>] [--settle <seconds>] [--lines <n>]
shim: wraps the old array as `slots` and lowercases `state`. `changed` is true when `.rota/workers.json` differs before and after (slot state or PR recorded). A poll with no slot and no registry gives `{"slots": [], "changed": false}`. Old exit 3 (host failure) becomes 5.
env: `ROTA_TEST_POLL_FIXTURE=<file>` classifies the file as a static pane, no host, writes nothing (old `--fixture`). `ROTA_TEST_POLL_STATUS=<idle|working|blocked|done|unknown|gone>` sets the host status in fixture mode (old `--status`). Neither is part of the CLI. In fixture mode the slot name is `<slot>` or `fixture`, and a missing fixture file exits 2. The shim passes them as `--fixture "$ROTA_TEST_POLL_FIXTURE" [--status "$ROTA_TEST_POLL_STATUS"]`.
note: `state` is one of `busy`, `idle`, `blocked`, `done`, `dead`, `limited`, `needs-permission`, `unknown`, lowercased to match what the helper already stores in the registry.

### rota worker done
rota worker done <slot> [--base <ref>]
repo: none
data: {"slot": string, "item": string, "head"?: string, "proofSkipped"?: bool, "missing"?: []string, "changed": bool}
exit: 4 when the slot's item has no PASS proof row at the slot branch's current HEAD for `test.fast` (`data.missing` lists the commands without one, `changed` false, the hint names `rota proof record`); 3 when the pool, slot or its branch is missing or the slot holds no item; 2 on a bad invocation
note: the step before the PR. A row proves `test.fast` when its result is PASS, its sha is an abbreviation of the branch HEAD (7 or more characters) and its check is either `rota test run fast` or, for each `test.fast` command, that command as `rota proof record` writes it (`{files}` expanded). A FAIL row or a PASS row at an older sha does not count. When `test.fast` is unset the proof half is skipped with a warning and `data.proofSkipped` is true; the verb never refuses on that half. On success the slot's state is recorded as `done` (`changed` is false when it already was); `rota worker poll` records the same state from the pane, so the two agree.

### rota worker gate
rota worker gate <slot|PR> --base <branch> [--check-only] [--no-verify] [--confirm --confirm-note <answer> | --approval <escalation> | --escalate]
repo: none
data: {"slot": string, "verdict": string, "base": string, "branch"?: string, "sha"?: string, "pr"?: string, "verified": []string, "verifySkipped": bool, "changed": bool, "excluded"?: []ledgerEntry, "expired"?: []ledgerEntry, "bounces"?: number, "parked"?: bool}; on the B1 refusal the same object with `verdict` `approval-required` plus `blockedBy`, `gate` and `paths` (manual-gate shape), and with `--escalate` an `escalation` (C5)
exit: 1 for every verdict except `fresh` and `pass` (`stale`, `pr-mismatch`, `provenance-fail`, `not-merged`, `not-on-base`, `verify-failed`, `merged-remotely`, `merge-failed`, `check-broke`, `base-moved`, `ci-not-run`, `verify-timeout`, `ci-config-changed`); 3 when the pool, slot, base branch or worker branch is missing, or (without --check-only) the base branch is not checked out; 2 when a confirmation flag is given without the other, or `ship.mergeApproval` is invalid; 4 when `ship.mergeApproval` covers the merge and the `merge-approval` gate is not cleared (B1: checked after provenance and before the merge, never under --check-only; `data.verdict` is `approval-required`, `changed` false); with `--approval`, 2, 3 or 4 as "C5: merge policy and approval requests" lists, and two of `--approval`, `--escalate`, `--confirm` together is 2
old: hv-worker-gate --slot <slot> --base <branch> [--check-only] [--no-verify]
shim: `verdict` comes from the stdout/stderr token: `FRESH` is `fresh` (only with --check-only), `GATE-PASS` is `pass`, `STALE` is `stale`, `PROVENANCE-FAIL` is `provenance-fail`, `NOT-MERGED` is `not-merged`, `NOT-ON-BASE` is `not-on-base`, `MERGED-REMOTELY` is `merged-remotely`, `CHECK-BROKE` is `check-broke`, a `verify FAILED` line is `verify-failed`, an `error: ... merge failed` line is `merge-failed`, and an `error: PR ... not open|targets|headed by|head is` line is `pr-mismatch`. `verified` lists the `verify ok:` commands. `verifySkipped` is true on `NO-VERIFY`. `sha` is the `<sha7>` of `MERGED`/`GATE-PASS`. `changed` is true once the merge landed (pass, `verify-failed`, `merged-remotely`). Old exit 3 is split by its message (a verdict becomes 1, missing refs stay 3); old 4, 5 and 6 become 1.
note: failure `data` on exit 1 carries the verdict, and `changed: true` once the merge landed (`verify-failed`, `merged-remotely`), so callers read `data.changed`, not the exit code (conventions).
note: (#29) the argument is a slot name, or a PR as `#N`, `N` or its URL. A PR resolves to the record on the round's review list (`prs` in `.rota/workers.json`, see `round assign`), else to a slot whose `pr` has that number, and the record stands in for the slot (branch, PR, base, relays). Nothing matching is 3. A slot that records no PR while a review record came from it is refused with 2 and a hint naming `rota worker gate <N>`, so a habitual `gate <slot>` never merges that slot's next branch locally. A `pass` drops the record from the list; any other verdict keeps it (`round reconcile` reports a record whose PR is merged as `pr-stale` and drops it on `--apply`).
note: (#31) a branch behind the base is not refused for that alone. The gate merges it itself, then verifies the merged tree, when `git merge-tree` finds no conflict and the two sides changed no file in common (`git diff --name-only` from the merge base on each side, minus `round.sharedPaths` globs); a `STALE-MERGE` line on stderr says so, and `--check-only` reports `fresh`. It stays `stale` with a reason in the message when the merge conflicts, when both sides changed the same file (a textually clean merge can still break, and the break would land on the base before re-verify sees it), or when the branch's work is already on the base. A `git merge-tree` or `git diff` that itself fails is `check-broke`.
note: (#31) bounces are counted per item in `bounces` of `.rota/workers.json` (issue to count), by a real gate run only. A `stale` or `provenance-fail` verdict counts one and puts `bounces` in `data`. When the count reaches `round.maxBounces` (default 3, `0` is off) the gate parks the item as `round transfer --to human` does (slot freed, `needs-human` label, a comment naming the count and the last refusal, the PR left open) and sets `parked: true`; the verdict stays on exit 1. Parking needs the round lease and the tracker; if it fails, the failure goes to stderr as a `BOUNCE-COUNT` line and `parked` is false. A `pass` or a park clears the count. `round.maxBounces` is validated like `round.stallMinutes`: an integer of 0 or more, else the park step reports the config error.
note: (#387) the gate reads `.rota/test-ledger.json` from the base before the merge, like `test.full`, so a branch cannot excuse its own failure. A `test.full` or `test.e2e` command that fails counts as verified when every test it names (go test `--- FAIL: <name>` lines) has an unexpired ledger entry; those entries are listed in `data.excluded` (`ledgerEntry` is `{"test": string, "owner": string, "receipt": string, "expires": string}`; the key is absent when none). A failing test with no entry, an expired entry, or a failure the output does not name (build or setup failure, panic, timeout), or output that does not end in go test's `FAIL` line (a later step of the command failed), still fails the verify. An expired entry, whether or not its test fails now, fails a gate that verifies (not `--check-only`, not `--no-verify`) before anything merges: `verify-failed`, `changed` false, the message and `data.expired` naming each test, owner and receipt. A malformed ledger is exit 2 naming the entry. The ledger applies to the local verify, not to `test.fullWhere` `ci`, where CI decides. See `rota test ledger check`.
note: `merged-remotely` must never be retried (the PR is on origin but the local base diverged), which is why it is a verdict on exit 1 and not 6.
note: `merge-failed` covers both a forge CLI merge that fails and a local `git merge` that conflicts when no PR is recorded.
note: `test.fullWhere` `ci` (default `local`; any other value exits 70) moves the gate's verify before the merge: the merge result (base plus PR head, `--no-ff`) is built in a scratch worktree, pushed to `origin` as `rota/ci/<slot>`, and its forge checks polled (GitHub check suites, check runs and commit statuses, GitLab the jobs of the newest pipeline per ref by job name, plus the pipeline as `pipeline #<id> (<ref>)`); `test.ciChecks` (array, default `[]`, read from the base's config) lists the required check names, and an empty list is refused up front with exit 70 (hint names `test.ciChecks`), never a local fallback. Green needs every listed check finished with success and no check on the commit failed, held over two polls in a row. Unlisted pending checks do not block; an unlisted failure is red. A listed check that finished skipped or neutral counts as failed, and same-name checks must all succeed. A GitLab job failed with `allow_failure: true`, or `manual` and not run, counts as skipped. A merge that changes `.github/workflows/`, `.github/actions/`, `.gitlab-ci.yml` or `.gitlab/ci/` is `ci-config-changed` before any push (the pushed tree's CI definition would choose its own verification). `test.full` is not run locally. Verdicts: `pass`; `verify-failed` on the first red check (nothing landed, `changed` false, so the slot goes back); `verify-timeout` when a listed check is still pending or missing after `test.ciTimeoutMinutes` (default 60, 1-1440; names them); `ci-not-run` when a listed check never appears, decided once the start window (5 minutes) has passed and no check on the commit is pending (the project's CI must run on `rota/ci/**` pushes; the message names the missing checks). A missing `origin` is `check-broke`. Before merging, the base is re-checked (`base-moved`, nothing landed); after merging, the landed tree must equal the CI-verified tree (else `base-moved`). The `rota/ci/` branch is deleted after each run. `--no-verify` skips CI too. Env, in seconds: `ROTA_CI_POLL` (20), `ROTA_CI_START_WAIT` (300), `ROTA_CI_TIMEOUT` (overrides the config).
note: `sha` is always 7 characters, whatever `core.abbrev` says. It is present on `fresh` (the tip of the checked worker branch, `origin/<branch>` when a PR is recorded), on `pass` and `verify-failed` (the merge commit), and on `merged-remotely` (the merge commit on `origin/<base>`, or the verified head for a fast-forward merge).

### rota worker train
rota worker train <slot|PR>... --base <branch> [--land-green] [--confirm --confirm-note <answer> | --approval <escalation> | --escalate]
repo: none
data: {"base": string, "verdict": string, "members": [{"target": string, "branch": string, "pr"?: string, "landed": bool, "culprit": bool}], "culprit"?: string, "landed": []string, "verified": []string, "changed": bool, "excluded"?: []ledgerEntry, "expired"?: []ledgerEntry, "sha"?: string}; on the B1 refusal the same object with `verdict` `approval-required` plus `blockedBy`, `gate` and `paths`, and with `--escalate` an `escalation`
exit: 0 only when every member landed (`verdict` `pass`); 1 for every other verdict: a member's own check verdict (`stale`, `pr-mismatch`, `provenance-fail`, `check-broke`), `merge-failed`, `verify-failed`, `not-merged`, `not-on-base`, `merged-remotely`, `base-moved`, `ci-not-run`, `verify-timeout`, `ci-config-changed`; 3 when the pool, a target or the base branch is missing, or the base is not checked out; 2 when no target is given, a target repeats, PRs and slots without one are mixed, or the confirmation flags conflict; 4 when `ship.mergeApproval` covers the train and the `merge-approval` gate is not cleared
old: none (new in #83; the orchestrator ran trains by hand in round 1)
note: targets are slot names or PRs as `rota worker gate` takes them, merged in the order given. Each is first checked as `gate --check-only` does (freshness, PR identity, provenance); the first refusal ends the train with that member's verdict and `culprit` set, nothing merged.
note: the members are merged in order onto the base (`origin/<base>` when any has a PR) in a scratch worktree, and `test.full` run on that tree once. On a pass each member lands through the gate in order (forge merge pinned to its head, no second verify; a branch behind the base only because an earlier member landed is not `stale`). Before the first landing the base and every head must be what the scratch tree was built from, else `base-moved` and nothing lands. Before each later landing the base tree must equal the scratch tree after the members landed so far, and after the last one the landed tree must equal the verified tree; a mismatch (a foreign commit on the base, or a forge that merged differently than git) stops the train with `base-moved`, `landed` listing what already went in and the hint saying `landed k of n`. Under `test.fullWhere` `ci` the scratch tree is pushed as `rota/ci/train` and verified by CI instead of `test.full`, each bisect prefix likewise (one CI run per step); `ci-not-run`, `verify-timeout` and `ci-config-changed` (any member touching the CI definition) end the train with nothing landed. All members must be PRs or all slots without one: a mix is exit 2.
note: a member that conflicts in the scratch tree is `merge-failed` with `culprit` set. When verification fails the train bisects prefixes of the order and names the first member whose merge breaks the tree as `culprit` (`verify-failed`); it may break only with the members before it. Bisect assumes a green base, so when the first member is named the bare base is verified once more; if that is red the verdict is `verify-failed` with no `culprit`. A red train costs up to ceil(log2 n) extra verifies. Nothing lands unless `--land-green`, which lands the verified prefix before the culprit (`landed` lists them, `changed` is true, the exit is still 1).
note: a gate (not `--check-only`) and a train on one repository serialize on `<git-common-dir>/rota/land.lock`, held from before the first write (the scratch worktree, the merge) through landing; the train's own landing steps run under it. The lock is taken outside `test/gate.sh`'s machine-wide lock, never inside.
note: one approval covers the train: `ship.mergeApproval` is judged over the union of the files the members change, before the scratch merge. `--approval` and `--escalate` use the first target's thread. Bounces are not counted; a landed member forgets its count.
note: (#387) the train reads the exclusion ledger exactly as `rota worker gate` does (see there): once from the base before the scratch merge, excusing a failing `test.full` or `test.e2e` command whose failing tests all have an unexpired entry (`data.excluded`), and failing as `verify-failed` with nothing landed, naming test, owner and receipt in the message and `data.expired`, when any entry has expired. A malformed ledger is exit 2.

### rota worker session check
rota worker session check [--session <name>]
repo: none
data: {"inside": bool, "where"?: string}
exit: 1 when outside a managed host session (`inside: false`); 5 when the host is unavailable
old: hv-worker-session check [--session <name>]
shim: `inside` is true on exit 0, with `where` the text after `inside ` (`herdr workspace w9`, a tmux variant). Old exit 3 becomes 5.

### rota worker session ensure
rota worker session ensure [--session <name>] [--body-file <path|->] [--boot-timeout <s>]
repo: none
data: {"inside": bool, "where"?: string, "handedOff": bool, "session"?: string, "changed": bool}
exit: 3 when the body file is missing; 4 when work.dispatch=herdr and the caller is outside herdr (`blockedBy: "outside herdr"`); 5 when the host fails or the operator window never came up
old: hv-worker-session ensure [--session <name>] [--instruction-file <path>] [--boot-timeout <s>]
shim: `--body-file` is passed as the old `--instruction-file`; `-` is spooled to a temp file. `inside: true, handedOff: false` on `inside <where> — no handoff needed`. `handedOff: true`, `session` and `changed: true` on the `handed off to tmux session '<s>'` banner. Old exit 2 (file missing) becomes 3; old exit 3 is split by its message (herdr outside becomes 4, the rest become 5).
note: after a handoff the caller is still outside the session, so `data` is `inside: false, handedOff: true`.

### rota worker account list
rota worker account list
repo: none
data: {"accounts": [{"name": string, "configDir": string, "verdict": string, "reason": string, "fiveHour"?: number, "sevenDay"?: number, "resetsAt"?: string, "headroom"?: number}]}
exit: implied only
old: hv-worker-account list --json
shim: wraps the old array as `accounts`; null fields become absent. `verdict` is `free`, `cooling` or `unknown`. No accounts configured gives `{"accounts": []}`. Env `ROTA_ACCOUNT_USAGE_DIR` stays as is.

### rota worker account pick
rota worker account pick [--exclude <name>[,<name>...]]
repo: none
data: {"found": bool, "account"?: string}
exit: 1 when no account is usable (every account cooling, none configured, or none left after --exclude; `found: false`)
old: hv-worker-account pick [--exclude <names>]
shim: `account` is the bare stdout name. Old exit 3 with `every configured account is cooling down` or no accounts after exclude becomes 1; other old exit 3 stays 3.

### rota worker account assign
rota worker account assign <slot> [--account <name>]
repo: none
data: {"slot": string, "account": string, "changed": bool}
exit: 3 when the registry, the slot or the named account is unknown; 4 when --account is omitted and no account is usable
old: hv-worker-account assign --slot <slot> [--account <name>]
shim: parses `assigned: <slot> -> <account>`; `changed` is false when the slot already held that account. Old exit 3 for no usable account becomes 4.
note: no usable account was exit 1; as a mutating verb that declines, it is now 4 (rule 7).

### rota test run
rota test run <fast|full|e2e> [--base <ref>]
repo: none
data: {"tier": string, "commands": []string, "verified": []string, "failed": []string, "logPath"?: string}
exit: 1 when a command fails (`failed` names it, `logPath` is its captured output); 3 when the tier holds no commands (the message names `test.<tier>`) or `{files}` is used and no base or merge base resolves; 2 for a tier other than `fast`, `full` or `e2e`
old: none (new in #375)
note: runs `test.<tier>` in the project root, in order, and stops at the first failure; `commands` lists what ran (after `{files}` expansion), `verified` the ones that passed. `full` reads what the merge gate reads, so a config that still holds `refactor.verifyCommands` keeps working.
note: with `test.isolate` (bool, silent default `true`) each command runs with `HERDR_*`, `TMUX*`, `SSH_AUTH_SOCK` and `SSH_AGENT_PID` removed and `HOME` and the `XDG_*` dirs pinned under a temp root removed after the run, for all three tiers; `GOCACHE`, `GOMODCACHE`, `GOPATH` and `GOENV` keep their real values. `false` passes the environment through unchanged.
note: `{files}` in a command expands to the files that differ between `git merge-base <base> HEAD` and the working tree (committed and uncommitted changes, plus untracked files that are not gitignored; deleted files left out), each single-quoted for the shell and joined by spaces; no changes expand to the empty string, with no length cap. `--base` defaults to the resolved base branch (`rota git base`), swapped for `origin/<base>` when that remote-tracking ref exists and is strictly ahead of the local base (a lagging local branch would otherwise pull upstream commits into the list); an explicit `--base` is used as given. The diff is read only when a command contains `{files}`. Because each word is already single-quoted, a command that wraps `{files}` in its own quotes (`"{files}"`, `'{files}'`) breaks the quoting. A changed file whose name starts with `-` reaches the command as an option; put `--` before `{files}` (`eslint -- {files}`).

### rota test ledger check
rota test ledger check
repo: none
data: {"verdict": "ok"|"expired"|"malformed", "entries": number, "expired": []ledgerEntry, "malformed": [{"index": number, "test": string, "reason": string}]}
exit: 1 when an entry has expired (`verdict` `expired`, `expired` names each) or the ledger is malformed (`verdict` `malformed`, `malformed` names the first bad entry); 0 otherwise, including a missing file or an empty list; 2 for an argument
old: none (new in #387)
note: reads `.rota/test-ledger.json`, a tracked JSON list of `{"test": string, "owner": string, "receipt": string, "expires": string}`: the test name (the top-level go test name; its subtests are covered), who owns the fix, the issue or PR that tracks it, and the day the exclusion lapses. `expires` is `YYYY-MM-DD` (valid through the end of that day, UTC) or an RFC 3339 time. Every field is required. A missing file or `[]` means no exclusions, and the file starts empty. The verb runs nothing; it is the check `rota worker gate` and `rota worker train` apply, without a merge.
