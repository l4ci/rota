package orchestrate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

// rig is a launcher with every outside call faked: no herdr, tmux or agent
// binary is reached (a live round runs inside both).
type rig struct {
	env       map[string]string
	installed map[string]bool
	calls     []string
	execs     [][]string
	execErr   error
}

func newRig(env map[string]string, installed ...string) *rig {
	r := &rig{env: env, installed: map[string]bool{}}
	for _, i := range installed {
		r.installed[i] = true
	}
	return r
}

func (r *rig) launcher() Env {
	run := func(_ context.Context, name string, args []string) (host.Result, error) {
		r.calls = append(r.calls, name+" "+strings.Join(args, " "))
		switch {
		case name == "herdr" && args[0] == "tab":
			return host.Result{Stdout: `{"result":{"tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2"}}}`}, nil
		case name == "tmux" && args[0] == "new-window":
			return host.Result{Stdout: "@3\n"}, nil
		}
		return host.Result{}, nil
	}
	look := func(n string) (string, error) {
		if r.installed[n] {
			return "/fake/" + n, nil
		}
		return "", errors.New("not found")
	}
	return Env{
		Getenv:   func(k string) string { return r.env[k] },
		LookPath: look,
		Host: func(kind string) host.Host {
			return host.New(kind, host.Deps{Run: run, Getenv: func(k string) string { return r.env[k] }, LookPath: look})
		},
		Exec: func(path string, argv, _ []string) error {
			r.execs = append(r.execs, append([]string{path}, argv...))
			return r.execErr
		},
		Self: "/bin/rota",
	}
}

func cfgOf(t *testing.T, kv map[string]any) any {
	t.Helper()
	root := jsonx.NewObject()
	for dotted, v := range kv {
		parts := strings.Split(dotted, ".")
		cur := root
		for _, p := range parts[:len(parts)-1] {
			next, ok := cur.Get(p)
			if !ok {
				next = jsonx.NewObject()
				cur.Set(p, next)
			}
			cur = next.(*jsonx.Object)
		}
		cur.Set(parts[len(parts)-1], v)
	}
	return root
}

func TestResolveByWhereTheCallerIs(t *testing.T) {
	herdrIn := map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1"}
	tests := []struct {
		name      string
		env       map[string]string
		installed []string
		cfg       map[string]any
		host      string
		mode      string
		code      string // expected Error.Code, "" for none
	}{
		{"inside herdr", herdrIn, []string{"herdr", "tmux"}, nil, "herdr", ModeTab, ""},
		{"inside tmux", map[string]string{"TMUX": "/tmp/t,1,0"}, []string{"tmux"}, nil, "tmux", ModeTab, ""},
		{"outside, tmux installed", nil, []string{"tmux", "herdr"}, nil, "tmux", ModeSession, ""},
		{"outside, no multiplexer", nil, nil, nil, "solo", ModeInPlace, ""},
		{"outside, dispatch tmux without tmux", nil, nil, map[string]any{"work.dispatch": "tmux"}, "", "", "unavailable"},
		{"outside, dispatch herdr", nil, []string{"herdr", "tmux"}, map[string]any{"work.dispatch": "herdr"}, "", "", "refused"},
		{"inside herdr beats dispatch tmux", herdrIn, []string{"herdr", "tmux"}, map[string]any{"work.dispatch": "tmux"}, "herdr", ModeTab, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := newRig(tt.env, tt.installed...).launcher().Resolve("/work/proj", cfgOf(t, tt.cfg))
			var oe *Error
			switch {
			case tt.code != "" && (!errors.As(err, &oe) || oe.Code != tt.code):
				t.Fatalf("err = %v, want code %s", err, tt.code)
			case tt.code == "" && err != nil:
				t.Fatal(err)
			}
			if tt.code == "" && (p.Host != tt.host || p.Mode != tt.mode) {
				t.Errorf("host/mode = %s/%s, want %s/%s", p.Host, p.Mode, tt.host, tt.mode)
			}
		})
	}
}

func TestResolveRefusalExplainsHerdrOutside(t *testing.T) {
	_, err := newRig(nil, "herdr").launcher().Resolve("/work/proj", cfgOf(t, map[string]any{"work.dispatch": "herdr"}))
	var oe *Error
	if !errors.As(err, &oe) || !strings.Contains(oe.Hint, "/work/proj") {
		t.Fatalf("hint should say where to run rota: %v", err)
	}
}

