// Package blockedbytest reads the set of declared blockedBy values for tests.
// The set is derived from the constants in blockedby.go, so no hand-kept list
// has to stay in step with them.
package blockedbytest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Declared is every string const in internal/blockedby/blockedby.go, as a set
// of values. A value declared by two consts fails the test.
func Declared(t testing.TB) map[string]bool {
	t.Helper()
	src := filepath.Join(moduleRoot(t), "internal", "blockedby", "blockedby.go")
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
				if i >= len(vs.Values) {
					continue
				}
				b, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || b.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(b.Value)
				if err != nil {
					t.Errorf("const %s: %v", name.Name, err)
					continue
				}
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

// moduleRoot walks up from the test's working directory to the go.mod, which
// holds under -trimpath where runtime.Caller names no file on disk.
func moduleRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}
