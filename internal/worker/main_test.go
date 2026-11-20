package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/testiso"
)

// TestMain puts tripwire herdr, tmux, gh and glab first on PATH (the host env
// is scrubbed by testiso). This round runs inside herdr and tmux, where a stray `herdr tab
// close` or `tmux kill-window` kills live agents: any test that reaches a real
// host binary fails the whole run.
func TestMain(m *testing.M) {
	tw, err := testiso.NewTripwire("worker-tripwire", "herdr", "tmux", "gh", "glab", "codex")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// The gate's local merge commits as the caller. CI runners have no git
	// identity, so the tests bring their own.
	gittest.SetIdentity()
	os.Unsetenv("ROTA_ACCOUNT_USAGE_DIR")
	code := m.Run()
	if hit := tw.Check(); hit != "" {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd a real host or forge binary from PATH:\n%s", hit)
		code = 1
	}
	tw.Close()
	os.Exit(code)
}

// ── fixtures shared by the tests ────────────────────────────────────────────

// sh runs a non-git command in dir; git goes through gittest.Run.
func sh(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// registry returns workers.json with the project path replaced by ROOT, so a
// project in any temp dir compares byte for byte with a golden.
func registry(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(RegistryPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), dir, "ROOT")
}

func mustEqual(t *testing.T, what, want, got string) {
	t.Helper()
	if want != got {
		t.Errorf("%s differs\n--- want\n%s\n--- got\n%s", what, want, got)
	}
}

var bg = context.Background()
