package testiso

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var configDirReference = regexp.MustCompile(`\b(ROTA_CONFIG_DIR|XDG_CONFIG_HOME)\b`)

// configIsolationFailures is a file-level guard, not proof that every test path
// is isolated. Keep the requirement local so copying a test into another package
// cannot silently lose an import supplied by a sibling file.
func configIsolationFailures(root string) ([]string, error) {
	var failures []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ProjectTemp(root, path, d) || NestedCheckout(root, path, d) || (d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata")) {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Tests of testiso itself always run this package's isolation init.
		if filepath.ToSlash(filepath.Dir(rel)) == "internal/testiso" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			if literal(imp.Path) == "github.com/l4ci/rota/internal/testiso" {
				return nil
			}
		}
		var referencesConfig, pinsConfig bool
		// Recognize Setenv only on *testing.T parameters, including renamed
		// receivers/imports, rather than accepting any object with that method.
		testingName := "testing"
		for _, imp := range file.Imports {
			if literal(imp.Path) == "testing" && imp.Name != nil {
				testingName = imp.Name.Name
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if configDirReference.MatchString(literal(n)) {
				referencesConfig = true
			}
			return true
		})
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			receivers := map[string]bool{}
			for _, param := range fn.Type.Params.List {
				ptr, ok := param.Type.(*ast.StarExpr)
				if !ok {
					continue
				}
				sel, ok := ptr.X.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "T" {
					continue
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != testingName {
					continue
				}
				for _, name := range param.Names {
					receivers[name.Name] = true
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Setenv" {
					return true
				}
				receiver, ok := sel.X.(*ast.Ident)
				if ok && receivers[receiver.Name] && (literal(call.Args[0]) == "HOME" || literal(call.Args[0]) == "XDG_CONFIG_HOME") {
					pinsConfig = true
				}
				return true
			})
		}
		if referencesConfig && !pinsConfig {
			failures = append(failures, filepath.ToSlash(rel))
		}
		return nil
	})
	return failures, err
}

func literal(n ast.Node) string {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, _ := strconv.Unquote(lit.Value)
	return s
}

func TestConfigTouchingTestsAreIsolated(t *testing.T) {
	failures, err := configIsolationFailures(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range failures {
		t.Errorf("%s references config-dir env: import internal/testiso or pin HOME/XDG_CONFIG_HOME with t.Setenv", path)
	}
}

func TestConfigIsolationGuardFixture(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"unsafe_test.go":     `package example; import "os"; var value = os.Getenv("ROTA_CONFIG_DIR")`,
		"embedded_test.go":   `package example; var command = "echo $XDG_CONFIG_HOME"`,
		"assignment_test.go": `package example; var env = "ROTA_CONFIG_DIR=/fixture"`,
		"near_miss_test.go":  `package example; var value = "NOT_ROTA_CONFIG_DIR"`,
		"unsafe_xdg_test.go": "package example; var value = `XDG_CONFIG_HOME`",
		"imported_test.go":   `package example; import _ "github.com/l4ci/rota/internal/testiso"; var value = "ROTA_CONFIG_DIR"`,
		"home_test.go":       `package example; import "testing"; func TestEnv(t *testing.T) { t.Setenv("HOME", t.TempDir()); _ = "ROTA_CONFIG_DIR" }`,
		"xdg_test.go":        `package example; import check "testing"; func TestEnv(test *check.T) { test.Setenv("XDG_CONFIG_HOME", test.TempDir()) }`,
		"override_test.go":   `package example; import "testing"; func TestEnv(t *testing.T) { t.Setenv("ROTA_CONFIG_DIR", t.TempDir()) }`,
		"fake_test.go":       `package example; func helper(t *Fake) { t.Setenv("HOME", "fake"); _ = "XDG_CONFIG_HOME" }`,
		"comment_test.go":    "package example // ROTA_CONFIG_DIR\n",
		"ordinary.go":        `package example; var value = "ROTA_CONFIG_DIR"`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := configIsolationFailures(root)
	want := []string{"assignment_test.go", "embedded_test.go", "fake_test.go", "override_test.go", "unsafe_test.go", "unsafe_xdg_test.go"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("guard = %v, %v; want %v", got, err, want)
	}
	// A fixture with exactly one offending file must still be rejected.
	for _, name := range want[1:] {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	got, err = configIsolationFailures(root)
	if err != nil || !reflect.DeepEqual(got, []string{"assignment_test.go"}) {
		t.Fatalf("single-file guard = %v, %v; want [assignment_test.go]", got, err)
	}
}
