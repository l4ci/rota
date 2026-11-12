// Package blockedbytest reads the set of declared blockedBy values for tests.
// The set is derived from the constants in blockedby.go, so no hand-kept list
// has to stay in step with them.
package blockedbytest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// Declared is every string const in internal/blockedby/blockedby.go, as a set
// of values. A value declared by two consts fails the test.
func Declared(t testing.TB) map[string]bool {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate blockedby.go")
	}
	src := filepath.Join(filepath.Dir(self), "..", "blockedby.go")
	f, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	ast.Inspect(f, func(node ast.Node) bool {
		gd, ok := node.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			return true
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				b, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || b.Kind != token.STRING {
					continue
				}
				v, _ := strconv.Unquote(b.Value)
				if set[v] {
					t.Errorf("const %s repeats the value %q", name.Name, v)
				}
				set[v] = true
			}
		}
		return false
	})
	if len(set) == 0 {
		t.Fatal("blockedby.go declares no string consts")
	}
	return set
}
