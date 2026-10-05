package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundWaitVerb(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "2", "--base", "main")
	h := &cliHost{inSession: true}
	useHost(deps, h)

	// No slot has a session yet.
	if code, _, _ := rotaInWith(t, deps, dir, "round", "wait", "--settle", "0"); code != 3 {
		t.Errorf("nothing to watch: %d, want 3", code)
	}
	brief := filepath.Join(t.TempDir(), "b.md")
	os.WriteFile(brief, []byte("hello\n"), 0o644)
	if code, _, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--body-file", brief); code != 0 {
		t.Fatalf("dispatch: %d", code)
	}

	for name, args := range map[string][]string{
		"negative timeout": {"--timeout", "-1"},
		"negative settle":  {"--settle", "-1"},
	} {
		if code, _, _ := rotaInWith(t, deps, dir, append([]string{"round", "wait"}, args...)...); code != 2 {
			t.Errorf("%s: %d, want 2", name, code)
		}
	}
	if code, _, _ := rotaInWith(t, deps, dir, "round", "wait", "ghost", "--settle", "0"); code != 3 {
		t.Errorf("unknown slot: %d, want 3", code)
	}

	// A static pane is idle: the first slot needing attention comes back.
	code, out, _ := rotaInWith(t, deps, dir, "round", "wait", "--settle", "0", "--json")
	d := data(t, out)
	if code != 0 || d["slot"] != "w1" || d["state"] != "idle" || d["source"] != "snapshot" {
		t.Fatalf("wait: %d %v", code, d)
	}
	if _, ok := d["changed"]; ok {
		t.Errorf("round wait is read-only, data has changed: %v", d)
	}
}

func TestRoundWaitTimeoutExitsOneWithTheAnswer(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	h := &movingHost{cliHost: cliHost{inSession: true}}
	useHost(deps, h)
	brief := filepath.Join(t.TempDir(), "b.md")
	os.WriteFile(brief, []byte("hello\n"), 0o644)
	rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--body-file", brief)

	code, out, _ := rotaInWith(t, deps, dir, "round", "wait", "--settle", "0", "--timeout", "0.05", "--json")
	d := data(t, out)
	rows, _ := d["slots"].([]any)
	if code != 1 || d["timedOut"] != true || len(rows) != 1 || rows[0].(map[string]any)["state"] != "busy" {
		t.Fatalf("timeout: %d %v", code, d)
	}
}

// movingHost's pane changes on every capture, so the slot is always busy.
type movingHost struct {
	cliHost
	n int
}

func (h *movingHost) Capture(context.Context, string, string, int) string {
	h.n++
	return string(rune('a' + h.n%26))
}
