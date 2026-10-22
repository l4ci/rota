package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// keys.go owns the defaults of the tracker labels and issues.retryWaitSeconds.
// round and tracker read them from the loaded config; a copy of one in their
// production code would drift when keys.go changes.
//
// The other labels are not guarded: round and tracker also write "in-progress",
// "changes-requested", "blocked" and "released" as backlog states and release
// markers, different things that happen to share the spelling.
func TestNoLabelOrRetryDefaultRestatedOutsideConfig(t *testing.T) {
	banned := map[string]string{}
	for _, role := range []string{"needsReview", "notPlanned", "needsHuman", "support"} {
		banned[strconv.Quote(Label(nil, role))] = "issues.labels." + role
	}
	for _, pkg := range []string{"round", "tracker"} {
		dir := filepath.Join("..", pkg)
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources under %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, f, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind == token.STRING {
						if key, ok := banned[x.Value]; ok {
							t.Errorf("%s: %s restates the default of %s; read it from the loaded config", fset.Position(x.Pos()), x.Value, key)
						}
					}
				case *ast.BinaryExpr:
					if l, ok := x.X.(*ast.BasicLit); ok && x.Op == token.MUL && l.Value == "60" {
						if sel, ok := x.Y.(*ast.SelectorExpr); ok && sel.Sel.Name == "Second" {
							t.Errorf("%s: 60 * time.Second restates the default of issues.retryWaitSeconds", fset.Position(x.Pos()))
						}
					}
				}
				return true
			})
		}
	}
}
