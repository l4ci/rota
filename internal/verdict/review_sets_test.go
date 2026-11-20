package verdict

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// AC-2 is an architecture boundary: callers select named queries instead of
// reconstructing the latest-record loop for their own list of review kinds.
func TestReviewKindLoopsStayInVerdict(t *testing.T) {
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == filepath.Join("..", "verdict") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		var alias string
		for _, imp := range file.Imports {
			if imp.Path.Value == `"github.com/l4ci/rota/internal/verdict"` {
				alias = "verdict"
				if imp.Name != nil {
					alias = imp.Name.Name
				}
			}
		}
		if alias == "" {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			loop, ok := node.(*ast.RangeStmt)
			if !ok {
				return true
			}
			ast.Inspect(loop.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Latest" {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if ok && pkg.Name == alias {
					t.Errorf("%s: review-kind lookup loop belongs in a named verdict query", fset.Position(call.Pos()))
				}
				return true
			})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The relay rule reads the non-core remainders by name. Core kinds lead each
// set, and each remainder is its set minus the core kinds, so a lens or cross
// kind is never lost or a core kind mistaken for one.
func TestNonCoreKindSetsAreSetsMinusCore(t *testing.T) {
	for name, c := range map[string]struct{ full, rest []string }{
		"bounce":    {bounceReviewKinds, relayNonCoreKinds},
		"readiness": {readinessReviewKinds, relayNonCoreReadinessKinds},
	} {
		if len(c.full) < len(coreReviewKinds) || !slices.Equal(c.full[:len(coreReviewKinds)], coreReviewKinds) {
			t.Fatalf("%s: core kinds %v must lead %v", name, coreReviewKinds, c.full)
		}
		want := slices.DeleteFunc(slices.Clone(c.full), IsCoreReview)
		if !slices.Equal(c.rest, want) {
			t.Fatalf("%s: non-core = %v, want %v", name, c.rest, want)
		}
		for _, k := range LensKinds {
			if !slices.Contains(c.rest, k) {
				t.Fatalf("%s: lens %q missing from relay set", name, k)
			}
		}
	}
}
