package version

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveLinkTimeWins(t *testing.T) {
	bi := &debug.BuildInfo{Main: debug.Module{Version: "v5.1.0"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "ffffffffffff"}}}
	got := resolve("5.0.0", "abc1234", "2026-10-02", bi)
	if got.Version != "5.0.0" || got.Commit != "abc1234" || got.Date != "2026-10-02" {
		t.Fatalf("link-time values must win, got %+v", got)
	}
}

func TestResolveFromBuildInfo(t *testing.T) {
	bi := &debug.BuildInfo{Main: debug.Module{Version: "v5.0.0"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef"},
		{Key: "vcs.time", Value: "2026-10-02T10:00:00Z"},
	}}
	got := resolve("", "", "", bi)
	if got.Version != "5.0.0" || got.Commit != "0123456" || got.Date != "2026-10-02T10:00:00Z" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveDevel(t *testing.T) {
	got := resolve("", "", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}})
	if got.Version != "dev" {
		t.Fatalf("a (devel) build reports dev, got %q", got.Version)
	}
	if resolve("", "", "", nil).Version != "dev" {
		t.Fatal("no build info reports dev")
	}
}

// TestLDFlagsSpellingsMatch is the drift check for the hand-copied -X targets
// in scripts and config: every spelling must equal Symbol of a variable that
// really exists in this package.
func TestLDFlagsSpellingsMatch(t *testing.T) {
	root := filepath.Join("..", "..")
	re := regexp.MustCompile(`-X\s+(github\.com/l4ci/rota/\S+)=`)
	known := map[string]bool{}
	for _, n := range []string{"Version", "Commit", "Date"} {
		known[Symbol(n)] = true
	}
	// Symbol's names must be the declared vars.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "version.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.VAR {
			for _, sp := range g.Specs {
				for _, id := range sp.(*ast.ValueSpec).Names {
					declared[id.Name] = true
				}
			}
		}
	}
	for _, n := range []string{"Version", "Commit", "Date"} {
		if !declared[n] {
			t.Errorf("var %s is not declared in version.go", n)
		}
		if got, want := Symbol(n), reflect.TypeOf(Info{}).PkgPath()+"."+n; got != want {
			t.Errorf("Symbol(%q) = %q, want %q", n, got, want)
		}
	}
	seen := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "node_modules", ".rota":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "internal/version/version_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil || len(b) > 1<<20 {
			return nil
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			seen++
			if !known[m[1]] {
				t.Errorf("%s spells ldflags symbol %q, not one of this package's variables", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("found no -X spellings: the scan is broken")
	}
}
