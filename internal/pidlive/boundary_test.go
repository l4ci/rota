package pidlive

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Reap consumes path-use facts; only pidlive owns reading the process table.
func TestReapDelegatesProcessReads(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "reap", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no reap sources found")
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := n.(*ast.BasicLit); ok && s.Kind == token.STRING {
				v, _ := strconv.Unquote(s.Value)
				if v == "lsof" || v == "/proc" || strings.HasPrefix(v, "/proc/") {
					t.Errorf("%s reads processes directly via %q; use pidlive", path, v)
				}
			}
			if fn, ok := n.(*ast.FuncDecl); ok {
				switch fn.Name.Name {
				case "ReadProcUses", "readProcUses", "ReadLsofUses", "parseLsofUses":
					t.Errorf("%s owns process reader %s; move it to pidlive", path, fn.Name.Name)
				}
			}
			return true
		})
	}
}
