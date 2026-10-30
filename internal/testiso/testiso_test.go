package testiso

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestScrubHostEnvDropsByPrefix(t *testing.T) {
	for _, k := range []string{"HERDR_FUTURE_VAR", "HERDR_ENV", "TMUX", "TMUX_PANE"} {
		t.Setenv(k, "x")
	}
	t.Setenv("ROTA_KEEP_ME", "x")
	scrubHostEnv()
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HERDR_") || strings.HasPrefix(kv, "TMUX") {
			t.Errorf("host variable survived the scrub: %s", kv)
		}
	}
	if os.Getenv("ROTA_KEEP_ME") == "" {
		t.Error("scrub dropped an unrelated variable")
	}
}

// A HERDR_ variable must not outlive package init, whatever the host sets.
func TestNoHostVarAfterInit(t *testing.T) {
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HERDR_") || strings.HasPrefix(kv, "TMUX") {
			t.Errorf("host variable survived package init: %s", kv)
		}
	}
}

func TestTripwireCatchesBinaryAndReportsHit(t *testing.T) {
	tw, err := NewTripwire("testiso-tw", "herdr")
	if err != nil {
		t.Fatal(err)
	}
	defer tw.Close()
	if tw.Check() != "" {
		t.Fatal("hit before any call")
	}
	out, err := exec.Command("herdr", "tab", "close", "x").CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 99 || !strings.Contains(string(out), "tripwire") {
		t.Fatalf("herdr on PATH is not the tripwire: %v %s", err, out)
	}
	if got := tw.Check(); !strings.Contains(got, "herdr tab close") {
		t.Fatalf("Check = %q", got)
	}
}
