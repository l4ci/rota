package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// The layout tests run with the package dir as cwd, so a call without -C
// resolves the enclosing checkout and reads its .rota/config.local.json
// (a developer's legacy work.accounts key there changes the result). This
// guard fails when any rotaRunWith call in layout_test.go lacks -C.
func TestLayoutTestsPinTheirProjectWithC(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "layout_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "rotaRunWith" {
			return true
		}
		calls++
		pinned := false
		for _, a := range call.Args {
			if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, _ := strconv.Unquote(lit.Value); s == "-C" {
					pinned = true
				}
			}
		}
		if !pinned {
			t.Errorf("%s: rotaRunWith without -C reads the enclosing checkout's config.local.json", fset.Position(call.Pos()))
		}
		return true
	})
	if calls == 0 {
		t.Fatal("found no rotaRunWith calls in layout_test.go: the guard checks nothing")
	}
}
