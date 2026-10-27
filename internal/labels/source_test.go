package labels

import (
	"errors"
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

// Explicit tracker label arguments in skills must be registered. Dynamic
// placeholders and shell variables are checked by their producers instead.
var skillLabelArg = regexp.MustCompile("(?:--label|--add|--remove) +([^ \t`;)]+)")

// Distinctive tracker names/prefixes belong to the registry. Generic words
// (blocked, released) also name workflow states, so those are pinned by the
// config-schema parity test rather than banning their other meanings.
func labelLiterals(root string) ([]string, error) {
	var distinctive []string
	for _, d := range definitions() {
		if d.Prefix || strings.ContainsAny(d.Name, ":/") || d.Name == PartialSlice || d.Name == Refactor || d.Name == NeedsInfo || d.Name == NeedsHuman || d.Name == MilestoneTracker {
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
	for _, tree := range []string{"internal", "cmd", "skills"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && path == filepath.Join(root, tree) {
				return nil
			}
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if d.IsDir() && (rel == "internal/labels" || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasPrefix(rel, "skills/") && strings.HasSuffix(path, ".md") {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				for i, line := range strings.Split(string(b), "\n") {
					for _, m := range skillLabelArg.FindAllStringSubmatch(line, -1) {
						if !strings.HasPrefix(m[0], "--label ") && !strings.Contains(line, "rota issues label ") {
							continue
						}
						name := strings.Trim(m[1], "\"'")
						if name == "" || strings.ContainsAny(name, "${") || strings.HasPrefix(name, "<") {
							continue
						}
						if _, ok := Find(Resolve(nil), name); !ok && name != LegacySupport {
							hits = append(hits, fmt.Sprintf("%s:%d: unregistered skill label %s", path, i+1, name))
						}
					}
				}
				return nil
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
				// Discover unregistered families at label parsers, independent of the
				// registry inventory. A slash family must not disappear from the guard
				// merely because its registry definition was forgotten.
				if lit := labelParserLiteral(n); lit != nil {
					prefix, _ := strconv.Unquote(lit.Value)
					hits = append(hits, fset.Position(lit.Pos()).String()+": label parser prefix "+prefix)
				}

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
					if s == v || (strings.HasSuffix(v, ":") || strings.HasSuffix(v, "/")) && strings.HasPrefix(s, v) && !strings.ContainsAny(s, " \t\r\n") {
						hits = append(hits, fset.Position(lit.Pos()).String()+": "+s)
						break
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return hits, nil
}

// labelParserLiteral identifies hard-coded prefix parsing without needing an
// existing registry entry. Paths parsed from non-label variables are unrelated.
func labelParserLiteral(n ast.Node) *ast.BasicLit {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) < 2 {
		return nil
	}
	fn, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (fn.Sel.Name != "CutPrefix" && fn.Sel.Name != "HasPrefix" && fn.Sel.Name != "TrimPrefix") {
		return nil
	}
	arg, ok := call.Args[0].(*ast.Ident)
	if !ok || !strings.Contains(strings.ToLower(arg.Name), "label") {
		return nil
	}
	lit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil
	}
	prefix, _ := strconv.Unquote(lit.Value)
	if strings.HasSuffix(prefix, "/") || strings.HasSuffix(prefix, ":") {
		return lit
	}
	return nil
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
	for _, literal := range []string{"claimed:new@mac", "harness:new", "model:custom", "type:other", "partial-slice", "on-milestone/M01"} {
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

func TestSkillLabelGuardRequiresRegistration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "release")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "milestone.md")
	if err := os.WriteFile(file, []byte("Run `rota issues label 1 --remove on-milestone/<MNN>`.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if hits, err := labelLiterals(root); err != nil || len(hits) != 0 {
		t.Fatalf("registered milestone family in skill: %v, %v", hits, err)
	}
	if err := os.WriteFile(file, []byte("Run `gh issue list --label undeclared-family/<id>`.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if hits, err := labelLiterals(root); err != nil || len(hits) != 1 {
		t.Fatalf("unregistered slash family in skill escaped guard: %v, %v", hits, err)
	}
	// The code guard also discovers new slash families before registration.
	dir = filepath.Join(root, "internal", "reader")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	code := `package reader
func read(label string) { strings.CutPrefix(label,"undeclared-family/") }
`
	if err := os.WriteFile(filepath.Join(dir, "read.go"), []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	if hits, err := labelLiterals(root); err != nil || len(hits) != 2 {
		t.Fatalf("unregistered code family escaped guard: %v, %v", hits, err)
	}
}
