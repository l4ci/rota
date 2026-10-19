package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

// hostRoot is a bare project the verbs resolve a host in, with the process
// cwd inside it.
func hostRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	return root
}

// notifyHerdr is a herdr that records Notify.
type notifyHerdr struct {
	cliHost
	notes []string
}

func (*notifyHerdr) Kind() host.Kind { return host.Herdr }
func (*notifyHerdr) Require() error  { return nil }
func (h *notifyHerdr) Notify(_ context.Context, title, _ string) {
	h.notes = append(h.notes, title)
}

// The host decision reads Deps' getenv and lookPath, not the process's: a
// process env that says tmux must not leak into a test that fakes herdr.
func TestPaletteHeaderHostFollowsDepsEnv(t *testing.T) {
	root := hostRoot(t)
	t.Setenv("TMUX", "/tmp/tmux-real")
	t.Setenv("HERDR_ENV", "")
	deps := testDeps()
	deps.Getenv = func(k string) string {
		if k == "HERDR_ENV" {
			return "1"
		}
		return ""
	}
	deps.LookPath = func(string) (string, error) { return "/fake/herdr", nil }
	c := &Ctx{Deps: deps, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
	if got := paletteHeader(c, true).Host; got != host.Herdr.String() {
		t.Errorf("palette host = %q in %s, want herdr from Deps env", got, root)
	}
}

func TestKeepaliveNotifyHostFollowsDepsEnv(t *testing.T) {
	root := hostRoot(t)
	t.Setenv("HERDR_ENV", "")
	t.Setenv("TMUX", "")
	fake := &notifyHerdr{}
	deps := testDeps()
	deps.Host = func(host.Kind) host.Host { return fake }
	c := &Ctx{Deps: deps, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}

	keepaliveNotify(context.Background(), c, root, "none", "")
	if len(fake.notes) != 0 {
		t.Fatalf("notified with no herdr in Deps env: %v", fake.notes)
	}
	deps.Getenv = func(k string) string {
		if k == "HERDR_ENV" {
			return "1"
		}
		return ""
	}
	deps.LookPath = func(string) (string, error) { return "/fake/herdr", nil }
	keepaliveNotify(context.Background(), c, root, "herdr", "")
	if len(fake.notes) != 1 {
		t.Errorf("notes = %v, want one once Deps env names herdr", fake.notes)
	}
}
