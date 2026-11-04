package cli

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestWorkerGateStatusDoneOnPass(t *testing.T) {
	dir := workerProject(t, `{"ship":{"review":"none"},"test":{"full":["test -f feature.txt"]}}`)
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	wt := filepath.Join(dir, ".worktrees", "w1")
	write(t, filepath.Join(wt, "feature.txt"), "f")
	gittest.Run(t, wt, "add", "feature.txt")
	gittest.Run(t, wt, "commit", "-q", "-m", "feature")
	branch := strings.TrimSpace(gittest.Run(t, wt, "rev-parse", "--abbrev-ref", "HEAD"))
	rotaIn(t, dir, "verdict", "add", branch, "--kind", "review-spec", "--verdict", "PASS")
	pinDir(t, dir, func(d *Deps) { d.IsTerminal = func(any) bool { return true } })
	code, errs := runTTY(t, dir, "worker", "gate", "w1", "--base", "main")
	if code != 0 {
		t.Fatalf("gate: %d %s", code, errs)
	}
	se := bytes.NewBufferString(errs)
	w, d := osc7501("working", "gate w1", -1), osc7501("done", "gate w1", -1)
	if i, j := strings.Index(se.String(), w), strings.Index(se.String(), d); i < 0 || j < i {
		t.Errorf("stderr %q lacks working then done", se.String())
	}
}

func TestOSC7501Format(t *testing.T) {
	msg := base64.StdEncoding.EncodeToString([]byte("gate w1"))
	for _, c := range []struct {
		state string
		pct   int
		want  string
	}{
		{"working", -1, "\x1b]7501;state=working:app=rota:msg=" + msg + "\x1b\\"},
		{"working", 40, "\x1b]7501;state=working:app=rota:progress=40:msg=" + msg + "\x1b\\"},
		{"working", 400, "\x1b]7501;state=working:app=rota:progress=100:msg=" + msg + "\x1b\\"},
		{"done", -1, "\x1b]7501;state=done:app=rota:msg=" + msg + "\x1b\\"},
		{"error", -1, "\x1b]7501;state=error:app=rota:msg=" + msg + "\x1b\\"},
	} {
		if got := osc7501(c.state, "gate w1", c.pct); got != c.want {
			t.Errorf("osc7501(%s,%d) = %q, want %q", c.state, c.pct, got, c.want)
		}
	}
	if got := osc7501("done", "", -1); got != "\x1b]7501;state=done:app=rota\x1b\\" {
		t.Errorf("no msg: %q", got)
	}
}

func statusCtx(tty, json bool) (*Ctx, *bytes.Buffer) {
	var so, se bytes.Buffer
	d := testDeps()
	d.IsTerminal = func(any) bool { return tty }
	return &Ctx{Path: "rota x", Stdout: &so, Stderr: &se, Deps: d, JSON: json}, &se
}

func TestTrackStatusSilentOffTerminal(t *testing.T) {
	for name, c := range map[string]func() (*Ctx, *bytes.Buffer){
		"pipe": func() (*Ctx, *bytes.Buffer) { return statusCtx(false, false) },
		"json": func() (*Ctx, *bytes.Buffer) { return statusCtx(true, true) },
	} {
		ctx, se := c()
		st := ctx.trackStatus("m")
		st.Progress(50)
		st.Finish(true)
		if se.Len() != 0 {
			t.Errorf("%s: wrote %q", name, se.String())
		}
	}
}

func TestTrackStatusWorkingThenDoneOrError(t *testing.T) {
	for _, ok := range []bool{true, false} {
		ctx, se := statusCtx(true, false)
		st := ctx.trackStatus("m")
		st.Finish(ok)
		st.Finish(!ok) // only the first end counts
		want := osc7501("working", "m", -1) + osc7501(map[bool]string{true: "done", false: "error"}[ok], "m", -1)
		if se.String() != want {
			t.Errorf("ok=%v: %q, want %q", ok, se.String(), want)
		}
	}
}

// The verb path: a refused worker gate leaves working then error on a
// terminal, and nothing on a pipe or under --json.
func TestWorkerGateStatusRecords(t *testing.T) {
	dir := workerProject(t, `{"ship":{"review":"none"},"test":{"full":["test -f feature.txt"]}}`)
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	// no commit on the slot: the gate cannot pass
	tty := func(on bool) func(*Deps) { return func(d *Deps) { d.IsTerminal = func(any) bool { return on } } }
	run := func(args ...string) (int, string) { return runTTY(t, dir, args...) }
	args := []string{"worker", "gate", "w1", "--base", "main"}

	pinDir(t, dir, tty(false))
	if code, errs := run(args...); code == 0 || strings.Contains(errs, "\x1b]7501") {
		t.Fatalf("pipe: code %d, stderr %q", code, errs)
	}

	pinDir(t, dir, tty(true))
	code, errs := run(args...)
	w, e := osc7501("working", "gate w1", -1), osc7501("error", "gate w1", -1)
	if code == 0 || !strings.Contains(errs, w) || !strings.Contains(errs, e) || strings.Index(errs, w) > strings.Index(errs, e) {
		t.Fatalf("tty: code %d, stderr %q", code, errs)
	}
	if _, errs := run(append([]string{"--json"}, args...)...); strings.Contains(errs, "\x1b]7501") {
		t.Fatalf("json: stderr %q", errs)
	}
}

// runTTY runs rota in dir with the pins registered for it, returning the exit
// code and stderr.
func runTTY(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var so, se bytes.Buffer
	code := mainWith(depsIn(dir), args, strings.NewReader(""), &so, &se)
	return code, se.String()
}
