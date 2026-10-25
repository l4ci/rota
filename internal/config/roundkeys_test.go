package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Guard key names in every expression, including local aliases, maps and
// dynamic-key prefixes. Diagnostic prose is not a key literal. Unquoting the
// AST literals also covers raw strings and escaped spellings, without comments.
var roundKeyLiteral = regexp.MustCompile(`^round\.[A-Za-z0-9_.]*$`)

func roundKeyLiterals(t *testing.T, root string) []string {
	t.Helper()
	var hits []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "node_modules", "vendor", "testdata":
				return fs.SkipDir
			}
			if filepath.ToSlash(rel) == "internal/config" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
			value, _ := strconv.Unquote(lit.Value)
			if roundKeyLiteral.MatchString(value) {
				hits = append(hits, fmt.Sprintf("%s: %s", fset.Position(lit.Pos()), lit.Value))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func TestNoRoundKeyLiteralOutsideConfig(t *testing.T) {
	if hits := roundKeyLiterals(t, "../.."); len(hits) > 0 {
		t.Fatalf("use the round key constants from internal/config:\n%s", strings.Join(hits, "\n"))
	}
}

func TestRoundKeyGuard(t *testing.T) {
	for _, tc := range []struct {
		name, file, code string
		want             int
	}{
		{"read", "internal/x/x.go", `var v = config.String(cfg, "round.scope")`, 1},
		{"unknown reader", "cmd/x/x.go", `var v = read("round.futureKey")`, 1},
		{"alias", "internal/x/x.go", `const key = "round.scope"`, 1},
		{"map", "internal/x/x.go", `var v = map[string]int{"round.slots": 3}`, 1},
		{"comparison", "internal/x/x.go", `var v = key == "round.role"`, 1},
		{"raw", "internal/x/x.go", "var v = `round.support`", 1},
		{"escaped", "internal/x/x.go", `var v = "round.\x73cope"`, 1},
		{"prefix", "internal/x/x.go", `var v = "round.tiers." + kind + "." + tier`, 1},
		{"config", "internal/config/x.go", `const key = "round.scope"`, 0},
		{"test", "internal/x/x_test.go", `var v = "round.scope"`, 0},
		{"fixture", "internal/x/testdata/x.go", `var v = "round.scope"`, 0},
		{"worktree", ".worktrees/x/x.go", `var v = "round.scope"`, 0},
		{"comment", "internal/x/x.go", `// "round.scope"`, 0},
		{"diagnostic", "internal/x/x.go", `var v = "round.scope must be valid"`, 0},
		{"other key", "internal/x/x.go", `var v = "work.tdd"`, 0},
		{"constant", "internal/x/x.go", `var v = config.RoundScopeKey`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, tc.file)
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("package x\n"+tc.code+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if hits := roundKeyLiterals(t, root); len(hits) != tc.want {
				t.Fatalf("got %v, want %d findings", hits, tc.want)
			}
		})
	}
}
