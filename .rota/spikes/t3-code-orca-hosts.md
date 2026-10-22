---
name: t3-code-orca-hosts
branch: ben/1736-evaluate-t3-code-and-orca
status: done
finished: 2026-10-22
created: 2026-10-22
---

# ben/1736-evaluate-t3-code-and-orca

## Question

Can t3-code or orca drive rota's `host.Host` contract (`internal/host/host.go`: Spawn, Send with pickup confirmation, Capture/ReadPane, Status, Kill, plus optional `Capabilities` sides), so a worker slot runs inside it like a herdr tab or tmux window? Build, defer or decline, with an effort estimate.

## What was tried

Docs only. Nothing was installed or run; every command below is quoted from a page, not executed.

- orca: `github.com/stablyai/orca` README, `onorca.dev/docs/cli/overview`, `onorca.dev/docs/cli/reference`, `skill-guides/orchestration.md` (raw, main). Earlier code read: `docs/contributing/market-review-2026-10.md`, "Orca orchestration".
- t3-code: `github.com/pingdotgg/t3code` README, `docs/internals/overview.md`, `docs/user/outside-agents.md`, third-party summaries. The RPC contract `packages/contracts/src/rpc.ts` and `apps/server` were **not read**.
- rota side: `internal/host/host.go`, `resolve.go`, and the method lists of `tmux.go` and `herdr.go`.

## Findings

### What rota needs

`Host` has 11 methods (`host.go:270-295`): `Kind`, `Capabilities`, `Require`, `InSession`, `Where`, `Spawn` (cwd, launch line, env, boot timeout), `Send` (paste a multi-line file as one prompt and prove pickup, else `ErrNotSubmitted` / `ErrDialogOpen`), `Capture` (recent N lines), `Status` (native state or ""), `Kill` (prove gone, pid-tree check) and `Notify`. `Capabilities` (`host.go:218-236`) has 17 optional sides: `Resubmitter`, `Explainer`, `PaneReader`, `Labeler`, `TurnReader`, `Watcher`, `PaneSweeper`, `Layouter`, `TabOpener`, `SessionStarter`, `TabLister`, `PaneHost`, `OutputWatcher`, `Operator`, `Snapshotter`, `WorkspaceCloser`, `LinkedLayouter`. tmux implements `PaneReader`, `PaneHost`, `Snapshotter`, `TabOpener` and `Operator` (checked by receiver in `internal/host`); herdr implements every other side and not `Operator`. `worker poll` classifies busy/done/blocked/limited/dead from `Capture` text plus `Status`, with `ROTA-DONE` / `ROTA-BLOCKED` sentinels read from pane text. Kind is a closed string set (`herdr`, `tmux`, `solo`) in `resolve.go`, and `host.New` has an `if kind == Herdr ... else tmux` shape.

A nil `Capabilities` field means "side absent" and callers fall back (`CapabilitiesOf` discovers sides by type assertion), so every side below is optional. The only gate is `CanGroup()`, which needs both `WorkspaceCloser` and `LinkedLayouter`.

### orca (Electron IDE, real terminals, CLI to the running app)

orca is the closer fit: it exposes PTY terminals through a CLI, so the pane model survives.

**Host methods** (all 11):

