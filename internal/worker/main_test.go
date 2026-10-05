package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

var tripwireHit string

// TestMain puts tripwire herdr, tmux, gh and glab first on PATH and clears the
// host env. This round runs inside herdr and tmux, where a stray `herdr tab
// close` or `tmux kill-window` kills live agents: any test that reaches a real
// host binary fails the whole run.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "worker-tripwire")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tripwireHit = filepath.Join(dir, "hit")
	for _, bin := range []string{"herdr", "tmux", "gh", "glab", "codex"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"$0 $*\" >> '%s'\necho 'tripwire: real %s reached from a test' >&2\nexit 99\n", tripwireHit, bin)
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Root the temp dirs of child processes (the fake forge and host scripts
	// mktemp) under the tripwire dir, which is removed below (#110).
	if tmp := filepath.Join(dir, "tmp"); os.Mkdir(tmp, 0o755) == nil {
		os.Setenv("TMPDIR", tmp)
	}
	// The gate's local merge commits as the caller. CI runners have no git
	// identity, so the tests bring their own.
	gittest.SetIdentity()
	for _, k := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_WORKSPACE_ID", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "ROTA_ACCOUNT_USAGE_DIR"} {
		os.Unsetenv(k)
	}
	code := m.Run()
	if b, err := os.ReadFile(tripwireHit); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd a real host or forge binary from PATH:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
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

// newProject makes a git project with one commit on main, a gitignored
// .worktrees/ and the given .rota/config.json. The path is symlink-resolved.
func newProject(t *testing.T, config string) string {
	t.Helper()
	dir := gittest.TempDir(t)
	gittest.Init(t, dir, "main")
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".worktrees/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(config), 0o644)
	gittest.Run(t, dir, "add", ".gitignore", "seed.txt")
	gittest.Run(t, dir, "commit", "-q", "-m", "seed")
	return dir
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

func TestTripwireCatchesRealHostBinary(t *testing.T) {
	out, err := exec.Command("herdr", "tab", "close", "x").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "tripwire") {
		t.Fatalf("herdr on PATH is not the tripwire: %s", out)
	}
	os.Remove(tripwireHit) // hit on purpose
}
