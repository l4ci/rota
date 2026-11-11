package testiso

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/hostenv"
)

func TestScrubHostEnvDropsByPrefix(t *testing.T) {
	for _, k := range []string{"HERDR_FUTURE_VAR", "HERDR_ENV", "TMUX", "TMUX_PANE", "HC_TERMINAL_ID", "HC_TERMINAL_SOCKET", "HC_SCOPE", "ORCA_TERMINAL_HANDLE"} {
		t.Setenv(k, "x")
	}
	t.Setenv("ROTA_KEEP_ME", "x")
	scrubHostEnv()
	for _, kv := range os.Environ() {
		if hostenv.IsKV(kv) {
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
		if hostenv.IsKV(kv) {
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

// Close must hand PATH and TMPDIR back as they were, set or unset, so later
// tests in the process never see a deleted dir.
func TestTripwireCloseRestoresPathAndTmpdir(t *testing.T) {
	cases := []struct {
		name   string
		tmpdir *string // nil = unset before NewTripwire
	}{
		{"set", ptr(t.TempDir())},
		{"unset", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PATH", "/usr/bin:/bin")
			t.Setenv("TMPDIR", "x") // registers restore of the real value
			if c.tmpdir == nil {
				os.Unsetenv("TMPDIR")
			} else {
				os.Setenv("TMPDIR", *c.tmpdir)
			}
			tw, err := NewTripwire("testiso-restore", "herdr")
			if err != nil {
				t.Fatal(err)
			}
			tw.Close()
			if got := os.Getenv("PATH"); got != "/usr/bin:/bin" {
				t.Errorf("PATH after Close = %q", got)
			}
			got, ok := os.LookupEnv("TMPDIR")
			if c.tmpdir == nil && ok {
				t.Errorf("TMPDIR set after Close: %q", got)
			}
			if c.tmpdir != nil && got != *c.tmpdir {
				t.Errorf("TMPDIR after Close = %q, want %q", got, *c.tmpdir)
			}
		})
	}
}

func ptr(s string) *string { return &s }
