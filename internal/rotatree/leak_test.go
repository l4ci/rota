package rotatree

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

// literalAllowed names the files that may spell ".rota" on purpose. hvmigrate
// moves the legacy state dir to .rota/, so the target directory is part of its own contract.
var literalAllowed = map[string]bool{
	"internal/hvmigrate/hv.go": true,
}

// TestNoLiteralRotaDir fails when non-test Go code outside rotatree spells the
// ".rota" directory name: packages ask rotatree for a path instead.
func TestNoLiteralRotaDir(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	for _, tree := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			if d.IsDir() {
				if rel == "internal/rotatree" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || literalAllowed[rel] {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if s, err := strconv.Unquote(lit.Value); err == nil && s == DirName {
					t.Errorf("%s: literal %q; use rotatree.Dir or rotatree.File", fset.Position(lit.Pos()), DirName)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
