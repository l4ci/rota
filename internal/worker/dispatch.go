package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/stamp"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/layout"
)

// DispatchOpts are the flags of `rota worker dispatch`.
type DispatchOpts struct {
	Slot        string
	BodyFile    string
	Task        string
	Relay       bool
	Round       *int
	BootTimeout int // seconds; 0 means 60
	// Model is the model this worker starts with ("" keeps models.worker, or
	// launches without --model under round.profile=inherit); see workerCommand.
	Model string
	// Branch is the per-task branch the reset guard cuts; "" means
	// rota-worker/<slot>-<task>. A round slot works on <agent>/<issue>-<slug>.
	Branch string
	// Kind is the harness, "claude" or "codex" (E1); "" is the slot's
	// recorded kind, else claude. A relay ignores it.
	Kind string
}

// DispatchResult is what a successful dispatch did.
type DispatchResult struct {
	Slot   string
	Handle string
	Task   string
	Round  *int
	Relay  bool
	// Kind is the harness the session runs; "" for a relay, which ignores it.
	Kind     string
	Warnings []string
}

// dispatchSetting is round.dispatch from cfg, "" when unset.
func dispatchSetting(cfg any) host.Kind { return host.KindOf(config.Dispatch(cfg)) }

// RegistryHost is the round host `round start` recorded (C8), "" when no
// round is in flight.
//
// It tolerates a corrupt registry (reads as no host): callers pick a host to
// talk to and cannot refuse; the verbs that write the registry refuse it.
func RegistryHost(root string) host.Kind { return LoadRegistryTolerant(root).Host() }

// IsSoloHost reports whether k, a recorded round host, is the solo host:
// workers are in-harness subagents and no verb drives a pane. It is the one
// place a registry host is compared to host.Solo; a caller that already holds
// the host (one registry read) asks here instead of comparing.
func IsSoloHost(k host.Kind) bool { return k == host.Solo }

// IsSolo is IsSoloHost over root's recorded host. A missing or corrupt
// registry reads as no host, so not solo (RegistryHost tolerates both).
func IsSolo(root string) bool { return IsSoloHost(RegistryHost(root)) }

// ResolveHost is the host the project's round runs on: host.Resolve over the
// round's recorded host, round.dispatch and the environment. Every verb that
// needs the host asks here. getenv and lookPath default to the process's own;
// a caller that holds injected ones (cli's Deps, worker.Env) passes them, so
// the decision reads the same sources as the handle it then drives. cfg is the
// project's loaded config, which the caller owns.
func ResolveHost(root string, cfg any, getenv func(string) string, lookPath func(string) (string, error)) host.Kind {
	return host.Resolve(RegistryHost(root), dispatchSetting(cfg), getenv, lookPath)
}

// ResolvePaneHost is ResolveHost for a verb that must drive a pane host even
// with no round recorded: a bare environment reads as tmux, which reports
// unavailable if it is absent. A recorded solo round stays solo.
func ResolvePaneHost(root string, cfg any, getenv func(string) string, lookPath func(string) (string, error)) host.Kind {
	k := ResolveHost(root, cfg, getenv, lookPath)
	if k == host.Solo && !IsSolo(root) {
		return host.Tmux
	}
	return k
}

// hostKind is the host a pane verb drives. Never pass "solo" on to host.New:
// callers refuse it first (SoloRefusal), and New maps it to tmux anyway.
func (e Env) hostKind(root string) host.Kind {
	return ResolveHost(root, e.Config(root), e.Getenv, e.LookPath)
}

// SoloRefusal is the exit 2 a pane verb gives when the round's recorded host
// is solo, nil otherwise. equiv names the solo verb that does the same job.
func SoloRefusal(root, equiv string) error {
	if !IsSolo(root) {
		return nil
	}
	return &exitcode.Error{Exit: exitcode.ExitUsage, Message: "solo round: workers are subagents, there are no panes", Hint: equiv}
}

// clearHandle: the slot's session is gone, so drop its handle and mark it idle.
func clearHandle(root, slot string) {
	UpdateSlot(root, slot, func(s *Slot) { s.Release() })
}

// recordDispatch writes the handle, state=busy, activeAt (the stall signal of
// `round reconcile`), the turn baseline (0 clears it) and, for a task, the
// task id (clearing the previous task's PR and relay log).
func recordDispatch(root, slot, handle, task, kind string, round *int, turnSeq int, now string) error {
	return Update(root, func(d *Doc) {
		if round != nil {
			d.SetRound(*round)
		}
		for _, s := range d.Slots() {
			if s.Name() == slot {
				s.Dispatch(handle, task, kind, now)
				s.SetTurnSeq(turnSeq)
			}
		}
	})
}

