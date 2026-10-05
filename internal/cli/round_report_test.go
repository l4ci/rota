package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

func recordRoundHost(t *testing.T, dir, h string) {
	t.Helper()
	def := jsonx.NewObject()
	def.Set("slots", []any{})
	if err := worker.Update(dir, def, func(doc *jsonx.Object) { doc.Set("host", h) }); err != nil {
		t.Fatal(err)
	}
}

func TestRoundReportVerb(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "2", "--base", "main")
	useHost(deps, &cliHost{inSession: true})

	// No round host recorded: exit 2.
	if code, _, _ := rotaInWith(t, deps, dir, "round", "report", "w1", "--state", "done"); code != 2 {
		t.Errorf("no round: %d, want 2", code)
	}
	recordRoundHost(t, dir, "tmux")
	if code, _, _ := rotaInWith(t, deps, dir, "round", "report", "w1", "--state", "done"); code != 2 {
		t.Errorf("tmux round: %d, want 2", code)
	}
	recordRoundHost(t, dir, "solo")

	url := "https://github.com/o/r/pull/3"
	code, out, _ := rotaInWith(t, deps, dir, "round", "report", "w1", "--state", "done", "--pr", url, "--evidence", "ok", "--json")
	d := data(t, out)
	if code != 0 || d["slot"] != "w1" || d["state"] != "done" || d["previous"] != "idle" || d["pr"] != url || d["evidence"] != "ok" || d["changed"] != true {
		t.Fatalf("report: %d %v", code, d)
	}
	code, out, _ = rotaInWith(t, deps, dir, "round", "report", "w1", "--state", "done", "--pr", url, "--json")
	if d = data(t, out); code != 0 || d["changed"] != false {
		t.Errorf("repeat: %d %v", code, d)
	}
	for name, args := range map[string][]string{
		"busy":       {"w1", "--state", "busy"},
		"no state":   {"w1"},
		"no slot":    {"--state", "done"},
		"bad pr":     {"w1", "--state", "done", "--pr", "later"},
		"unknown":    {"ghost", "--state", "done"},
		"two slots":  {"w1", "w2", "--state", "done"},
		"bad (3)":    {"ghost", "--state", "bogus"},
		"unknown ok": {"ghost", "--state", "dead"},
	} {
		want := 2
		if name == "unknown" || name == "unknown ok" {
			want = 3
		}
		if code, _, _ := rotaInWith(t, deps, dir, append([]string{"round", "report"}, args...)...); code != want {
			t.Errorf("%s: %d, want %d", name, code, want)
		}
	}

	// round wait reads the recorded state and never blocks.
	rotaInWith(t, deps, dir, "round", "report", "w2", "--state", "blocked")
	code, out, _ = rotaInWith(t, deps, dir, "round", "wait", "--json")
	if d = data(t, out); code != 0 || d["slot"] != "w1" || d["state"] != "done" || d["source"] != "registry" {
		t.Errorf("wait: %d %v", code, d)
	}
}

func TestPaneVerbsExitTwoUnderSolo(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	h := &cliHost{inSession: true}
	useHost(deps, h)
	recordRoundHost(t, dir, "solo")
	brief := filepath.Join(t.TempDir(), "b.md")
	os.WriteFile(brief, []byte("hello\n"), 0o644)
	for name, args := range map[string][]string{
		"dispatch":       {"worker", "dispatch", "w1", "--body-file", brief},
		"dispatch relay": {"worker", "dispatch", "w1", "--relay", "--body-file", brief},
		"poll":           {"worker", "poll"},
		"session check":  {"worker", "session", "check"},
		"session ensure": {"worker", "session", "ensure"},
		"account pick":   {"worker", "account", "pick"},
		"account assign": {"worker", "account", "assign", "w1"},
	} {
		code, _, errOut := rotaInWith(t, deps, dir, args...)
		if code != 2 {
			t.Errorf("%s: %d, want 2 (%s)", name, code, errOut)
		}
	}
}
