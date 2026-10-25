package blockedby

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func declared(t *testing.T) map[string]bool {
	t.Helper()
	set := map[string]bool{}
	for _, v := range All() {
		if set[v] {
			t.Errorf("All() lists %q twice", v)
		}
		set[v] = true
	}
	return set
}

// TestBlockedByIsClosed holds the contract docs against the declared set: a
// blockedBy value a doc quotes that nothing declares is a typo or a new
// spelling that bypassed this package.
func TestBlockedByIsClosed(t *testing.T) {
	set := declared(t)
	files, err := filepath.Glob("../../docs/contributing/contract/*.md")
	skillFiles, _ := filepath.Glob("../../skills/*/*.md")
	files = append(files, skillFiles...)
	if err != nil || len(files) == 0 {
		t.Fatalf("no contract docs found: %v", err)
	}
	// The forms the contract docs and skills use: `blockedBy: "x"`, `"blockedBy": "x"`, `blockedBy: x`
	// and a backticked key followed by a backticked value (`blockedBy` `x`).
	quoted := regexp.MustCompile("blockedBy\"?`?:\\s*(?:\"([^\"]+)\"|`([^`]+)`)")
	bare := regexp.MustCompile("blockedBy\"?`?:\\s*([A-Za-z][A-Za-z-]*)")
	spaced := regexp.MustCompile("blockedBy`\\s+`([^`]+)`")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range quoted.FindAllStringSubmatch(string(raw), -1) {
			got = append(got, m[1]+m[2])
		}
		for _, m := range bare.FindAllStringSubmatch(string(raw), -1) {
			got = append(got, m[1])
		}
		for _, m := range spaced.FindAllStringSubmatch(string(raw), -1) {
			got = append(got, m[1])
		}
		for _, v := range got {
			if v != "string" && !set[v] {
				t.Errorf("%s quotes blockedBy %q, which blockedby.All() does not declare", filepath.Base(f), v)
			}
		}
	}
}

// TestNoBareBlockedByLiteral fails on a string literal in blockedBy position
// outside this package: a data key written as "blockedBy", a BlockedBy / By
// field, or the first argument of a blocked/refuse constructor.
func TestNoBareBlockedByLiteral(t *testing.T) {
	root := "../"
	var found []string
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(p), "../blockedby/") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		lit := func(e ast.Expr) bool {
			b, ok := e.(*ast.BasicLit)
			return ok && b.Kind == token.STRING
		}
		report := func(n ast.Node) { found = append(found, fset.Position(n.Pos()).String()) }
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				for i, a := range x.Args {
					if b, ok := a.(*ast.BasicLit); ok && b.Kind == token.STRING {
						if s, _ := strconv.Unquote(b.Value); s == "blockedBy" && i+1 < len(x.Args) && lit(x.Args[i+1]) {
							report(x.Args[i+1])
						}
					}
				}
				if id, ok := x.Fun.(*ast.Ident); ok && (id.Name == "blocked" || id.Name == "refuse" || id.Name == "refused") {
					if len(x.Args) > 0 && lit(x.Args[0]) {
						report(x.Args[0])
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := x.Key.(*ast.Ident); ok && (id.Name == "BlockedBy" || id.Name == "Blocked" || id.Name == "By") && lit(x.Value) {
					report(x.Value)
				}
			case *ast.AssignStmt:
				for i, l := range x.Lhs {
					if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "BlockedBy" && i < len(x.Rhs) && lit(x.Rhs[i]) {
						report(x.Rhs[i])
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	for _, pos := range found {
		t.Errorf("bare blockedBy string literal at %s: declare it in internal/blockedby", pos)
	}
}
