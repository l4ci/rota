package gittest

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

// uncheckedGit returns the positions of raw exec.Command("git", ...) calls in
// src whose result is thrown away: a bare statement, a go or defer, or an
// assignment to blanks only. A fixture step that fails there is a silent green;
// gittest.Run fails the test where it happens. A call whose result is kept
// (assigned, tested, returned) is the author's to check.
func uncheckedGit(t *testing.T, name, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		call, ok := n.(*ast.CallExpr)
		if !ok || !isRawGit(call) {
			return true
		}
		// Climb the method chain: exec.Command(...).Run() and the like.
		i := len(stack) - 1
		for i > 0 {
			switch p := stack[i-1].(type) {
			case *ast.SelectorExpr:
				if p.X == stack[i] {
					i--
					continue
				}
			case *ast.CallExpr:
				if p.Fun == stack[i] {
					i--
					continue
				}
			}
			break
		}
		if i == 0 {
			return true
		}
		discarded := false
		switch p := stack[i-1].(type) {
		case *ast.ExprStmt, *ast.GoStmt, *ast.DeferStmt:
			discarded = true
		case *ast.AssignStmt:
			discarded = true
			for _, l := range p.Lhs {
				if id, ok := l.(*ast.Ident); !ok || id.Name != "_" {
					discarded = false
				}
			}
		}
		if discarded {
			bad = append(bad, fset.Position(call.Pos()).String())
		}
		return true
	})
	return bad
}

func isRawGit(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "exec" {
		return false
	}
	arg := 0
	switch sel.Sel.Name {
	case "Command":
	case "CommandContext":
		arg = 1
	default:
		return false
	}
	if len(c.Args) <= arg {
		return false
	}
	lit, ok := c.Args[arg].(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && lit.Value == `"git"`
}

func TestUncheckedGitFlagsDiscardedResults(t *testing.T) {
	const head = "package x\nimport \"os/exec\"\nfunc f() {\n"
	for _, c := range []struct {
		name, body string
		want       int
	}{
		{"bare", `exec.Command("git", "merge").Run()`, 1},
		{"blank", `_ = exec.Command("git", "merge").Run()`, 1},
		{"blank output", `_, _ = exec.Command("git", "log").Output()`, 1},
		{"context", `exec.CommandContext(ctx, "git", "merge").Run()`, 1},
		{"defer", `defer exec.Command("git", "gc").Run()`, 1},
		{"if", `if err := exec.Command("git", "merge").Run(); err != nil {}`, 0},
		{"assigned", `err := exec.Command("git", "merge").Run(); _ = err`, 0},
		{"built", `cmd := exec.Command("git", "merge"); _ = cmd`, 0},
		{"not git", `exec.Command("ls").Run()`, 0},
	} {
		if got := uncheckedGit(t, c.name+".go", head+c.body+"\n}\n"); len(got) != c.want {
			t.Errorf("%s: flagged %v, want %d", c.name, got, c.want)
		}
	}
}

// TestNoUncheckedRawGitInTests fails on a fixture that runs git with exec and
// drops the error. Use gittest.Run, or keep the error and assert on it.
func TestNoUncheckedRawGitInTests(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, tree := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return fs.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := readFile(path)
			if err != nil {
				return err
			}
			for _, pos := range uncheckedGit(t, path, src) {
				t.Errorf("%s: raw exec.Command(\"git\") result discarded; use gittest.Run", pos)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
