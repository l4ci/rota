package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
)

// Deps.Getenv is the one env reader in cli: os.Getenv and os.LookupEnv appear
// only in deps.go, where the default Deps binds os.Getenv.
func TestNoRawEnvReadsOutsideDeps(t *testing.T) {
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
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv") {
				t.Errorf("%s: os.%s; read env through Deps.Getenv", fset.Position(sel.Pos()), sel.Sel.Name)
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
	if cd, err = rotastate.CommonDir(root); err != nil {
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
