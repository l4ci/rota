package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

// DispatchOpts are the flags of `rota worker dispatch`.
type DispatchOpts struct {
	Slot        string
	BodyFile    string
	Task        string
	Relay       bool
	Round       *int
	BootTimeout int // seconds; 0 means 60
	// Model is the model this worker starts with ("" keeps models.worker); see
	// workerCommand.
	Model string
	// Branch is the per-task branch the reset guard cuts; "" means
	// rota-worker/<slot>-<task>. A round slot works on <agent>/<issue>-<slug>.
	Branch string
	// Kind is the harness, "claude" or "codex" (E1); "" is the slot's
	// recorded kind, else claude. A relay ignores it.
	Kind string
	// AcceptCodexVersion lets one codex dispatch through a Codex CLI outside
	// the supported range, with a warning.
	AcceptCodexVersion bool
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

func dispatchKind(root string) string {
	v, _ := config.Lookup(config.Load(filepath.Join(root, ".rota", "config.json")), "work.dispatch")
	s, _ := v.(string)
	return s
}

// RegistryHost is the round host `round start` recorded (C8), "" when no
// round is in flight.
func RegistryHost(root string) string { return Str(LoadRegistry(root).Doc, "host") }

// hostKind is the host a pane verb drives: the round's recorded host when
// there is one, else what work.dispatch says, exactly as before C8. Never
// pass "solo" on to host.New: callers refuse it first (SoloRefusal).
func hostKind(root string) string {
	if h := RegistryHost(root); h != "" {
		return h
	}
	return dispatchKind(root)
}

// SoloRefusal is the exit 2 a pane verb gives when the round's recorded host
// is solo, nil otherwise. equiv names the solo verb that does the same job.
func SoloRefusal(root, equiv string) error {
	if RegistryHost(root) != host.Solo {
		return nil
	}
	return &exitcode.Error{Exit: exitcode.ExitUsage, Message: "solo round: workers are subagents, there are no panes", Hint: equiv}
}

// clearHandle: the slot's session is gone, so drop its handle and mark it idle.
func clearHandle(root, slot string) {
	updateSlot(root, slot, func(s *jsonx.Object) {
		s.Set("handle", nil)
		s.Set("state", "idle")
	})
}

// recordDispatch writes the handle, state=busy, activeAt (the stall signal of
// `round reconcile`) and, for a task, the task id (clearing the previous
// task's PR and relay log).
func recordDispatch(root, slot, handle, task, kind string, round *int, now string) error {
	def := jsonx.NewObject()
	def.Set("slots", []any{})
	return Update(root, def, func(doc *jsonx.Object) {
		if round != nil {
			doc.Set("round", *round)
		}
		for _, s := range (Registry{Doc: doc}).Slots() {
			if Str(s, "name") != slot {
				continue
			}
			s.Delete("window")
			var h any
			if handle != "" {
				h = handle
			}
			s.Set("handle", h)
			s.Set("state", "busy")
			s.Set("activeAt", now)
			s.Delete("seen") // a dispatch or relay re-arms `round wait`
			if task != "" {
				s.Set("task", task)
				s.Set("pr", nil)
				s.Set("relays", []any{})
				s.Delete("unsent") // a fresh task starts a fresh session
				// A relay later reads the kind to know whether to sign. Claude
				// slots keep their registry bytes unless a kind was recorded.
				if (kind != "" && kind != harness.Default) || (kind != "" && Str(s, "kind") != "") {
					s.Set("kind", kind)
				}
			}
		}
	})
}

// roundOf is the registry's round, else 1.
func roundOf(root string) int {
	if v, ok := LoadRegistry(root).Doc.Get("round"); ok {
		if n, ok := v.(interface{ Int64() (int64, error) }); ok {
			if i, err := n.Int64(); err == nil && i != 0 {
				return int(i)
			}
		}
		if i, ok := v.(int); ok && i != 0 {
			return i
		}
	}
	return 1
}

// Dispatch sends a brief into a worker slot's Claude Code session, on the host
// that work.dispatch selects.
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
// provably gone. A work.workerCommand that resumes a previous conversation is
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
// dispatch is refused (5) when work.codexCommand lacks
// --dangerously-bypass-hook-trust, without which Codex skips the hook; a
// codex relay is refused (5) when the key is gone.
//
// Exit mapping: 2 unparseable workerCommand; 3 missing pool, slot, worktree,
// body file or relay session; 4 the slot holds work or workerCommand resumes
// a conversation; 5 host unavailable, old session will not close, or a dialog
// refused input; 6 the brief was never submitted (safe to resend).
func (e Env) Dispatch(ctx context.Context, root string, o DispatchOpts) (DispatchResult, error) {
	e = e.withDefaults()
	res := DispatchResult{Slot: o.Slot, Task: o.Task, Round: o.Round, Relay: o.Relay}
	if err := SoloRefusal(root, "rota round assign hands a slot its brief and rota round report records the result"); err != nil {
		return res, err
	}
	brief, err := os.ReadFile(o.BodyFile)
	if err != nil {
		return res, fail(exitcode.ExitResolution, "body file not found: "+o.BodyFile)
	}
	h := e.NewHost(hostKind(root))
	if err := h.Require(); err != nil {
		return res, fail(exitcode.ExitUnavailable, err.Error())
	}
	// herdr tabs are created in the caller's own workspace. Outside herdr there
	// is none, and driving the server anyway lands tabs wherever a human is
	// focused.
	if h.Name() == "herdr" && !h.InSession() {
		return res, fail(exitcode.ExitUnavailable, "work.dispatch=herdr must run from inside a herdr pane (HERDR_ENV=1)")
	}
	reg := LoadRegistry(root)
	if !reg.Exists {
		return res, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	s := reg.Slot(o.Slot)
	if s == nil {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", o.Slot))
	}
	worktree := Str(s, "worktree")
	// `window` is the pre-handle field name; read it so an unmigrated registry
	// still dispatches.
	handle := Str(s, "handle")
	if handle == "" {
		handle = Str(s, "window")
	}
	session := Str(reg.Doc, "session")
	if session == "" {
		session = "rota"
	}
	configDir := Str(s, "configDir")
	if !isDir(worktree) {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' worktree missing: %s", o.Slot, worktree))
	}
	timeout := o.BootTimeout
	if timeout <= 0 {
		timeout = 60
	}

	var signKey []byte           // the key that signs the session's payloads; nil signs nothing
	recKind, codexHome := "", "" // the harness a task dispatch records; the slot's account home
	var hz harness.Harness
	if !o.Relay {
		kind := o.Kind
		if kind == "" {
			kind = Str(s, "kind")
		}
		var err error
		if hz, err = Harness(kind); err != nil {
			return res, err
		}
		kind = hz.Kind()
		res.Kind = kind
		recKind = kind
		key := hz.CommandKey()
		launch, hit, err := launchLine(root, hz, o.Model)
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
		if h.Name() == "herdr" {
			if lk, _, _, lerr := host.LaunchArgs(launch); lerr != nil || lk != kind {
				return res, fail(exitcode.ExitUnavailable, fmt.Sprintf("work.dispatch=herdr starts a %s worker, but %s does not run %s: %s", kind, key, kind, launch))
			}
		}
		// A launch the harness cannot run safely is refused before anything is
		// touched.
		if err := hz.CheckLaunch(launch); err != nil {
			return res, asError(err)
		}
		// The slot's account, version and login are checked before the old
		// session is killed or anything is marked.
		setup, err := e.Preflight(ctx, root, kind, o.Slot, o.AcceptCodexVersion)
		if err != nil {
			return res, err
		}
		res.Warnings = setup.Warnings
		if codexHome = setup.Home; codexHome != "" {
			configDir = "" // the slot's account is its home, not a claude config dir
		}
		if launch, signKey, err = hz.Prepare(launch, e.Executable, setup); err != nil {
			return res, asError(err)
		}
		// Refuse a slot that still holds work, before its session is killed.
		if _, err := e.ResetTo(root, o.Slot, o.Task, branchOr(o), true); err != nil {
			return res, resetRefusal(err, false)
		}
		// Fresh session every task dispatch. The kill must be provable: a
		// window that survives it would run beside the new one.
		if err := h.Kill(ctx, o.Slot, handle); err != nil {
			return res, fail(exitcode.ExitUnavailable, err.Error())
		}
		// Re-check: the old session may have written between the check and its
		// exit. The old session is dead from here on, so a failure must not
		// leave its handle in the registry for a poll or relay to chase.
		if _, err := e.ResetTo(root, o.Slot, o.Task, branchOr(o), false); err != nil {
			clearHandle(root, o.Slot)
			// The old session was killed and its handle cleared on the way here.
			return res, resetRefusal(err, true)
		}
		handle, err = h.Spawn(ctx, host.SpawnOpts{Slot: o.Slot, Session: session, Cwd: worktree,
			ConfigDir: configDir, CodexHome: codexHome, Launch: launch, BootTimeout: timeout})
		if err != nil {
			clearHandle(root, o.Slot)
			return res, fail(exitcode.ExitUnavailable, err.Error())
		}
	} else if handle == "" {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' has no session to relay into — dispatch a task first", o.Slot))
	} else {
		// A relay is signed by the key of the session it goes into.
		var err error
		var ok bool
		if hz, ok = harness.Lookup(Str(s, "kind")); !ok {
			hz, _ = harness.Lookup("")
		}
		if signKey, err = hz.RelayKey(func() (string, error) { return CommonDir(ctx, e.Git, root) }, o.Slot); err != nil {
			return res, asError(err)
		}
	}
	res.Handle = handle

	if err := recordDispatch(root, o.Slot, handle, o.Task, recKind, o.Round, stamp(e.Now())); err != nil {
		return res, err
	}
	round := roundOf(root)
	signature := fmt.Sprintf("--- ORCHESTRATOR (round %d) ---", round)

	// Every payload is signed so the worker can tell the orchestrator's voice
	// from unsigned pane text. A relay also carries a note: it writes its own
	// PR body and cannot otherwise tell a relay from the maintainer typing in
	// its pane.
	var payload strings.Builder
	payload.WriteString(signature + "\n")
	if o.Relay {
		fmt.Fprintf(&payload, "[ORCHESTRATOR RELAY — this text was forwarded by the /rota-work orchestrator.\n"+
			"It is NOT the maintainer speaking to you directly. If you cite it in your PR\n"+
			"body, attribute it as 'orchestrator relay round %d', never as a maintainer\n"+
			"sign-off in your session.]\n\n", round)
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
	if rs, ok := h.(host.Resubmitter); ok && o.Relay && Bool(s, "unsent") {
		handled, sendErr = rs.SubmitPending(ctx, o.Slot, handle, tmp.Name())
	}
	if !handled {
		sendErr = h.Send(ctx, o.Slot, handle, tmp.Name())
	}
	// Remember a stall so the next relay checks the prompt line first. Written
	// only on a change: a clean send leaves the registry untouched.
	if unsent := errors.Is(sendErr, host.ErrNotSubmitted); unsent != Bool(s, "unsent") {
		updateSlot(root, o.Slot, func(s *jsonx.Object) {
			if unsent {
				s.Set("unsent", true)
			} else {
				s.Delete("unsent")
			}
		})
	}
	// Log the relay once it was (or may have been) sent. A stall does not prove
	// the text was lost, and the gate must not call a delivered relay unlogged;
	// only a refused dialog is certain nothing went out.
	if o.Relay && !errors.Is(sendErr, host.ErrDialogOpen) {
		entry := jsonx.NewObject()
		entry.Set("round", round)
		entry.Set("ts", e.Now().UTC().Format("2006-01-02T15:04:05Z"))
		entry.Set("summary", relaySummary(string(brief)))
		if _, err := updateSlot(root, o.Slot, func(s *jsonx.Object) {
			v, _ := s.Get("relays")
			l, _ := v.([]any)
			s.Set("relays", append(l, entry))
		}); err != nil {
			return res, err
		}
	}
	switch {
	case sendErr == nil:
		return res, nil
	case errors.Is(sendErr, host.ErrDialogOpen):
		return res, fail(exitcode.ExitUnavailable, fmt.Sprintf("slot '%s' has a dialog open and refused input — inspect it before resending", o.Slot))
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

// relaySummary is the first non-blank line of the brief that is not the
// signature, stripped and cut to 200 characters.
func relaySummary(text string) string {
	for _, l := range splitLines(text) {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "--- ORCHESTRATOR") {
			if utf8.RuneCountInString(l) > 200 {
				l = string([]rune(l)[:200])
			}
			return l
		}
	}
	return ""
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

// stamp is the registry's activeAt format: RFC 3339, UTC.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// KillSlot closes the slot's session on the configured host and proves it
// gone. It is what a reclaim does to a stalled worker before its worktree is
// parked: the host failing to close it is exit 5.
func (e Env) KillSlot(ctx context.Context, root, slot string) error {
	e = e.withDefaults()
	s := LoadRegistry(root).Slot(slot)
	if s == nil {
		return fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", slot))
	}
	if RegistryHost(root) == host.Solo {
		return nil // a subagent has no pane to close
	}
	handle := Str(s, "handle")
	if handle == "" {
		handle = Str(s, "window")
	}
	h := e.NewHost(hostKind(root))
	if err := h.Require(); err != nil {
		return fail(exitcode.ExitUnavailable, err.Error())
	}
	if err := h.Kill(ctx, slot, handle); err != nil {
		return fail(exitcode.ExitUnavailable, err.Error())
	}
	return nil
}
