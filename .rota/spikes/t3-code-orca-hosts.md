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

`Host` has 10 methods: `Kind`, `Capabilities`, `Require`, `InSession`, `Where`, `Spawn` (cwd, launch line, env, boot timeout), `Send` (paste a multi-line file as one prompt and prove pickup, else `ErrNotSubmitted` / `ErrDialogOpen`), `Capture` (recent N lines), `Status` (native state or ""), `Kill` (prove gone, pid-tree check), `Notify`. tmux implements `PaneReader`, `Draft`, `PaneHost`, `Snapshotter`, `OpenTab` and `Operator`; herdr adds `Turn`, `Explain`, `Watch`, `Label`, `Resubmitter` and workspace linking. `worker poll` classifies busy/done/blocked/limited/dead from `Capture` text plus `Status`, with `ROTA-DONE` / `ROTA-BLOCKED` sentinels read from pane text. Kind is a closed string set (`herdr`, `tmux`, `solo`) in `resolve.go`, and `host.New` has an `if kind == Herdr … else tmux` shape.

### orca (Electron IDE, real terminals, CLI to the running app)

orca is the closer fit: it exposes PTY terminals through a CLI, so the pane model survives.

| Host method | orca command (docs) | Verdict |
|---|---|---|
| Spawn | `orca worktree create --repo id:<id> --name <n> --agent claude\|codex --prompt "<text>" --json`, or `orca terminal create --worktree path:<dir> --title <t> --command "<launch>" --json` | Drivable. `--worktree path:` takes a directory, so rota's own worktrees can be reused. The handle is runtime-scoped and goes stale ("run `terminal list` and reacquire"). |
| Env passing | **Not found** on `terminal create` or `worktree create` | Gap. A `KEY=V cmd` prefix in `--command` is the tmux approach and probably works, but `CLAUDE_CONFIG_DIR` / `ROTA_SLOT` pass-through is unconfirmed. |
| Send + pickup | `orca terminal send --terminal <h> --text "<t>" --enter --json`; `orca terminal wait --terminal <h> --for tui-idle --timeout-ms N --json` | Partly. Multi-line paste behaviour (bracketed paste vs. submit-per-newline) is **not documented**. Pickup confirmation would reuse rota's existing prompt-line / busy-marker check over `terminal read --screen`. `wait --for` documents only `tui-idle`. |
| Capture | `orca terminal read --terminal <h> --json` (accumulated stream, escapes stripped), `--screen` (rendered frame), `--cursor <c> --limit N` for increments | Drivable. **No `--lines`**; rota takes the tail of the stream or uses `--screen`. Alt-screen TUIs (OpenCode) need `--screen`. |
| Status | No per-agent status command found. `orca agent hooks status` manages hook installation, `orca status` is runtime health. `orca terminal show --terminal <h>` and `orchestration worker-show` report PTY liveness (`observation.status`), `worker-list` gives `live`/`unverifiable`/`exited` | Weak. `Status` returns "" (like tmux); `worker poll` falls back to pane text, as it does under tmux. |
| Kill | `orca terminal close --terminal <h> --json`; `orca worktree rm --worktree id:<id> --force --json` | Drivable. `terminal stop` is **not found**. The guide warns not to substitute `terminal close` for `worker-release` on orchestrated dispatches, which does not apply to plain terminals. rota keeps its own pid-tree proof. |
| Require | `command -v orca`; `orca status --json` | Drivable. Orca is a GUI app: the runtime must be open. |
| InSession | **Not found** (no documented env marker like `HERDR_ENV` / `TMUX`) | Gap. Would probe `orca terminal current` / `orca worktree current --json` instead. |
| Notify | **Not found** | Skip (tmux's is a no-op too). |

Capabilities missing: `Labeler` (`--title` on create could cover it), `TurnReader`, `Explainer`, `Watcher` / `OutputWatcher` (poll `terminal read --cursor` instead), `WorkspaceCloser` / `LinkedLayouter` (no `CanGroup`), `Layouter`, `Operator`, `Snapshotter` (could build from `terminal list` + `show`). Same shape as tmux. Orca's own `orchestration worker-start` / `worker_done` mailbox is a second coordinator and would duplicate rota's, so it should stay unused.

Harnesses: orca lists Claude Code, Codex and OpenCode among its agent backends (`--agent claude|codex`), and `terminal create --command` runs any launch line.

Classification under `worker poll`: busy, done (sentinel), blocked (sentinel) and limited (pane phrases) work from the stream as under tmux; dead needs `terminal show` / `terminal list` to prove exit, since a stale handle is ambiguous (orca's own rule: absence is `unverifiable`, not dead).

Risks: CLI surface is young and the docs were thin on flags (the fetch tool saw summaries); a GUI dependency makes headless/CI rounds impossible; handles are runtime-scoped so a restart of the app orphans slots; 8.5k open issues on the repo (market review) says churn.

### t3-code (web/Electron/mobile control surface, `npx t3@latest`)

t3-code is **not a terminal host**. It drives agent CLIs through the server's provider adapters ("Provider processes, terminals, Git, and project files belong to the server"; clients talk RPC). Whether threads run in a PTY or via a protocol (Codex app-server, SDK) was **not confirmed**.

| Host method | t3-code surface (docs) | Verdict |
|---|---|---|
| Spawn | `t3 thread start <project> "<prompt>" [--worktree <base-branch>]` prints a thread id | Different model: spawn and first prompt are one call, and it makes its own worktree from a base branch. **No cwd flag found**, so it cannot adopt a rota-created worktree. |
| Send | `t3 thread send <id> "<message>"` (`-` reads stdin). Never interrupts a busy thread; queues as next turn | Delivery is confirmed by the server, but rota's relay semantics (interrupt-free paste into a TUI) differ; a relay mid-turn would queue silently. |
| Capture | `t3 thread show <id>`, `--wait` prints the agent's reply, `--json` | Gives the structured transcript, **not** pane text. `worker poll`'s sentinel and phrase matching would run on the reply text instead of the screen. Scrollback of the agent's TUI is not available. |
| Status | `t3 thread show` / `thread list`; `--wait` exits 1 on failure/stop, 2 when the agent needs an answer in the app | Better than tmux: native done/failed/needs-input. Maps to done/dead/blocked. No busy-vs-idle detail documented. |
| Kill | MCP "stop threads" tool (name not listed); **no `t3 thread stop` found in docs** | Not confirmed from the CLI. |
| Env | **Not found** | Gap. Provider processes are spawned by the server, not by rota, so `CLAUDE_CONFIG_DIR` / `ROTA_SLOT` per slot cannot be set per thread. |
| Require / InSession / Where | `t3 --help` (binary); `t3 service install` for a background server | Drivable for Require; InSession has no marker. |

Capabilities missing: everything pane-shaped (`PaneReader` as text, `Draft`, `Resubmitter`, `PaneHost`, `OutputWatcher`, `Snapshotter`, `Layouter`, `Operator`). The workers would also not be Claude Code TUIs, so rota's prompt-line guard (`--- ROTA-SIG ---`, `PreToolUse` hook, `rota-prompt.key`) and per-slot account env would not apply the way the worker contract assumes. A t3-code "host" is really a new worker transport, not a `Host`.

Fit to the interface: a `Host` implementation would need `Spawn` to return a thread id and `Send` to be `thread send`, with `Capture` synthesised from `thread show`. Possible, but the harness layer (launch line, account env, guard hook) and `worker poll` pane parsing would both need branches. That is a bigger change than a third `Kind`, and the issue puts changing `Host` out of scope.

## Decision

- **orca: defer.** Drivable behind the existing interface with tmux-like capabilities, but two blockers are unconfirmed (env pass-through, multi-line paste/pickup) and neither can be settled from docs. Condition to build: a day-long hands-on probe with orca installed shows `terminal create --command "K=V claude …"` carries env and `terminal send --text` pastes a multi-line brief as one submission; plus a third user or a user who commits to testing it. Cost if that holds: about 4 to 6 days (kind + `Resolve` + `New` switch, host file against fakes, `Require`/`InSession` probes, docs, a smoke section behind a `ROTA_SMOKE_HOST=orca` fake; the `Host` interface needs no change).
- **t3-code: decline.** It is a thread API over server-owned providers, not a terminal. It lacks cwd adoption, per-thread env, a confirmed stop, and pane text, so it cannot honour the worker contract (account env, signed relays, sentinels in the pane) without redesigning the harness and poll layers. Revisit only if t3-code ships a PTY/terminal CLI (`t3 terminal …`) or a documented `--cwd` plus `--env` on `thread start`. Effort if forced: 10 or more days and a `Host`-shaped hole, which is out of scope here.

Demand note: two users asked, nothing else is recorded in the issue thread. That supports a probe, not a build.

## Unverified

- Everything above is from docs; no tool was installed or run.
- orca: multi-line `terminal send` behaviour, env on `terminal create`, existence of `terminal stop`, other `wait --for` values, whether `terminal read` default size is bounded, an `InSession` env marker.
- t3-code: PTY vs protocol execution, CLI stop command, RPC method names in `packages/contracts/src/rpc.ts`.
