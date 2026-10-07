package cli

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tui"
)

// stubModel is the screen a test view builds from a verb's Data.
type stubModel struct{ data any }

func (s stubModel) Update(tui.Msg) (tui.Model, tui.Cmd) { return s, tui.Cmd{} }
func (s stubModel) Render(int, int, tui.Style) string   { return "" }

type uiRig struct {
	root    *Command
	deps    *Deps
	ran     int
	models  []tui.Model
	viewErr error
}

func newUIRig(t *testing.T) *uiRig {
	t.Helper()
	t.Setenv("TERM", "xterm")
	r := &uiRig{deps: testDeps()}
	r.deps.IsTerminal = func(any) bool { return true }
	r.deps.RunView = func(c *Ctx, m tui.Model) error {
		r.models = append(r.models, m)
		return nil
	}
	verb := func(fail bool) func(*flag.FlagSet) RunFunc {
		return func(*flag.FlagSet) RunFunc {
			return func(c *Ctx, args []string) (Result, error) {
				r.ran++
				if fail {
					return Result{}, Failed("boom")
				}
				return Result{Data: map[string]any{"n": 7}, Text: "plain\n"}, nil
			}
		}
	}
	view := func(c *Ctx, res Result) (tui.Model, error) {
		if r.viewErr != nil {
			return nil, r.viewErr
		}
		return stubModel{res.Data}, nil
	}
	r.root = &Command{Name: "rota", Subs: []*Command{{Name: "demo", Summary: "demo", Subs: []*Command{
		{Name: "show", Summary: "has a view", Verb: verb(false), View: view},
		{Name: "bare", Summary: "no view", Verb: verb(false)},
		{Name: "boom", Summary: "fails", Verb: verb(true), View: view},
	}}}}
	return r
}

func (r *uiRig) do(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(r.root, r.deps, args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func wantRefusal(t *testing.T, r *uiRig, code int, stdout, stderr, path, msg string) {
	t.Helper()
	if code != 2 {
		t.Errorf("exit %d, want 2\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, path+": "+msg+"\n") || !strings.Contains(stderr, "hint: run: "+path+"\n") {
		t.Errorf("stderr = %q", stderr)
	}
	if r.ran != 0 || len(r.models) != 0 {
		t.Errorf("verb ran %d times, %d views run; refusals come first", r.ran, len(r.models))
	}
}

func TestUIRefusesWithJSON(t *testing.T) {
	r := newUIRig(t)
	code, out, errs := r.do("demo", "show", "--ui", "--json")
	wantRefusal(t, r, code, out, errs, "rota demo show", "--ui and --json do not mix")
	if e := envelope(t, out)["error"].(map[string]any); e["code"] != "usage" {
		t.Errorf("error = %v", e)
	}
}

func TestUIRefusesAVerbWithoutAView(t *testing.T) {
	r := newUIRig(t)
	code, out, errs := r.do("demo", "bare", "--ui")
	wantRefusal(t, r, code, out, errs, "rota demo bare", "no --ui view for this verb")
}

func TestUIRefusesOffATerminal(t *testing.T) {
	r := newUIRig(t)
	r.deps.IsTerminal = func(f any) bool { return false }
	code, out, errs := r.do("demo", "show", "--ui")
	wantRefusal(t, r, code, out, errs, "rota demo show", "--ui needs an interactive terminal")

	r = newUIRig(t)
	t.Setenv("TERM", "dumb")
	code, out, errs = r.do("demo", "show", "--ui")
	wantRefusal(t, r, code, out, errs, "rota demo show", "--ui needs an interactive terminal")
}

func TestUIRefusalOrder(t *testing.T) {
	r := newUIRig(t)
	r.deps.IsTerminal = func(any) bool { return false }
	_, _, errs := r.do("demo", "bare", "--ui", "--json")
	if !strings.Contains(errs, "do not mix") {
		t.Errorf("json refusal comes first: %q", errs)
	}
	r = newUIRig(t)
	r.deps.IsTerminal = func(any) bool { return false }
	_, _, errs = r.do("demo", "bare", "--ui")
	if !strings.Contains(errs, "no --ui view") {
		t.Errorf("no-view refusal precedes the terminal one: %q", errs)
	}
}

func TestUIRunsTheViewOnTheVerbsData(t *testing.T) {
	for _, args := range [][]string{{"demo", "show", "--ui"}, {"--ui", "demo", "show"}} {
		r := newUIRig(t)
		code, out, errs := r.do(args...)
		if code != 0 || out != "" || errs != "" {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, out, errs)
		}
		if r.ran != 1 || len(r.models) != 1 {
			t.Fatalf("%v: ran %d, views %d", args, r.ran, len(r.models))
		}
		got := r.models[0].(stubModel).data.(map[string]any)
		if got["n"] != 7 {
			t.Errorf("%v: model data = %v", args, got)
		}
	}
}

func TestUIFailingVerbFailsAsWithoutIt(t *testing.T) {
	r := newUIRig(t)
	code, _, errs := r.do("demo", "boom", "--ui")
	_, _, plain := r.do("demo", "boom")
	if code != 1 || errs != plain || len(r.models) != 0 {
		t.Errorf("exit %d stderr %q vs %q, views %d", code, errs, plain, len(r.models))
	}
}

func TestUIViewBuildErrorIsANormalFailure(t *testing.T) {
	r := newUIRig(t)
	r.viewErr = Failed("no screen")
	code, out, errs := r.do("demo", "show", "--ui")
	if code != 1 || out != "" || !strings.Contains(errs, "rota demo show: no screen") || len(r.models) != 0 {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errs)
	}
}

func TestUIDoesNotChangeOutputWithoutTheFlag(t *testing.T) {
	r := newUIRig(t) // a terminal on both ends, TERM=xterm
	code, out, _ := r.do("demo", "show")
	if code != 0 || out != "plain\n" || len(r.models) != 0 {
		t.Errorf("text: exit %d stdout %q views %d", code, out, len(r.models))
	}
	_, out, _ = r.do("demo", "show", "--json")
	if out != `{"ok": true, "data": {"n": 7}}`+"\n" {
		t.Errorf("json: %q", out)
	}
}

func TestUIHelpNamesTheFlagOnlyOnVerbsWithAView(t *testing.T) {
	r := newUIRig(t)
	_, out, _ := r.do("demo", "show", "--help")
	if !strings.Contains(out, "-h/--help, --ui\n") {
		t.Errorf("view verb help: %q", out)
	}
	for _, v := range []string{"bare", "show"} {
		_, out, _ = r.do("demo", v, "--help")
		if strings.Contains(out, "--ui") != (v == "show") {
			t.Errorf("%s help: %q", v, out)
		}
	}
	_, js, _ := r.do("demo", "show", "--help", "--json")
	if strings.Contains(js, `"ui"`) {
		t.Errorf("help json lists ui as a verb flag: %s", js)
	}
}

func TestDefaultRunViewOffATerminalIsAUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	c := &Ctx{Path: "rota demo show", Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}
	err := defaultDeps().RunView(c, stubModel{})
	if err == nil || asError(err).Exit != 2 {
		t.Errorf("err = %v", err)
	}
}

func TestDefaultIsTerminalRefusesDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	if defaultIsTerminal(f) {
		t.Error("/dev/null counted as a terminal: --ui would run the verb before refusing")
	}
}
