package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

type adoptLookupForge struct {
	round.Forge
	err error
}

func (f adoptLookupForge) OpenPRs(context.Context) ([]tracker.PR, error) {
	return nil, f.err
}

func TestAdoptWarnsWhenOpenPRLookupFails(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(strconv.FormatBool(asJSON), func(t *testing.T) {
			dir, deps := adoptProject(t)
			base := deps.RoundEnv
			deps.RoundEnv = func(ctx context.Context, root string) round.Env {
				e := base(ctx, root)
				e.Forge = adoptLookupForge{err: errors.New("forge connection refused")}
				return e
			}
			args := []string{"worker", "adopt", "codex/b01-a", "--issue", "B01"}
			if asJSON {
				args = append(args, "--json")
			}
			code, out, errOut := rotaInWith(t, deps, dir, args...)
			if code != 0 {
				t.Fatalf("adopt failed: %d %s %s", code, out, errOut)
			}
			if !strings.Contains(errOut, "open PRs of codex/b01-a") || !strings.Contains(errOut, "forge connection refused") || !strings.Contains(errOut, "rota round reconcile --apply") {
				t.Errorf("missing lookup warning: %q", errOut)
			}
			if asJSON {
				var envelope struct{ Warnings []string }
				if err := json.Unmarshal([]byte(out), &envelope); err != nil || len(envelope.Warnings) != 1 || !strings.Contains(envelope.Warnings[0], "forge connection refused") {
					t.Errorf("missing JSON lookup warning: %s (decode: %v)", out, err)
				}
			}
			s := worker.LoadRegistryTolerant(dir).Slot("ext-1")
			if s == nil || s.PR() != "" || s.Task() != "B01" {
				t.Fatalf("failed lookup should still adopt without a PR: %v", s)
			}
		})
	}
}

func adoptProject(t *testing.T) (string, *Deps) {
	t.Helper()
	deps := testDeps()
	dir := workerProject(t, `{}`)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("## Bugs\n- **[B01] [P1] a.** x Captured: 2026-01-01\n- **[B02] [P1] b.** y Captured: 2026-01-01\n"), 0o644)
	c := exec.Command("git", "worktree", "add", "-q", "-b", "codex/b01-a", filepath.Join(dir, ".worktrees", "cx"), "HEAD")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return dir, deps
}

func TestAdoptVerb(t *testing.T) {
	dir, deps := adoptProject(t)
	code, out, _ := rotaInWith(t, deps, dir, "worker", "adopt", "codex/b01-a", "--issue", "B01", "--pr", "https://x/pull/3")
	if code != 0 || out != "adopted ext-1 codex/b01-a #B01\n" {
		t.Fatalf("%d %q", code, out)
	}
	s := worker.LoadRegistryTolerant(dir).Slot("ext-1")
	if s == nil || !s.IsExternal() || s.Handle() != "" || s.Task() != "B01" || s.PR() != "https://x/pull/3" {
		t.Fatalf("slot %v", s)
	}
	code, out, _ = rotaInWith(t, deps, dir, "worker", "adopt", "codex/b01-a", "--issue", "B02", "--json")
	if d := data(t, out); code != 4 || d["blockedBy"] != "registered" || d["changed"] != false {
		t.Errorf("registered: %d %s", code, out)
	}
	code, out, _ = rotaInWith(t, deps, dir, "worker", "adopt", filepath.Join(dir, ".worktrees", "cx"), "--issue", "B01", "--name", "ext-9", "--json")
	if d := data(t, out); code != 4 || d["blockedBy"] != "registered" {
		t.Errorf("registered by path: %d %s", code, out)
	}
}

func TestAdoptVerbUsageAndForeignPath(t *testing.T) {
	dir, deps := adoptProject(t)
	for _, args := range [][]string{
		{"worker", "adopt", "codex/b01-a"},
		{"worker", "adopt", "--issue", "B01"},
		{"worker", "adopt", t.TempDir(), "--issue", "B01"},
		{"worker", "adopt", "nope", "--issue", "B01"},
	} {
		if code, _, _ := rotaInWith(t, deps, dir, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if worker.LoadRegistryTolerant(dir).Slot("ext-1") != nil {
		t.Error("a refused adopt registered a slot")
	}
}

func TestAdoptVerbThenDispatchExternal(t *testing.T) {
	dir, deps := adoptProject(t)
	rotaInWith(t, deps, dir, "worker", "adopt", "codex/b01-a", "--issue", "B01")
	useHost(deps, &cliHost{inSession: true})
	brief := filepath.Join(t.TempDir(), "b.md")
	os.WriteFile(brief, []byte("x"), 0o644)
	code, out, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "ext-1", "--body-file", brief, "--task", "B01", "--json")
	if d := data(t, out); code != 4 || d["blockedBy"] != "host" {
		t.Errorf("%d %s", code, out)
	}
}