func TestSupervisorWrapsTheAgentAndPassesThePromptFirstOnly(t *testing.T) {
	l := newRig(nil).launcher()
	cases := []struct {
		name string
		cfg  map[string]any
		want []string
	}{
		{"claude by default", nil,
			[]string{"/bin/rota", "keepalive", "run", "--first-prompt", "/rota-orchestrate", "--", "claude", "--model", "opus", "--permission-mode", "auto"}},
		{"claude on models.orchestrator", map[string]any{"models.orchestrator": "sonnet"},
			[]string{"/bin/rota", "keepalive", "run", "--first-prompt", "/rota-orchestrate", "--", "claude", "--model", "sonnet", "--permission-mode", "auto"}},
		{"work.operatorCommand wins for claude", map[string]any{"work.operatorCommand": "claude --model fable"},
			[]string{"/bin/rota", "keepalive", "run", "--first-prompt", "/rota-orchestrate", "--", "claude", "--model", "fable"}},
		{"codex", map[string]any{"orchestrator.harness": "codex"},
			[]string{"/bin/rota", "keepalive", "run", "--first-prompt", "$rota-orchestrate", "--", "codex"}},
		{"hermes", map[string]any{"orchestrator.harness": "hermes"},
			[]string{"/bin/rota", "keepalive", "run", "--first-prompt", "You are the orchestrator: run the rota-orchestrate skill.", "--", "hermes", "chat", "-s", "rota-orchestrate", "-q"}},
		{"opencode", map[string]any{"orchestrator.harness": "opencode"},
			[]string{"/bin/rota", "keepalive", "run", "--first-prompt", "You are the orchestrator: load the rota-orchestrate skill and follow it.", "--", "opencode", "--prompt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := l.Resolve("/p", cfgOf(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(p.Supervisor, c.want) {
				t.Errorf("supervisor = %q\nwant %q", p.Supervisor, c.want)
			}
		})
	}
}

func TestUnknownHarnessIsAConfigError(t *testing.T) {
	_, err := newRig(nil).launcher().Resolve("/p", cfgOf(t, map[string]any{"orchestrator.harness": "emacs"}))
	var oe *Error
	if !errors.As(err, &oe) || oe.Code != "config" || !strings.Contains(oe.Msg, "claude, codex, hermes, opencode") {
		t.Fatalf("err = %v", err)
	}
}

func TestLaunchTabRunsTheQuotedSupervisorInAFocusedTab(t *testing.T) {
	r := newRig(map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1"}, "herdr")
	l := r.launcher()
	p, _ := l.Resolve("/p", cfgOf(t, nil))
	got, err := l.Launch(context.Background(), p)
	if err != nil || got.Handle != "w1:t2" {
		t.Fatalf("handle = %q, err = %v", got.Handle, err)
	}
	log := strings.Join(r.calls, "\n")
	if !strings.Contains(log, "herdr tab create --workspace w1 --cwd /p --label orchestrator --focus") ||
		!strings.Contains(log, "herdr pane run w1:p2 /bin/rota keepalive run --first-prompt /rota-orchestrate -- claude --model opus --permission-mode auto") {
		t.Errorf("calls:\n%s", log)
	}
	if len(r.execs) != 0 {
		t.Errorf("a tab launch must not replace this process: %v", r.execs)
	}
}

func TestLaunchPromptWithDollarIsQuotedForTheShell(t *testing.T) {
	r := newRig(map[string]string{"TMUX": "x"}, "tmux")
	l := r.launcher()
	p, _ := l.Resolve("/p", cfgOf(t, map[string]any{"orchestrator.harness": "codex"}))
	if _, err := l.Launch(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), `--first-prompt '$rota-orchestrate'`) {
		t.Errorf("$ must reach codex literally, not be expanded by the tab's shell:\n%s", strings.Join(r.calls, "\n"))
	}
}

func TestLaunchSessionExecsTmuxWithTheSupervisor(t *testing.T) {
	r := newRig(nil, "tmux")
	l := r.launcher()
	p, _ := l.Resolve("/work/my.proj", cfgOf(t, nil))
	if _, err := l.Launch(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(r.execs) != 1 {
		t.Fatalf("execs = %v", r.execs)
	}
	e := r.execs[0]
	if e[0] != "/fake/tmux" || !reflect.DeepEqual(e[1:9], []string{"tmux", "new-session", "-A", "-s", "rota-my-proj", "-c", "/work/my.proj", "-n"}) {
		t.Errorf("exec = %q", e)
	}
	if line := e[len(e)-1]; !strings.HasPrefix(line, "/bin/rota keepalive run --first-prompt /rota-orchestrate -- claude") || !strings.HasSuffix(line, `exec "${SHELL:-sh}"`) {
		t.Errorf("shell line = %q", line)
	}
}

func TestLaunchInPlaceExecsTheSupervisorItself(t *testing.T) {
	r := newRig(nil)
	l := r.launcher()
	p, _ := l.Resolve("/p", cfgOf(t, nil))
	if _, err := l.Launch(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(r.execs) != 1 || r.execs[0][0] != "/bin/rota" || r.execs[0][1] != "/bin/rota" || r.execs[0][2] != "keepalive" {
		t.Errorf("execs = %q", r.execs)
	}
}

func TestLaunchExecFailureIsUnavailable(t *testing.T) {
	r := newRig(nil)
	r.execErr = errors.New("permission denied")
	l := r.launcher()
	p, _ := l.Resolve("/p", cfgOf(t, nil))
	_, err := l.Launch(context.Background(), p)
	var oe *Error
	if !errors.As(err, &oe) || oe.Code != "unavailable" {
		t.Fatalf("err = %v", err)
	}
}
