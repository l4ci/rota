package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
)

// Attachable host session guarantee: the port of bin/hv-worker-session.
//
// `work.dispatch: tmux` is worth its cost for one reason: a worker that needs
// a decision can idle and a human can answer it in that worker's pane. If
// /rota-work is launched from a terminal that is NOT already inside tmux, the
// worker windows get created in a DETACHED session nobody is looking at, and
// every escalation goes unanswered. So being inside the host is a
// PRECONDITION, not a nicety; when it is not met the orchestrator itself has
// to move into the session, which is what `ensure` does.
//
// The operator window runs `claude --continue` by default, which resumes the
// most recent conversation for this directory, so the cycle keeps its plan and
// wave layout. The caller MUST stop after a successful handoff: two
// orchestrators driving one pool would dispatch the same task twice.
//
// Inside a herdr pane there is nothing to hand off: worker tabs open in the
// caller's own workspace. Outside one, `ensure` refuses, because herdr's own
// guide forbids driving its server from outside a managed pane.

// SessionState is the answer of `session check` and `session ensure`.
type SessionState struct {
	Inside    bool
	Where     string
	HandedOff bool
	Session   string
}

// SessionCheck keys on an env var the host's panes inherit ($TMUX,
// HERDR_ENV). Under tmux it is the only reliable signal: `tmux has-session`
// answers whether a session EXISTS, which is a different question from
// whether WE are in it, and conflating them produces detached panes nobody
// reads.
func (e Env) SessionCheck(ctx context.Context, root string) SessionState {
	e = e.withDefaults()
	h := e.NewHost(e.hostKind(root))
	if h.InSession() {
		return SessionState{Inside: true, Where: h.Where()}
	}
	return SessionState{}
}

// SessionOpts are the flags of `rota worker session ensure`.
type SessionOpts struct {
	Session     string // default "rota"
	BodyFile    string // optional instruction for the operator
	BootTimeout int    // default 60
}

// operatorCommand is work.operatorCommand, else a resumed opus session. The
// operator runs in `auto`, not the workers' skip-permissions mode: it is the
// process that merges into the cycle branch and talks to the user, and the one
// window a human is actually watching, so it keeps a gate the workers do not.
func operatorCommand(root string) string {
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	if v, ok := config.Lookup(cfg, "work.operatorCommand"); ok {
		if s, _ := v.(string); s != "" {
			return s
		}
	}
	model := "opus"
	if v, ok := config.Lookup(cfg, "models.orchestrator"); ok {
		if s, _ := v.(string); s != "" {
			model = s
		}
	}
	return "claude --continue --model " + model + " --permission-mode auto"
}

// SessionEnsure guarantees a session a human can attach to. Exits: 3 missing
// body file; 4 work.dispatch=herdr outside herdr; 5 host failure or the
// operator window never came up.
func (e Env) SessionEnsure(ctx context.Context, root string, o SessionOpts) (SessionState, error) {
	e = e.withDefaults()
	if o.Session == "" {
		o.Session = "rota"
	}
	if o.BootTimeout <= 0 {
		o.BootTimeout = 60
	}
	if err := SoloRefusal(root, "a solo round has no host session to hand off into"); err != nil {
		return SessionState{}, err
	}
	h := e.NewHost(e.hostKind(root))
	if err := h.Require(); err != nil {
		return SessionState{}, fail(exitcode.ExitUnavailable, err.Error())
	}
	if h.InSession() {
		return SessionState{Inside: true, Where: h.Where()}, nil
	}
	if h.Name() == "herdr" {
		cwd, _ := os.Getwd()
		err := fail(exitcode.ExitRefused, "work.dispatch=herdr needs /rota-work to run inside a herdr pane")
		err.Data = BlockData{BlockedBy: "outside herdr"}
		err.Hint = fmt.Sprintf("open herdr, start Claude Code in a pane at %s, and run /rota-work there; worker tabs then open in that workspace, beside the orchestrator", cwd)
		return SessionState{}, err
	}
	op, ok := h.(host.Operator)
	if !ok {
		return SessionState{}, fail(exitcode.ExitUnavailable, h.Name()+" host cannot open an operator window")
	}
	if o.BodyFile != "" {
		if _, err := os.Stat(o.BodyFile); err != nil {
			return SessionState{}, fail(exitcode.ExitResolution, "instruction file not found: "+o.BodyFile)
		}
	}
	abs, err := filepath.EvalSymlinks(root)
	if err != nil {
		abs = root
	}
	err = op.EnsureOperator(ctx, host.OperatorOpts{Session: o.Session, Root: abs,
		Command: operatorCommand(root), Instruction: o.BodyFile, BootTimeout: o.BootTimeout})
	if err != nil {
		w := fail(exitcode.ExitUnavailable, err.Error())
		switch {
		case errors.Is(err, host.ErrOperatorBoot):
			w.Message = fmt.Sprintf("the operator session did not come up within %ds", o.BootTimeout)
			w.Hint = "the window exists — attach with: tmux attach -t " + o.Session
		case errors.Is(err, host.ErrOperatorSend):
			w.Hint = "attach and paste it by hand: tmux attach -t " + o.Session
		case errors.Is(err, host.ErrOperatorSession):
			w.Message = fmt.Sprintf("could not create tmux session '%s'", o.Session)
		}
		return SessionState{}, w
	}
	return SessionState{HandedOff: true, Session: o.Session}, nil
}