| Host method | orca command (docs) | Verdict |
|---|---|---|
| Kind | n/a (rota constant) | Needs a new `Kind` value. |
| Capabilities | n/a (rota-side struct) | Returns the sides listed below. |
| Spawn | `orca worktree create --repo id:<id> --name <n> --agent claude\|codex --prompt "<text>" --json`, or `orca terminal create --worktree path:<dir> --title <t> --command "<launch>" --json` | Drivable. `--worktree path:` takes a directory, so rota's own worktrees can be reused. The handle is runtime-scoped and goes stale ("run `terminal list` and reacquire"). |
| Env passing (part of Spawn) | **Not found** on `terminal create` or `worktree create` | Gap. A `KEY=V cmd` prefix in `--command` is the tmux approach and probably works, but `CLAUDE_CONFIG_DIR` / `ROTA_SLOT` pass-through is unconfirmed. |
| Send + pickup | `orca terminal send --terminal <h> --text "<t>" --enter --json`; `orca terminal wait --terminal <h> --for tui-idle --timeout-ms N --json` | Partly. Multi-line paste behaviour (bracketed paste vs. submit-per-newline) is **not documented**. Pickup confirmation would reuse rota's existing prompt-line / busy-marker check over `terminal read --screen`. `wait --for` documents only `tui-idle`. |
| Capture | `orca terminal read --terminal <h> --json` (accumulated stream, escapes stripped), `--screen` (rendered frame), `--cursor <c> --limit N` for increments | Drivable. **No `--lines`**; rota takes the tail of the stream or uses `--screen`. Alt-screen TUIs (OpenCode) need `--screen`. |
| Status | No per-agent status command found. `orca agent hooks status` manages hook installation, `orca status` is runtime health. `orca terminal show --terminal <h>` and `orchestration worker-show` report PTY liveness (`observation.status`), `worker-list` gives `live`/`unverifiable`/`exited` | Weak. `Status` returns "" (like tmux); `worker poll` falls back to pane text, as it does under tmux. |
| Kill | `orca terminal close --terminal <h> --json`; `orca worktree rm --worktree id:<id> --force --json` | Drivable. `terminal stop` is **not found**. The guide warns not to substitute `terminal close` for `worker-release` on orchestrated dispatches, which does not apply to plain terminals. rota keeps its own pid-tree proof. |
| Require | `command -v orca`; `orca status --json` | Drivable. Orca is a GUI app: the runtime must be open. |
| InSession | **Not found** (no documented env marker like `HERDR_ENV` / `TMUX`) | Gap. Would probe `orca terminal current` / `orca worktree current --json` instead. |
| Where | `orca terminal current --json` / `orca worktree current --json` for a name; otherwise a rota-built string | Drivable (status-line text only). Exact output fields not read. |
| Notify | **Not found** | Skip (tmux's is a no-op too). |

**Capabilities sides** (all 17; "lacks" = leave the field nil, caller falls back):

| Side | orca | Verdict |
|---|---|---|
| Resubmitter | `terminal read --screen` to spot an unsent brief on the prompt line, then `terminal send --enter` with empty text | Buildable, same technique as herdr's. Optional: tmux has none, and without it a resend types the brief again, which risks a duplicate prompt but does not break `Send`. Unconfirmed until multi-line paste is probed. |
| Explainer | **Not found** (no equivalent of herdr `agent explain`) | Lacks. Cosmetic. |
| PaneReader | `terminal read --screen` can return text; a vanished terminal would show in `terminal list` / `show` | Buildable. Must map "terminal gone" (`unverifiable` vs. `exited`) onto `ErrPaneGone`, which docs call ambiguous, so it returns it only on `exited`. |
| Labeler | `--title` on `terminal create` sets a title | Partial: slot title yes; `LabelWorkspace` (round token) has no documented command. Cosmetic, can be skipped. |
| TurnReader | **Not found** (no state-change or completion sequence numbers) | Lacks. `Turn.Finished` cannot be computed; `worker poll` uses sentinels instead, as under tmux. |
| Watcher | **Not found** (no agent-status event stream) | Lacks. Callers poll. |
| PaneSweeper | `terminal list` plus `terminal show` to find shell-only terminals in a cwd, then `terminal close` | Buildable; shell-only detection needs a field the docs did not show. Not needed if `Kill` closes the terminal it created. |
| Layouter | **Not found** (no pane move/split command) | Lacks. |
| TabOpener | `orca terminal create --title <t> --command "<cmd>"` (focus flag not found) | Buildable. |
| SessionStarter | **Not found** (the GUI app owns the session; no headless named-session start) | Lacks. |
| TabLister | `orca terminal list --json`, `orca terminal close` | Buildable for list and close. |
| PaneHost | `terminal list` / `show` resolve a handle; `terminal read --screen` captures; `terminal send --enter` sends | Buildable, same shape as tmux's. |
| OutputWatcher | **Not found** (poll `terminal read --cursor` instead) | Lacks. |
| Operator | **Not found** (tmux-only concept) | Lacks. |
| Snapshotter | `terminal list --json` plus `terminal show` | Buildable. |
| WorkspaceCloser | `orca worktree rm` closes a worktree, but not a rota "linked workspace" | Lacks. |
| LinkedLayouter | **Not found** | Lacks. With `WorkspaceCloser` it makes `CanGroup()` false, as for tmux. |

Net: orca can plausibly get `Resubmitter`, `PaneReader`, `PaneHost`, `TabOpener`, `TabLister`, `Snapshotter` and partial `Labeler`/`PaneSweeper`; it lacks `Explainer`, `TurnReader`, `Watcher`, `OutputWatcher`, `Layouter`, `SessionStarter`, `Operator`, `WorkspaceCloser`, `LinkedLayouter`. That is the tmux shape plus `TabLister`. Orca's own `orchestration worker-start` / `worker_done` mailbox is a second coordinator and would duplicate rota's, so it should stay unused.

Harnesses: orca lists Claude Code, Codex and OpenCode among its agent backends (`--agent claude|codex`), and `terminal create --command` runs any launch line.

Classification under `worker poll`: busy, done (sentinel), blocked (sentinel) and limited (pane phrases) work from the stream as under tmux; dead needs `terminal show` / `terminal list` to prove exit, since a stale handle is ambiguous (orca's own rule: absence is `unverifiable`, not dead).

Risks: CLI surface is young and the docs were thin on flags (the fetch tool saw summaries); a GUI dependency makes headless/CI rounds impossible; handles are runtime-scoped so a restart of the app orphans slots; 8.5k open issues on the repo (market review) says churn.

### t3-code (web/Electron/mobile control surface, `npx t3@latest`)

t3-code is **not a terminal host**. It drives agent CLIs through the server's provider adapters ("Provider processes, terminals, Git, and project files belong to the server"; clients talk RPC). Whether threads run in a PTY or via a protocol (Codex app-server, SDK) was **not confirmed**.

**Host methods** (all 11):

| Host method | t3-code surface (docs) | Verdict |
|---|---|---|
| Kind | n/a (rota constant) | Needs a new `Kind` value. |
| Capabilities | n/a (rota-side struct) | Returns the sides listed below. |
| Spawn | `t3 thread start <project> "<prompt>" [--worktree <base-branch>]` prints a thread id | Different model: spawn and first prompt are one call, and it makes its own worktree from a base branch. **No cwd flag found**, so it cannot adopt a rota-created worktree. |
| Env passing (part of Spawn) | **Not found** | Gap. Provider processes are spawned by the server, not by rota, so `CLAUDE_CONFIG_DIR` / `ROTA_SLOT` per slot cannot be set per thread. |
| Send | `t3 thread send <id> "<message>"` (`-` reads stdin). Never interrupts a busy thread; queues as next turn | Delivery is confirmed by the server, but rota's relay semantics (interrupt-free paste into a TUI) differ; a relay mid-turn would queue silently. |
| Capture | `t3 thread show <id>`, `--wait` prints the agent's reply, `--json` | Gives the structured transcript, **not** pane text. `worker poll`'s sentinel and phrase matching would run on the reply text instead of the screen. Scrollback of the agent's TUI is not available. |
| Status | `t3 thread show` / `thread list`; `--wait` exits 1 on failure/stop, 2 when the agent needs an answer in the app | Better than tmux: native done/failed/needs-input. Maps to done/dead/blocked. No busy-vs-idle detail documented. |
| Kill | An MCP "stop threads" tool exists (name not listed); the CLI command is **not found** (no `t3 thread stop` in docs) | Drivable in principle via MCP; the CLI call is unnamed, so unconfirmed. rota's pid-tree proof cannot apply, since the server owns the provider process. |
| Require | `t3 --help` (binary); `t3 service install` for a background server | Drivable. |
| InSession | **Not found** (no env marker) | Gap. |
| Where | Server URL / project name from `t3` config (exact command not read) | Buildable as a rota-built string. |
| Notify | **Not found** | Skip (tmux's is a no-op too). |

**Capabilities sides** (all 17):

| Side | t3-code | Verdict |
|---|---|---|
| Resubmitter | **Not applicable** (`thread send` is acknowledged by the server; no prompt line to hold an unsent brief) | Lacks. A resend after a failed send is a second `thread send`, which queues a duplicate turn. |
| Explainer | **Not found** | Lacks. |
| PaneReader | **Not found** (no pane; `thread show` returns a transcript) | Lacks as pane text. A transcript read could stand in for `Capture`, but `ErrPaneGone` needs a "thread deleted" signal the docs do not show. |
| Labeler | **Not found** | Lacks. Cosmetic. |
| TurnReader | `thread show --json` reports a native status (done/failed/needs-input). **No sequence number found** | Partial at best: `Turn.Status` can be filled, but `StateSeq` and `CompletionSeq` stay 0, and `Turn.Finished(base)` needs `base > 0` and `CompletionSeq > base`, so it never reports finished. A real `TurnReader` needs a per-turn counter (message or turn index in the `--json` output would do); unread. |
| Watcher | **Not found** (`thread show --wait` blocks on one thread; no stream across threads) | Lacks. Callers poll, or hold one `--wait` per slot. |
| PaneSweeper | **Not applicable** (no panes) | Lacks. |
| Layouter | **Not applicable** | Lacks. |
| TabOpener | **Not found** | Lacks. |
| SessionStarter | `t3 service install` starts a background server, not a named session running a command | Lacks. |
| TabLister | `t3 thread list` lists threads, no close command found | Partial: list only. |
| PaneHost | **Not applicable** | Lacks. |
| OutputWatcher | **Not found** | Lacks. |
| Operator | **Not found** | Lacks. |
| Snapshotter | `t3 thread list` | Buildable as a thread list; `Agent` fields would be thin. |
| WorkspaceCloser | **Not found** | Lacks. |
| LinkedLayouter | **Not found** | Lacks. `CanGroup()` false. |

Net: t3-code could fill `Snapshotter`, part of `TabLister` and the status half of `TurnReader`; it lacks nearly everything pane-shaped. The workers would also not be Claude Code TUIs, so rota's prompt-line guard (`--- ROTA-SIG ---`, `PreToolUse` hook, `rota-prompt.key`) and per-slot account env would not apply the way the worker contract assumes. A t3-code "host" is really a new worker transport, not a `Host`.

Fit to the interface: a `Host` implementation would need `Spawn` to return a thread id and `Send` to be `thread send`, with `Capture` synthesised from `thread show`. Possible, but the harness layer (launch line, account env, guard hook) and `worker poll` pane parsing would both need branches. That is a bigger change than a third `Kind`, and the issue puts changing `Host` out of scope.

## Decision

- **orca: defer.** Drivable behind the existing interface with tmux-like capabilities, but two blockers are unconfirmed (env pass-through, multi-line paste/pickup) and neither can be settled from docs. Condition to build: a day-long hands-on probe with orca installed shows `terminal create --command "K=V claude ..."` carries env and `terminal send --text` pastes a multi-line brief as one submission; plus a third user or a user who commits to testing it. Cost if that holds: about 4 to 6 days (kind + `Resolve` + `New` switch, host file against fakes, `Require`/`InSession` probes, `PaneReader`/`PaneHost`/`TabLister`/`Snapshotter` over `terminal list|show|read`, docs, a smoke section behind a `ROTA_SMOKE_HOST=orca` fake). The `Host` interface needs no change because every side above is optional; `Resubmitter` is the one that could add a day, since its prompt-line check needs the same paste probe. If the probe shows the paste is not one submission, `Send` itself fails the contract and the estimate no longer holds.
- **t3-code: decline.** It is a thread API over server-owned providers, not a terminal. It lacks cwd adoption, per-thread env, a CLI stop command (an MCP stop tool exists but is unnamed in the docs), a sequence number for `TurnReader`, and pane text, so it cannot honour the worker contract (account env, signed relays, sentinels in the pane) without redesigning the harness and poll layers. Revisit only if t3-code ships a PTY/terminal CLI (`t3 terminal ...`) or a documented `--cwd` plus `--env` on `thread start`. Effort if forced: 10 or more days and a `Host`-shaped hole, which is out of scope here.

Demand note: two users asked, nothing else is recorded in the issue thread. That supports a probe, not a build.

## Unverified

- Everything above is from docs; no tool was installed or run.
- orca: multi-line `terminal send` behaviour, env on `terminal create`, existence of `terminal stop`, the `terminal list|show` fields a `PaneSweeper` would need, other `wait --for` values, whether `terminal read` default size is bounded, an `InSession` env marker.
- t3-code: PTY vs protocol execution, the CLI stop command (the MCP stop tool exists), whether `--json` output carries a turn or message counter, RPC method names in `packages/contracts/src/rpc.ts`.