// roundOf is the registry's round, else 1.
func roundOf(root string) int {
	if n, ok := LoadRegistryTolerant(root).Round(); ok && n != 0 {
		return n
	}
	return 1
}

// Dispatch sends a brief into a worker slot's Claude Code session, on the host
// that round.dispatch selects.
//
// A task brief recreates the slot's session first: /clear does not reliably
// reset a Claude Code session (it can land as a literal chat message), so a
// fresh window or tab is the only trustworthy reset. A relay goes into the
// RUNNING session instead: it answers a question the worker asked, and a fresh
// session would have no idea what the answer is to.
//
// A task dispatch also guards the slot (the reset guard: refuse when the
// worktree holds uncommitted or unmerged work, else cut a fresh per-task branch
// from the cycle branch) and refuses to spawn unless the old session is
// provably gone. A round.workerCommand that resumes a previous conversation is
// rejected.
//
// Provenance: every payload, brief or relay, is signed with a first line
// `--- ORCHESTRATOR (round N) ---`. A relay is also appended to the slot's
// relays[] as {round, ts, summary}; the gate checks the PR's approvals
// against it. That header is forgeable text, so a codex payload also ends with
// a `--- ROTA-SIG <hmac> ---` trailer made with a key in the slot's codex home
// (rotated on every task dispatch), and the codex launch carries a
// UserPromptSubmit hook (`rota worker prompt-check`) that blocks any input
// without a valid trailer, bar a maintainer's `m:` answer. A codex task
// dispatch is refused (5) when round.codexCommand lacks
// --dangerously-bypass-hook-trust, without which Codex skips the hook; a
// codex relay is refused (5) when the key is gone.
//
// Exit mapping: 2 unparseable workerCommand; 3 missing pool, slot, worktree,
// body file or relay session; 4 the slot holds work or workerCommand resumes
// a conversation; 5 host unavailable, old session will not close, or a dialog
// refused input; 6 the brief was never submitted (safe to resend).
func (e Env) Dispatch(ctx context.Context, root string, o DispatchOpts) (DispatchResult, error) {
	e = e.WithDefaults()
	res := DispatchResult{Slot: o.Slot, Task: o.Task, Round: o.Round, Relay: o.Relay}
	if err := SoloRefusal(root, "rota round assign hands a slot its brief and rota round report records the result"); err != nil {
		return res, err
	}
	pre, err := LoadRegistry(root)
	if err != nil {
		return res, err
	}
	if s := pre.Slot(o.Slot); s != nil && s.IsExternal() {
		e := fail(exitcode.ExitRefused, "external slot has no host")
		e.Data = BlockData{BlockedBy: "host"}
		return res, e
	}
	brief, err := os.ReadFile(o.BodyFile)
	if err != nil {
		return res, fail(exitcode.ExitResolution, "body file not found: "+o.BodyFile)
	}
	cfg := e.Config(root)
	h := e.NewHost(ResolveHost(root, cfg, e.Getenv, e.LookPath))
	if err := h.Require(); err != nil {
		return res, fail(exitcode.ExitUnavailable, err.Error())
	}
	// herdr tabs are created in the caller's own workspace. Outside herdr there
	// is none, and driving the server anyway lands tabs wherever a human is
	// focused.
	if h.Kind() == host.Herdr && !h.InSession() {
		return res, fail(exitcode.ExitUnavailable, "round.dispatch=herdr must run from inside a herdr pane (HERDR_ENV=1)")
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return res, err
	}
	if !reg.Exists {
		return res, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	s := reg.Slot(o.Slot)
	if s == nil {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", o.Slot))
	}
	worktree := s.Worktree()
	handle := s.PaneHandle()
	session := reg.Session()
	if session == "" {
		session = "rota"
	}
	configDir := s.ConfigDir()
	if !fsio.IsDir(worktree) {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' worktree missing: %s", o.Slot, worktree))
	}
	timeout := o.BootTimeout
	if timeout <= 0 {
		timeout = 60
	}

	var signKey []byte      // the key that signs the session's payloads; nil signs nothing
	var accountEnv []string // the variable that selects the slot's account
	recKind := ""           // the harness a task dispatch records
	var hz harness.Harness
	if !o.Relay {
		kind := o.Kind
		if kind == "" {
			kind = s.Kind()
		}
		var err error
		if hz, err = Harness(kind); err != nil {
			return res, err
		}
		kind = hz.Kind()
		res.Kind = kind
		recKind = kind
		key := hz.CommandKey()
		launch, hit, err := e.launchLine(root, hz, o.Model)
		if err != nil {
			return res, err
		}
		if hit.Token != "" {
			e := fail(exitcode.ExitRefused, fmt.Sprintf("%s contains %s'%s', which reopens the previous conversation; a task dispatch must start a fresh session. Remove it.", key, hit.Noun, hit.Token))
			e.Data = BlockData{BlockedBy: "resume flag"}
			return res, e
		}
		// Under herdr the launch binary decides `agent start --kind`, so it
		// must be the kind's own: refuse before anything is killed.
		if h.Kind() == host.Herdr {
			if lk, _, _, lerr := host.LaunchArgs(launch); lerr != nil || lk != kind {
				return res, fail(exitcode.ExitUnavailable, fmt.Sprintf("round.dispatch=herdr starts a %s worker, but %s does not run %s: %s", kind, key, kind, launch))
			}
		}
		// A launch the harness cannot run safely is refused before anything is
		// touched.
		if err := hz.CheckLaunch(launch); err != nil {
			return res, asError(err)
		}
		// The slot's account, launch flags and login are checked before the old
		// session is killed or anything is marked.
		setup, err := e.Preflight(ctx, root, kind, o.Slot, o.Model)
		if err != nil {
			return res, err
		}
		res.Warnings = setup.Warnings
		if !hz.WorkAccounts() {
			configDir = "" // a codex slot's account is its home, not a claude config dir
			account := setup.Account
			if _, err := UpdateSlot(root, o.Slot, func(sl *Slot) { sl.SetHomeAccount(account) }); err != nil {
				return res, err
			}
		}
		if configDir != "" {
			// A slot cached by an older rota may hold the path as written.
			home, _ := e.Home()
			configDir = ExpandConfigDirIn(configDir, home)
		}
		acct := harness.Account{ConfigDir: configDir, Home: setup.Home}
		// No account configured for the slot: it runs under the orchestrator's.
		amb := hz.AmbientAccount(e.Getenv)
		if acct.ConfigDir == "" {
			acct.ConfigDir = amb.ConfigDir
		}
		if acct.Home == "" {
			acct.Home = amb.Home
		}
		accountEnv = hz.AccountEnv(acct)
		if launch, signKey, err = hz.Prepare(launch, e.Executable, setup); err != nil {
			return res, asError(err)
		}
		group, err := GroupSlots(cfg, h.Capabilities())
		if err != nil {
			return res, fail(exitcode.ExitResolution, err.Error())
		}
		// Refuse a slot that still holds work, before its session is killed.
		if _, err := e.ResetTo(root, o.Slot, o.Task, branchOr(o), true); err != nil {
			return res, resetRefusal(err, false)
		}
		// Fresh session every task dispatch. The kill must be provable: a
		// window that survives it would run beside the new one.
		sweepShells(ctx, h, worktree)
		if err := h.Kill(ctx, o.Slot, handle); err != nil {
			return res, fail(exitcode.ExitUnavailable, err.Error())
		}
		e.closeSlotWorkspace(ctx, h, root, o.Slot)
		// The old handle names a pane that is gone. Drop it now, not at
		// recordDispatch after the boot: a watch ticking in between would
		// read the dead pane and report the slot dead (#707).
		clearHandle(root, o.Slot)
		// Re-check: the old session may have written between the check and its
		// exit. The old session is dead from here on, so a failure must not
		// leave its handle in the registry for a poll or relay to chase.
		if _, err := e.ResetTo(root, o.Slot, o.Task, branchOr(o), false); err != nil {
			clearHandle(root, o.Slot)
			// The old session was killed and its handle cleared on the way here.
			return res, resetRefusal(err, true)
		}
		portStart, portBlock := PortRange(cfg)
		portBase, width, err := EnsurePortBase(root, o.Slot, portStart, portBlock)
		if err != nil {
			return res, fail(exitcode.ExitUnavailable, err.Error())
		}
		var linked bool
		handle, err = h.Spawn(ctx, host.SpawnOpts{Slot: o.Slot, Session: session, Cwd: worktree, LinkWorkspace: group, Linked: &linked,
			Env: append(accountEnv, SlotEnv(o.Slot, portBase, width)...), Launch: launch, BootTimeout: timeout})
		if err != nil {
			clearHandle(root, o.Slot)
			if handle != "" {
				// The spawn failed with its session still open (an unanswered
				// startup screen). Record the handle so the replay's kill
				// reaps it instead of spawning a second one beside it.
				UpdateSlot(root, o.Slot, func(sl *Slot) { sl.SetHandle(handle) })
			}
			return res, fail(exitcode.ExitUnavailable, err.Error())
		}
		if group && linked {
			// The slot's tab id leads with the workspace it lives in. Record it
			// so the next kill closes this workspace and no other.
			ws, _, _ := strings.Cut(handle, ":")
			UpdateSlot(root, o.Slot, func(sl *Slot) { sl.SetWorkspaceID(ws) })
		}
	} else if handle == "" {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' has no session to relay into — dispatch a task first", o.Slot))
	} else {
		// A relay is signed by the key of the session it goes into.
		var err error
		var ok bool
		if hz, ok = harness.Lookup(s.Kind()); !ok {
			hz = harness.Resolve("")
		}
		if signKey, err = hz.RelayKey(func() (string, error) { return CommonDir(ctx, e.Git, root) }, o.Slot); err != nil {
			return res, asError(err)
		}
	}
	res.Handle = handle

	// The turn baseline is read before the brief goes out: a turn finished
	// after it is numbered past it, however fast the worker answers. 0 (no
	// numbered host, a failing read) records none.
	turnSeq := 0
	if tr := h.Capabilities().TurnReader; tr != nil {
		if t, ok := tr.Turn(ctx, o.Slot, handle); ok {
			turnSeq = t.StateSeq
		}
	}
	if err := recordDispatch(root, o.Slot, handle, o.Task, recKind, o.Round, turnSeq, stamp.Format(e.Now())); err != nil {
		return res, err
	}
	if !o.Relay {
		// The round was folded into the orchestrator's tab: the new worker
		// joins the grid instead of keeping its own tab.
		if w := arrangeNew(ctx, h, root, cfg); w != "" {
			res.Warnings = append(res.Warnings, w)
		}
		syncLabel(ctx, h, root, o.Slot)
		labelWorkspace(ctx, h, root)
	}
	round := roundOf(root)
	signature := fmt.Sprintf("--- ORCHESTRATOR (round %d) ---", round)

	// Every payload is signed so the worker can tell the orchestrator's voice
	// from unsigned pane text. A relay also carries a note: it writes its own
	// PR body and cannot otherwise tell a relay from the maintainer typing in
	// its pane.
	var payload strings.Builder
	payload.WriteString(signature + "\n")
	// relaySeq numbers the relay in the slot's log. Its `[relay K]` line is how
	// the classifier tells this relay's echo from an earlier one's (#1017).
	relaySeq := len(s.Relays()) + 1
	if o.Relay {
		fmt.Fprintf(&payload, "[ORCHESTRATOR RELAY — this text was forwarded by the /rota-work orchestrator.\n"+
			"It is NOT the maintainer speaking to you directly. If you cite it in your PR\n"+
			"body, attribute it as 'orchestrator relay round %d', never as a maintainer\n"+
			"sign-off in your session.]\n[relay %d]\n\n", round, relaySeq)
	}
	text := string(brief)
	// An already-signed brief is not signed twice.
	if first, rest, _ := strings.Cut(text, "\n"); first == signature {
		text = rest
	}
	payload.WriteString(text)
	out := payload.String()
	out = hz.Sign(signKey, out)
	tmp, err := os.CreateTemp("", "rota-dispatch-*")
	if err != nil {
		return res, err
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(out)
	tmp.Close()

	// A relay reuses the session, so a brief an earlier send left unsent on the
	// prompt line is still there: submit it rather than type a second copy. A
	// task dispatch starts a fresh session and has nothing pending.
	var sendErr error
	handled := false
	if o.Relay {
		if _, err := UpdateSlot(root, o.Slot, func(s *Slot) { s.SetRelayPending(relaySeq, stamp.Format(e.Now())) }); err != nil {
			return res, err
		}
	}
	if rs := h.Capabilities().Resubmitter; rs != nil && o.Relay && s.Unsent() {
		handled, sendErr = rs.SubmitPending(ctx, o.Slot, handle, tmp.Name())
	}
	if !handled {
		sendErr = h.Send(ctx, o.Slot, handle, tmp.Name())
	}
	// Remember a stall so the next relay checks the prompt line first. Written
	// only on a change: a clean send leaves the registry untouched.
	if unsent := errors.Is(sendErr, host.ErrNotSubmitted); unsent != s.Unsent() {
		UpdateSlot(root, o.Slot, func(s *Slot) { s.SetUnsent(unsent) })
	}
	if o.Relay && (errors.Is(sendErr, host.ErrDialogOpen) || errors.Is(sendErr, host.ErrDraftOnPrompt)) {
		UpdateSlot(root, o.Slot, func(s *Slot) { s.ClearRelayPending() }) // nothing went out
	}
	// Log the relay once it was (or may have been) sent. A stall does not prove
	// the text was lost, and the gate must not call a delivered relay unlogged;
	// only a refused dialog or a human draft is certain nothing went out.
	if o.Relay && !errors.Is(sendErr, host.ErrDialogOpen) && !errors.Is(sendErr, host.ErrDraftOnPrompt) {
		entry := newRelayEntry(round, e.Now(), string(brief))
		if _, err := UpdateSlot(root, o.Slot, func(s *Slot) { s.AppendRelay(entry) }); err != nil {
			return res, err
		}
	}
	switch {
	case sendErr == nil:
		return res, nil
	case errors.Is(sendErr, host.ErrDialogOpen):
		first := ""
		var de *host.DialogError
		if errors.As(sendErr, &de) {
			first = " (" + de.First + ")"
		}
		return res, &exitcode.Error{Exit: exitcode.ExitUnavailable, Data: BlockData{BlockedBy: "dialog"},
			Message: fmt.Sprintf("slot '%s' has a dialog open%s and refused input — inspect it before resending", o.Slot, first)}
	case errors.Is(sendErr, host.ErrDraftOnPrompt):
		return res, fail(exitcode.ExitUnavailable, fmt.Sprintf("slot '%s' has a human draft on its prompt line and nothing was sent — resend once it is submitted or cleared", o.Slot))
	default:
		return res, fail(exitcode.ExitRetry, fmt.Sprintf("slot '%s' never picked up the brief — inspect the session before resending", o.Slot))
	}
}

// resetRefusal maps a reset-guard error onto dispatch's exits: a slot holding
// work is a refusal (4), anything else keeps its own exit.
func resetRefusal(err error, changed bool) error {
	var we *exitcode.Error
	if errors.As(err, &we) && we.Data != nil {
		return &exitcode.Error{Exit: exitcode.ExitRefused, Message: we.Message, Data: BlockData{BlockedBy: "reset guard", Changed: changed}}
	}
	return err
}

// BlockData is the failure data of an exit-4 refusal: what blocked it and
// whether the verb changed state on the way (contract: exit 4 data).
type BlockData struct {
	BlockedBy string
	Changed   bool
}

// splitLines is Python's str.splitlines for the separators that matter.
func splitLines(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, string(rs[start:i]))
			start = i + 1
		case '\r':
			out = append(out, string(rs[start:i]))
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

func branchOr(o DispatchOpts) string {
	if o.Branch != "" {
		return o.Branch
	}
	return BranchFor(o.Slot, o.Task)
}

// KillSlot closes the slot's session on the configured host and proves it
// gone. It is what a reclaim does to a stalled worker before its worktree is
// parked: the host failing to close it is exit 5.
func (e Env) KillSlot(ctx context.Context, root, slot string) error {
	e = e.WithDefaults()
	reg, err := LoadRegistry(root)
	if err != nil {
		return err
	}
	s := reg.Slot(slot)
	if s == nil {
		return fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", slot))
	}
	if IsSolo(root) {
		return nil // a subagent has no pane to close
	}
	handle := s.PaneHandle()
	h := e.NewHost(e.hostKind(root))
	if err := h.Require(); err != nil {
		return fail(exitcode.ExitUnavailable, err.Error())
	}
	sweepShells(ctx, h, s.Worktree())
	if err := h.Kill(ctx, slot, handle); err != nil {
		return fail(exitcode.ExitUnavailable, err.Error())
	}
	e.closeSlotWorkspace(ctx, h, root, slot)
	return nil
}

// closeSlotWorkspace closes the linked workspace recorded on the slot, after its
// session is killed, and forgets it. Only a recorded id is closed: a handle from
// the default path names the orchestrator's own workspace. Best effort, since
// herdr closes the workspace with its last tab already.
func (e Env) closeSlotWorkspace(ctx context.Context, h host.Host, root, slot string) {
	s := LoadRegistryTolerant(root).Slot(slot)
	if s == nil || s.WorkspaceID() == "" {
		return
	}
	if wc := h.Capabilities().WorkspaceCloser; wc != nil {
		if err := wc.CloseWorkspace(ctx, s.WorkspaceID()); err != nil {
			fmt.Fprintf(e.Stderr, "rota: slot %s: %v\n", slot, err)
		}
	}
	UpdateSlot(root, slot, func(sl *Slot) { sl.SetWorkspaceID("") })
}

// SweepSlot closes the leftover shell panes of a slot whose agent is already
// gone (a dead slot), wherever a layout split moved them. Best effort: a pane
// that stays is a stray window, not a reason to refuse a reclaim.
func (e Env) SweepSlot(ctx context.Context, root, slot string) {
	e = e.WithDefaults()
	s := LoadRegistryTolerant(root).Slot(slot) // best effort: a corrupt registry leaves the stray panes
	if s == nil || IsSolo(root) {
		return
	}
	h := e.NewHost(e.hostKind(root))
	if h.Require() == nil {
		sweepShells(ctx, h, s.Worktree())
	}
}

// sweepShells closes the bare-shell panes left in the worktree, on a host that
// can list them. Best effort: Kill still proves the session gone.
func sweepShells(ctx context.Context, h host.Host, worktree string) {
	if sw := h.Capabilities().PaneSweeper; sw != nil && worktree != "" {
		sw.SweepShells(ctx, worktree)
	}
}

// arrangeNew re-applies a recorded split to the round after a spawn, so a
// worker assigned later does not sit in a tab of its own. A host that cannot
// arrange panes, or a round in tabs, is a no-op. The worker is already up, so a
// failure is a warning, never a failed dispatch.
func arrangeNew(ctx context.Context, h host.Host, root string, cfg any) string {
	l := h.Capabilities().Layouter
	reg := LoadRegistryTolerant(root) // a warning-only rearrange after a spawn; cannot fail the dispatch
	if l == nil || reg.Layout() != layout.Split {
		return ""
	}
	_, _, found, err := layout.Arrange(ctx, l, root, layout.Split, reg.CLIPane(), LiveWorkers(ctx, l, reg))
	if group, gerr := GroupSlots(cfg, h.Capabilities()); err == nil && found && gerr == nil && group {
		// The split closed the new worker's linked workspace.
		err = RecordWorkspaces(ctx, l, root, reg.CLIPane())
	}
	switch {
	case err != nil:
		return "layout split: " + err.Error() + "; run rota layout split to retry"
	case !found:
		return "layout split: no orchestrator pane open in herdr; the worker kept its tab"
	}
	return ""
}

// LiveWorkers are the slots whose agent has a pane, in registry order. A
// parked slot has no handle and a dead one no agent, so both are left out.
func LiveWorkers(ctx context.Context, h host.Layouter, reg Registry) []layout.Worker {
	var out []layout.Worker
	for _, s := range reg.Slots() {
		handle := s.PaneHandle()
		if handle == "" {
			continue
		}
		if pane := h.PaneOf(ctx, s.Name(), handle); pane != "" {
			out = append(out, layout.Worker{Slot: s.Name(), Pane: pane, Handle: handle, Path: s.Worktree()})
		}
	}
	return out
}

// GroupSlots reads round.herdrGroupSlots: whether a slot lives in a linked
// workspace of its own. It is on only for a host that can group (herdr); on
// any other host the default being true must not record workspaces.
func GroupSlots(cfg any, caps host.Capabilities) (bool, error) {
	on, err := config.Bool(cfg, "round.herdrGroupSlots")
	return on && caps.CanGroup(), err
}

// RecordWorkspaces stores each live slot's linked workspace as herdr has it
// now: "" for a slot whose pane is in the orchestrator's workspace, which is
// where a layout split leaves it once herdr closed the linked one. Callers
// call it for a grouped round only.
func RecordWorkspaces(ctx context.Context, l host.Layouter, root, cli string) error {
	panes, err := l.LayoutPanes(ctx)
	if err != nil {
		return err
	}
	st, ok := layout.Find(panes, root, layout.Label, cli, LiveWorkers(ctx, l, LoadRegistryTolerant(root)))
	if !ok {
		return nil
	}
	linked := layout.Linked(st)
	return UpdateSlots(root, func(s *Slot) {
		if ws, ok := linked[s.Name()]; ok {
			s.SetWorkspaceID(ws)
		}
	})
}
