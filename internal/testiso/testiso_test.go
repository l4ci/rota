package testiso

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/hostenv"
)

func TestScrubHostEnvDropsByPrefix(t *testing.T) {
	for _, k := range []string{"HERDR_FUTURE_VAR", "HERDR_ENV", "TMUX", "TMUX_PANE", "HC_TERMINAL_ID", "HC_TERMINAL_SOCKET", "HC_SCOPE", "ORCA_TERMINAL_HANDLE"} {
		t.Setenv(k, "x")
	}
	t.Setenv("ROTA_KEEP_ME", "x")
	hostenv.Scrub()
	for _, kv := range os.Environ() {
		if hostenv.Drop(strings.SplitN(kv, "=", 2)[0]) {
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
		if hostenv.Drop(strings.SplitN(kv, "=", 2)[0]) {
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

// TestGitCeilingStopsAtTMPDIR re-runs this binary with TMPDIR inside a fake
// checkout, as a worker slot's is (#3508). The child's init sets the ceiling; a
// git lookup from a temp dir must not walk up into that checkout.
func TestGitCeilingStopsAtTMPDIR(t *testing.T) {
	if os.Getenv("TESTISO_CEILING_CHILD") == "1" {
		d, err := os.MkdirTemp("", "ceiling-probe")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(d)
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		cmd.Dir = d
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("git resolved a checkout above TMPDIR: %s", out)
		}
		return
	}
	fake, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", fake, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	slotTmp := filepath.Join(fake, ".rota", "tmp", "slots", "x")
	if err := os.MkdirAll(slotTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestGitCeilingStopsAtTMPDIR$")
	cmd.Env = append(os.Environ(), "TESTISO_CEILING_CHILD=1", "TMPDIR="+slotTmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

// Compare the real shell entry point with the tripwire's Go scrub on a fixed
// environment. The explicit survivors also catch both paths dropping too much.
func TestHostEnvScrubMatchesShell(t *testing.T) {
	env := []string{
		"HERDR_PANE_ID=one", "HERDR_FUTURE=two", "HC_SESSION=three",
		"HC_=four", "ORCA_HANDLE=five", "TMUX=six", "TMUX_PANE=seven", "TMUXFUTURE=eight",
		"HERDR=keep", "HC=keep", "HCX=keep", "ORCA=keep", "XHERDR_X=keep", "herdr_x=keep", "ROTA_KEEP_ME=keep",
	}
	var keys []string
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		keys = append(keys, key)
		t.Setenv(key, value)
	}
	hostenv.Scrub()
	var goKept []string
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			goKept = append(goKept, key+"="+value)
		}
	}
	want := []string{"HC=keep", "HCX=keep", "HERDR=keep", "ORCA=keep", "ROTA_KEEP_ME=keep", "XHERDR_X=keep", "herdr_x=keep"}
	sort.Strings(goKept)
	if !reflect.DeepEqual(goKept, want) {
		t.Fatalf("Go survivors = %v, want %v", goKept, want)
	}
	script, err := filepath.Abs("../../test/lib/isolate.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `set -euo pipefail; source "$1"; isolate_env "$2"; env`, "scrub-parity", script, t.TempDir())
	cmd.Env = append(env, "PATH="+os.Getenv("PATH"), "GOCACHE="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell scrub: %v\n%s", err, out)
	}
	var shellKept []string
	for _, kv := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, _, _ := strings.Cut(kv, "=")
		for _, candidate := range keys {
			if key == candidate {
				shellKept = append(shellKept, kv)
			}
		}
	}
	sort.Strings(shellKept)
	if !reflect.DeepEqual(shellKept, want) {
		t.Fatalf("shell survivors = %v, want %v (Go survivors %v)", shellKept, want, goKept)
	}
}
