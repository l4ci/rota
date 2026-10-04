package worker

import (
	"context"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

// operatorHost is a tmux-like fake that can open an operator window.
type operatorHost struct {
	*fakeHost
	opts host.OperatorOpts
	err  error
}

func (o *operatorHost) EnsureOperator(_ context.Context, opts host.OperatorOpts) error {
	o.opts = opts
	return o.err
}

func TestSessionCheck(t *testing.T) {
	dir := newProject(t, `{}`)
	f := tmuxFake()
	if st := envWith(f).SessionCheck(bg, dir); !st.Inside || st.Where != "main" {
		t.Errorf("%+v", st)
	}
	f.inSession = false
	if st := envWith(f).SessionCheck(bg, dir); st.Inside {
		t.Errorf("%+v", st)
	}
}

func TestSessionEnsureInsideNeedsNoHandoff(t *testing.T) {
	dir := newProject(t, `{}`)
	st, err := envWith(tmuxFake()).SessionEnsure(bg, dir, SessionOpts{})
	if err != nil || !st.Inside || st.HandedOff {
		t.Errorf("%+v %v", st, err)
	}
}

func TestSessionEnsureHerdrOutsideIsRefusedNotHandedOff(t *testing.T) {
	dir := newProject(t, `{"work":{"dispatch":"herdr"}}`)
	f := &fakeHost{name: "herdr"}
	_, err := envWith(f).SessionEnsure(bg, dir, SessionOpts{})
	we, ok := err.(*exitcode.Error)
	if !ok || we.Exit != exitcode.ExitRefused || !strings.Contains(we.Message, "needs /rota-work to run inside a herdr pane") || !strings.Contains(we.Hint, "open herdr") {
		t.Errorf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("herdr was driven from outside a pane: %v", f.calls)
	}
}

func TestSessionEnsureOpensTheOperatorWindow(t *testing.T) {
	dir := newProject(t, `{"models":{"orchestrator":"fable"}}`)
	instr := writeBrief(t, "go\n")
	oh := &operatorHost{fakeHost: &fakeHost{name: "tmux"}}
	st, err := (Env{NewHost: func(string) host.Host { return oh }}).SessionEnsure(bg, dir, SessionOpts{Session: "ops", BodyFile: instr, BootTimeout: 9})
	if err != nil || !st.HandedOff || st.Session != "ops" {
		t.Fatalf("%+v %v", st, err)
	}
	if oh.opts.Session != "ops" || oh.opts.Root != dir || oh.opts.Instruction != instr || oh.opts.BootTimeout != 9 ||
		oh.opts.Command != "claude --continue --model fable --permission-mode auto" {
		t.Errorf("opts = %+v", oh.opts)
	}
	// default session and command
	oh2 := &operatorHost{fakeHost: &fakeHost{name: "tmux"}}
	dir2 := newProject(t, `{"work":{"operatorCommand":"my-claude"}}`)
	(Env{NewHost: func(string) host.Host { return oh2 }}).SessionEnsure(bg, dir2, SessionOpts{})
	if oh2.opts.Session != "rota" || oh2.opts.Command != "my-claude" || oh2.opts.BootTimeout != 60 {
		t.Errorf("opts = %+v", oh2.opts)
	}
}

func TestSessionEnsureFailures(t *testing.T) {
	dir := newProject(t, `{}`)
	oh := &operatorHost{fakeHost: &fakeHost{name: "tmux"}}
	e := Env{NewHost: func(string) host.Host { return oh }}
	_, err := e.SessionEnsure(bg, dir, SessionOpts{BodyFile: "/no/such"})
	if exitOf(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "instruction file not found") {
		t.Errorf("missing body: %v", err)
	}
	for _, tc := range []struct {
		err  error
		msg  string
		hint string
	}{
		{host.ErrOperatorBoot, "did not come up within 60s", "tmux attach -t rota"},
		{host.ErrOperatorSend, "never picked up its instruction", "paste it by hand"},
		{host.ErrOperatorSession, "could not create tmux session 'rota'", ""},
		{host.ErrOperatorWindow, "could not create the operator window", ""},
	} {
		oh.err = tc.err
		_, err := e.SessionEnsure(bg, dir, SessionOpts{})
		we, ok := err.(*exitcode.Error)
		if !ok || we.Exit != exitcode.ExitUnavailable || !strings.Contains(we.Message, tc.msg) || !strings.Contains(we.Hint, tc.hint) {
			t.Errorf("%v: err = %#v", tc.err, err)
		}
	}
	oh.fakeHost.requireErr = os.ErrNotExist
	if _, err := e.SessionEnsure(bg, dir, SessionOpts{}); exitOf(err) != exitcode.ExitUnavailable {
		t.Errorf("host missing: %v", err)
	}
}
