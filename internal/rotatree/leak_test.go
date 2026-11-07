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

// literalAllowed names the files that may spell ".rota" on purpose, each with
// the reason. hvmigrate moves the legacy state dir to .rota/, so the target
// directory is part of its own contract; the gitignore table is the
// literal text written into users' .gitignore, globs included.
var literalAllowed = map[string]string{
	"internal/hvmigrate/hv.go":       "the legacy-state migration names its target directory",
	"internal/initproj/gitignore.go": "ignore patterns are the literal text written to .gitignore, globs included",
}

// messageAllowed names the user-facing message literals that start with
// ".rota/". They are text shown to people, not paths a program opens, and
// rewording them is out of scope. Keyed by exact literal, so a new path
// literal in the same file still fails.
var messageAllowed = map[string]bool{
	".rota/BACKLOG.md not found":                   true,
	".rota/config.json is not a valid JSON object": true,
	".rota/config.json is not a JSON object":       true,
	".rota/config.local.json is not a JSON object": true,
	".rota/config.local.json does not decode: ":    true,
	".rota/config.local.json cannot be read: ":     true,
	".rota/":                             true, // migrate_round.go: prefix of a corrupt-file message
	".rota/%s: %w":                       true,
	".rota/designs/%s.md already exists": true,
	".rota/plans/%s.md already exists":   true,
	".rota/spikes/%s.md already exists":  true,
	".rota/counters.json is not valid JSON; fix or remove it":    true,
	".rota/counters.json is not a JSON object; fix or remove it": true,
}

// isLeak reports whether a string literal spells the state directory or a path
// under it and is not an allowed message.
func isLeak(s string) bool {
	return (s == DirName || strings.HasPrefix(s, DirName+"/")) && !messageAllowed[s]
}

func TestIsLeak(t *testing.T) {
	for s, want := range map[string]bool{
		DirName: true, DirName + "/plans/x.md": true, DirName + "/BACKLOG.md not found": false,
		"docs/" + DirName: false, "other": false, "": false,
	} {
		if got := isLeak(s); got != want {
			t.Errorf("isLeak(%q) = %v, want %v", s, got, want)
		}
	}
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
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || literalAllowed[rel] != "" {
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
				if s, err := strconv.Unquote(lit.Value); err == nil && isLeak(s) {
					t.Errorf("%s: literal %q; use rotatree.Dir, rotatree.File or rotatree.Rel", fset.Position(lit.Pos()), s)
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
