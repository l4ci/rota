package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Kind-to-adapter table is the one place per-kind facts live (#3415,
// #3765). These guards keep it that way: a Kind constant with no row fails,
// and so does a switch or map keyed on a Kind outside policy.go.

// kindConstNames parses the non-test sources of this package and returns the
// names of constants declared with type Kind. It reads the source, not the
// table, so a constant the table forgot is still seen.
func kindConstNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, f := range parsePackage(t) {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "Kind" {
					for _, n := range vs.Names {
						names[n.Name] = true
					}
				}
			}
		}
	}
	return names
}

// parsePackage returns the parsed non-test .go files of internal/host, keyed
// by file name.
func parsePackage(t *testing.T) map[string]*ast.File {
	t.Helper()
	files := map[string]*ast.File{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != "." {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			return err
		}
		files[p] = f
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestEveryKindHasADescriptor fails when a Kind constant has no row in the
// table. Solo is the one Kind with no adapter by design (see kindSpecs).
func TestEveryKindHasADescriptor(t *testing.T) {
	for name := range kindConstNames(t) {
		if name == "Solo" {
			continue
		}
		// The constant's value is its wire spelling; find it in source.
		if _, ok := specOf(Kind(kindValue(t, name))); !ok {
			t.Errorf("Kind constant %s has no kindSpecs row", name)
		}
	}
}

// kindValue returns the string literal a Kind constant is declared with.
func kindValue(t *testing.T, name string) string {
	t.Helper()
	for _, f := range parsePackage(t) {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if n.Name == name && i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
							return strings.Trim(lit.Value, `"`)
						}
					}
				}
			}
		}
	}
	return ""
}

// TestNoKindSwitchOutsideTable fails on a switch or a map literal keyed by a
// Kind constant anywhere in internal/host except policy.go, where the table
// lives. Tests are exempt.
func TestNoKindSwitchOutsideTable(t *testing.T) {
	consts := kindConstNames(t)
	var bad []string
	for p, f := range parsePackage(t) {
		if filepath.Base(p) == "policy.go" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CaseClause:
				for _, e := range x.List {
					if id, ok := e.(*ast.Ident); ok && consts[id.Name] {
						bad = append(bad, p+": case "+id.Name)
					}
				}
			case *ast.CompositeLit:
				if _, isMap := x.Type.(*ast.MapType); isMap {
					for _, el := range x.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							if id, ok := kv.Key.(*ast.Ident); ok && consts[id.Name] {
								bad = append(bad, p+": map key "+id.Name)
							}
						}
					}
				}
			}
			return true
		})
	}
	if len(bad) > 0 {
		t.Fatalf("branch on Kind outside the kindSpecs table (policy.go): %s", strings.Join(bad, "; "))
	}
}

// The guards read the working tree, so a stray file would be missed if the
// package moved. Fail loudly if the directory has no sources.
func TestKindGuardSeesTheSources(t *testing.T) {
	if _, err := os.Stat("policy.go"); err != nil {
		t.Fatalf("guard must run in internal/host: %v", err)
	}
	if len(parsePackage(t)) == 0 {
		t.Fatal("no sources parsed")
	}
}
