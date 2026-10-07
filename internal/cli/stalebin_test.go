package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/version"
)

func staleRepo(t *testing.T, module string) (dir, commit string) {
	t.Helper()
	dir = gittest.NewRepo(t, "main")
	commit = gittest.Commit(t, dir, "mod", "go.mod", "module "+module+"\n\ngo 1.22\n")
	gittest.Commit(t, dir, "verb fix", "internal/x/x.go", "package x\n")
	return dir, commit
}

func TestStaleBinaryOnlyInRotaCheckout(t *testing.T) {
	dir, commit := staleRepo(t, "github.com/l4ci/rota")
	f, ok := staleBinary(context.Background(), git.Exec, dir)
	if ok {
		t.Fatalf("a binary with no commit stamp cannot be compared: %+v", f)
	}
	old := version.Commit
	version.Commit = commit
	t.Cleanup(func() { version.Commit = old })
	f, ok = staleBinary(context.Background(), git.Exec, dir)
	if !ok || f.Behind != 1 || !strings.Contains(f.Rebuild, "Version=$(cat VERSION)") {
		t.Fatalf("rota checkout: ok=%v finding=%+v", ok, f)
	}
	other, oc := staleRepo(t, "example.com/other")
	version.Commit = oc
	if _, ok := staleBinary(context.Background(), git.Exec, other); ok {
		t.Error("outside rota's own repo nothing may change")
	}
}

func TestStaleBinaryWatchWarnsOncePerRound(t *testing.T) {
	dir, commit := staleRepo(t, "github.com/l4ci/rota")
	old := version.Commit
	version.Commit = commit
	t.Cleanup(func() { version.Commit = old })
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	cd := dir + "/.git"
	c := &Ctx{}
	if _, ok := staleBinaryOncePerRound(c, cd, 7); !ok {
		t.Fatal("first heartbeat of a round must warn")
	}
	if _, ok := staleBinaryOncePerRound(c, cd, 7); ok {
		t.Error("the same round must not warn twice")
	}
	if _, ok := staleBinaryOncePerRound(c, cd, 8); !ok {
		t.Error("a new round warns again")
	}
}
