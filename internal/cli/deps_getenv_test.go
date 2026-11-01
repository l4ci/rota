package cli

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
)

// OS environment, process identity and directory reads belong at the Deps boundary.
func TestNoRawMachineReadsOutsideDeps(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "deps.go" {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv" || sel.Sel.Name == "Getpid" || sel.Sel.Name == "TempDir" || sel.Sel.Name == "UserCacheDir") {
				t.Errorf("%s: os.%s; read the machine through Deps", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

// heldLeaseDeps holds a live lease for a fake pid 4242 and returns Deps whose
// Getenv records every key it is asked for.
func heldLeaseDeps(t *testing.T) (c *Ctx, root, cd string, keys *[]string) {
	t.Helper()
	root = orchProject(t, false)
	le := roundlease.DefaultEnv()
	le.Alive = func(p int) bool { return p == 4242 }
	le.StartTime = func(p int) (uint64, bool) { return 99, p == 4242 }
	var err error
	if cd, err = rotastatetest.CommonDir(root); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := le.Acquire(cd, root, roundlease.Holder{PID: 4242, Start: 99}, 1); err != nil {
		t.Fatal(err)
	}
	keys = new([]string)
	d := testDeps()
	d.LeaseEnv = func() roundlease.Env { return le }
	d.HolderPID = func() int { return 4242 }
	d.Getenv = func(k string) string {
		*keys = append(*keys, k)
		return os.Getenv(k)
	}
	return &Ctx{Deps: d}, root, cd, keys
}

func sawKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// The limit lease check reads the holder's pane env through the injected Getenv.
func TestLimitWatchGuardReadsInjectedEnv(t *testing.T) {
	c, root, cd, keys := heldLeaseDeps(t)
	if _, err := limitWatchGuard(c, root, cd); err != nil {
		t.Fatalf("holder 4242 holds the lease: %v", err)
	}
	if !sawKey(*keys, "TMUX_PANE") {
		t.Errorf("injected Getenv not consulted for the pane, saw %v", *keys)
	}
}

// The autopilot lease check does the same: the lease is held, so it passes
// the check, and the pane lookup went through the injected Getenv.
func TestAutopilotTickLeaseCheckReadsInjectedEnv(t *testing.T) {
	c, root, _, keys := heldLeaseDeps(t)
	_, err := autopilotTick(c, root, roundcfg.Settings{}, "", 4242)
	if err == errAutopilotStopped {
		t.Fatalf("holder 4242 holds the lease, tick reported stopped")
	}
	if !sawKey(*keys, "TMUX_PANE") {
		t.Errorf("injected Getenv not consulted for the pane, saw %v", *keys)
	}
}

// A lease lookup error is the guard's own unavailable error, not a "no lease" refusal.
func TestLimitWatchGuardLookupErrorIsUnavailable(t *testing.T) {
	c, root, cd, _ := heldLeaseDeps(t)
	p := roundlease.Path(cd)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0o755); err != nil { // a directory where the file belongs: ReadFile fails
		t.Fatal(err)
	}
	_, err := limitWatchGuard(c, root, cd)
	var ce *Error
	if !errors.As(err, &ce) || ce.Exit != ExitUnavailable {
		t.Fatalf("lookup error: want ExitUnavailable, got %v", err)
	}
}

// A watcher owns its current process marker even when the lease holder is an ancestor.
func TestLimitWatchGuardUsesInjectedPID(t *testing.T) {
	c, root, cd, _ := heldLeaseDeps(t)
	le := c.deps().LeaseEnv()
	le.Alive = func(pid int) bool { return pid == 4242 || pid == 7777 }
	le.StartTime = func(pid int) (uint64, bool) { return 99, pid == 4242 || pid == 7777 }
	c.deps().LeaseEnv = func() roundlease.Env { return le }
	c.deps().PID = func() int { return 7777 }
	rec := ownWatching(c, limits.ModeWatch)
	if rec.PID != 7777 || rec.Start != 99 || rec.Host != le.Host {
		t.Fatalf("watcher identity = %+v, want current pid 7777, start 99 on %s", rec, le.Host)
	}
	if err := limits.WriteWatching(cd, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := limitWatchGuard(c, root, cd); err != nil {
		t.Fatalf("own watcher refused: %v", err)
	}
	c.deps().PID = func() int { return 8888 }
	if _, err := limitWatchGuard(c, root, cd); err == nil {
		t.Fatal("another process's watcher allowed")
	}
}
