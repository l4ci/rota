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
	"internal/testiso/nested.go":     "test helper built alone into test/fast_tree_scans_test.py's minimal module, so it cannot import rotatree",
}

// messageAllowed names the user-facing message literals that start with
// ".rota/", per file. They are text shown to people, not paths a program opens,
// and rewording them is out of scope. Keyed by file and exact literal, so a new
// path literal, or the same text in another file, still fails.
var messageAllowed = map[string]map[string]bool{
	"internal/backlog/write.go":          {".rota/BACKLOG.md not found": true},
	"internal/plan/uncertain.go":         {".rota/BACKLOG.md not found": true},
	"internal/cli/config.go":             {".rota/config.json is not a valid JSON object": true},
	"internal/config/fill.go":            {".rota/config.json is not a valid JSON object; fix or remove it, rota will not overwrite it": true},
	"internal/config/verbs.go":           {".rota/config.json is not a JSON object; fix or remove it, rota will not overwrite it": true, ".rota/config.local.json is not a JSON object; fix or remove it, rota will not overwrite it": true},
	"internal/roundcfg/support_fence.go": {".rota/config.local.json does not decode: ": true, ".rota/config.local.json cannot be read: ": true},
	"internal/artifact/docs.go":          {".rota/%s/%s.md already exists": true},
	"internal/spike/spike.go":            {".rota/spikes/%s.md already exists": true},
	"internal/initproj/init.go": {
		".rota/counters.json is not valid JSON; fix or remove it":    true,
		".rota/counters.json is not a JSON object; fix or remove it": true,
	},
}

// isLeak reports whether a string literal in file rel spells the state
// directory or a path under it and is not an allowed message for that file.
func isLeak(rel, s string) bool {
	return (s == DirName || strings.HasPrefix(s, DirName+"/")) && !messageAllowed[rel][s]
}

func TestIsLeak(t *testing.T) {
	const file = "internal/backlog/write.go"
	for _, c := range []struct {
		rel, s string
		want   bool
	}{
		{file, DirName, true}, {file, DirName + "/plans/x.md", true},
		{file, DirName + "/BACKLOG.md not found", false},
		{"internal/other/x.go", DirName + "/BACKLOG.md not found", true},
		{"internal/other/x.go", DirName + "/", true},
		{"internal/other/x.go", DirName + "/%s: %w", true},
		{file, "docs/" + DirName, false}, {file, "other", false}, {file, "", false},
	} {
		if got := isLeak(c.rel, c.s); got != c.want {
			t.Errorf("isLeak(%q, %q) = %v, want %v", c.rel, c.s, got, c.want)
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
				s, err := strconv.Unquote(lit.Value)
				if err == nil && isLeak(rel, s) {
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

// TestMessageAllowedEntriesUsed fails on an allowlist entry no literal matches,
// so a dead entry cannot quietly widen the exemption.
func TestMessageAllowedEntriesUsed(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	for rel, lits := range messageAllowed {
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		seen := map[string]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					seen[s] = true
				}
			}
			return true
		})
		for s := range lits {
			if !seen[s] {
				t.Errorf("messageAllowed[%q] has unused entry %q", rel, s)
			}
		}
	}
}
