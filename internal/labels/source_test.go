package labels

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Distinctive tracker names/prefixes belong to the registry. Generic words
// (blocked, released) also name workflow states, so those are pinned by the
// config-schema parity test rather than banning their other meanings.
func labelLiterals(root string) ([]string, error) {
	var distinctive []string
	for _, d := range definitions() {
		if strings.Contains(d.Name, ":") || d.Name == PartialSlice || d.Name == Refactor || d.Name == NeedsInfo || d.Name == NeedsHuman || d.Name == MilestoneTracker {
			distinctive = append(distinctive, d.Name)
		}
		if d.Name == PriorityPrefix {
			for _, v := range d.values {
				distinctive = append(distinctive, d.Name+v)
			}
		}
	}
	// The type family must also catch a new, unregistered type suffix.
	distinctive = append(distinctive, "type:")

	var hits []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() && (rel == "internal/labels" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, _ := strconv.Unquote(lit.Value)
			if rel == "internal/round/stale.go" && s == "in-progress" {
				hits = append(hits, fset.Position(lit.Pos()).String()+": "+s)
			}
			// These literals name a CLI verb and a legacy config object, not labels.
			if s == "refactor" && (rel == "internal/cli/backlog.go" || rel == "internal/config/fill.go") {
				return true
			}
			for _, v := range distinctive {
				if s == v || strings.HasSuffix(v, ":") && strings.HasPrefix(s, v) && !strings.ContainsAny(s, " \t\r\n") {
					hits = append(hits, fset.Position(lit.Pos()).String()+": "+s)
					break
				}
			}
			return true
		})
		return nil
	})
	return hits, err
}

func TestNoLabelLiteralOutsideRegistry(t *testing.T) {
	hits, err := labelLiterals("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Fatalf("tracker label literals outside registry:\n%s", strings.Join(hits, "\n"))
	}
}

func TestLabelLiteralGuardCatchesNewPaths(t *testing.T) {
	for _, literal := range []string{"claimed:new@mac", "harness:new", "model:custom", "type:other", "partial-slice"} {
		root := t.TempDir()
		dir := filepath.Join(root, "internal", "newwriter")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		code := "package newwriter\nconst label = " + strconv.Quote(literal) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "label.go"), []byte(code), 0644); err != nil {
			t.Fatal(err)
		}
		hits, err := labelLiterals(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Errorf("guard missed %q: %v", literal, hits)
		}
	}
}
