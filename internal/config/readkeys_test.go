package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// schemaReaders are the config functions that resolve a key against the
// schema. Lookup is left out: it reads keys as written, legacy ones included.
var schemaReaders = map[string]bool{
	"String": true, "StringIfSet": true, "StringOrDefault": true,
	"IntOr": true, "Int": true, "Bool": true, "Value": true,
}

// TestReadKeysExistInSchema fails when code outside internal/config reads a
// literal key that is not in the schema, so a typo cannot read as unset.
func TestReadKeysExistInSchema(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	reads := 0
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "config" || d.Name() == "testdata") {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !schemaReaders[sel.Sel.Name] {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "config" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(lit.Value)
			reads++
			if !IsSchemaKey(key) {
				t.Errorf("%s: config.%s reads %q, which is not in the schema", fset.Position(call.Pos()), sel.Sel.Name, key)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reads < 20 {
		t.Fatalf("found %d literal key reads; the scan is not seeing the callers", reads)
	}
}

func TestStringUnknownKeyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("String on an unknown key returned instead of failing")
		}
	}()
	String(nil, "no.such.key")
}
