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
	// and a backticked key followed by a backticked value (`blockedBy` `x`). A value may be followed
	// by alternatives ("x" or "y"), each of which is a value too.
	q := "(?:\"[^\"]+\"|`[^`]+`)"
	chain := regexp.MustCompile("blockedBy(?:\"?`?:\\s*|`\\s+)(" + q + "(?:\\s+or\\s+" + q + ")*)")
	each := regexp.MustCompile(q)
	bare := regexp.MustCompile("blockedBy\"?`?:\\s*([A-Za-z][A-Za-z-]*)")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range chain.FindAllStringSubmatch(string(raw), -1) {
			for _, v := range each.FindAllString(m[1], -1) {
				got = append(got, v[1:len(v)-1])
			}
		}
		for _, m := range bare.FindAllStringSubmatch(string(raw), -1) {
			got = append(got, m[1])
		}
		for _, v := range got {
			if v != "string" && !set[v] {
				t.Errorf("%s quotes blockedBy %q, which blockedby.All() does not declare", filepath.Base(f), v)
			}
		}
	}
}

// TestAllListsEveryConst holds All() against blockedby.go itself: a string
// const declared there but left out of All() would go unseen by the doc scan
// until some doc happened to quote it.
func TestAllListsEveryConst(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "blockedby.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	set := declared(t)
	n := 0
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
				n++
				if v, _ := strconv.Unquote(b.Value); !set[v] {
					t.Errorf("const %s = %s is not listed in All()", name.Name, b.Value)
				}
			}
		}
		return false
	})
	if n != len(set) {
		t.Errorf("blockedby.go declares %d string consts, All() lists %d", n, len(set))
	}
}

// isBlockedByOperand reports whether e reads a blockedBy value: a BlockedBy
// field or a lookup keyed by "blockedBy" (jsonx.Str(d, "blockedBy")).
func isBlockedByOperand(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == "BlockedBy"
	case *ast.CallExpr:
		for _, a := range x.Args {
			if b, ok := a.(*ast.BasicLit); ok && b.Kind == token.STRING {
				if s, _ := strconv.Unquote(b.Value); s == "blockedBy" {
					return true
				}
			}
		}
	}
	return false
}

// bareBlockedByLiterals finds string literals in blockedBy position
// outside this package: a data key written as "blockedBy", a BlockedBy / By
// field, the first argument of a blocked/refuse constructor, the by argument of
// refuseHarvest/blockedRefusal/blocked, the by argument of milestoneFail, a map
// literal keyed "blockedBy", or a comparison or switch case against a blockedBy
// value. It also fails on a Block* const declared outside this package.
func bareBlockedByLiterals(root string) ([]string, error) {
	var found []string
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		// The labels registry owns tracker label strings, including "blocked", not blockedBy reasons.
		if strings.HasPrefix(rel, "blockedby/") || strings.HasPrefix(rel, "labels/") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		inCLI := strings.HasPrefix(rel, "cli/")
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
				// An empty by passes "no refusal".
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "milestoneFail" && len(x.Args) > 1 && lit(x.Args[1]) {
					if b := x.Args[1].(*ast.BasicLit); b.Value != `""` {
						report(b)
					}
				}
				// Helpers whose second parameter is the by value.
				if id, ok := x.Fun.(*ast.Ident); ok && (id.Name == "refuseHarvest" || id.Name == "blockedRefusal" || (id.Name == "blocked" && inCLI)) {
					if len(x.Args) > 1 && lit(x.Args[1]) {
						report(x.Args[1])
					}
				}
			case *ast.BinaryExpr:
				// An empty string tests "no refusal", not a value.
				nonEmpty := func(e ast.Expr) bool {
					b, ok := e.(*ast.BasicLit)
					return ok && b.Kind == token.STRING && b.Value != `""`
				}
				if x.Op == token.EQL || x.Op == token.NEQ {
					if nonEmpty(x.Y) && isBlockedByOperand(x.X) {
						report(x.Y)
					}
					if nonEmpty(x.X) && isBlockedByOperand(x.Y) {
						report(x.X)
					}
				}
			case *ast.SwitchStmt:
				if x.Tag != nil && isBlockedByOperand(x.Tag) {
					for _, c := range x.Body.List {
						for _, e := range c.(*ast.CaseClause).List {
							if lit(e) {
								report(e)
							}
						}
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := x.Key.(*ast.Ident); ok && (id.Name == "BlockedBy" || id.Name == "Blocked" || id.Name == "By") && lit(x.Value) {
					report(x.Value)
				}
				if k, ok := x.Key.(*ast.BasicLit); ok && k.Kind == token.STRING && lit(x.Value) {
					if s, _ := strconv.Unquote(k.Value); s == "blockedBy" {
						report(x.Value)
					}
				}
			case *ast.GenDecl:
				// A package-local const holding a literal would bypass the closed
				// set; an alias of a blockedby constant is the intended form.
				if x.Tok != token.CONST {
					break
				}
				for _, spec := range x.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if strings.HasPrefix(name.Name, "Block") && i < len(vs.Values) && lit(vs.Values[i]) {
							report(name)
						}
					}
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
	sort.Strings(found)
	return found, err
}

func TestNoBareBlockedByLiteral(t *testing.T) {
	found, err := bareBlockedByLiterals("../")
	if err != nil {
		t.Fatal(err)
	}
	for _, pos := range found {
		t.Errorf("bare blockedBy string literal at %s: declare it in internal/blockedby", pos)
	}
}

func TestBlockedByGuardOwnerBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, path, code string
		wantHit          bool
	}{
		{"label owner", "labels/registry.go", `const Blocked = "blocked"`, false},
		{"blockedBy owner", "blockedby/reason.go", `const Blocked = "blocked"`, false},
		{"other constant", "other/reason.go", `const Blocked = "blocked"`, true},
		{"bare refusal", "other/refuse.go", `func f() { blocked("bare-reason") }`, true},
		{"sibling name", "labels_extra/reason.go", `const Blocked = "blocked"`, true},
		{"bare field", "cli/field.go", `var data = map[string]string{"blockedBy":"bare-reason"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("package fixture\n"+tc.code+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			hits, err := bareBlockedByLiterals(root)
			if err != nil {
				t.Fatal(err)
			}
			if (len(hits) > 0) != tc.wantHit {
				t.Fatalf("guard hits=%v, wantHit=%v", hits, tc.wantHit)
			}
		})
	}
}
