package worker

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

func TestStateSets(t *testing.T) {
	cases := []struct {
		state                                                                 string
		done, idle, blocked, dead, parkable, settled, reclaim, building, busy bool
	}{
		{"done", true, false, false, false, true, true, true, false, false},
		{"idle", false, true, false, false, true, true, true, false, false},
		{"blocked", false, false, true, false, false, true, true, true, false},
		{"dead", false, false, false, true, false, false, true, false, false},
		{"busy", false, false, false, false, false, false, false, true, true},
		{"", false, false, false, false, false, false, false, true, false},
		{"limited", false, false, false, false, false, false, false, false, false},
	}
	for _, c := range cases {
		for _, st := range []string{c.state, upper(c.state)} {
			got := []bool{IsDone(st), IsIdle(st), IsBlocked(st), IsDead(st), Parkable(st), Settled(st), Reclaimable(st), Building(st), IsBusy(st)}
			want := []bool{c.done, c.idle, c.blocked, c.dead, c.parkable, c.settled, c.reclaim, c.building, c.busy}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("state %q predicate %d = %v, want %v", st, i, got[i], want[i])
				}
			}
		}
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// TestNoRetypedStateSets keeps the one case rule: a caller asks through the
// named predicates, never by comparing a recorded slot state to a literal.
func TestNoRetypedStateSets(t *testing.T) {
	re := regexp.MustCompile(`(?i)\bState(\(\))?\s*[!=]=\s*"(done|idle|blocked|dead|busy)"`)
	root := filepath.Join("..")
	var hits []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(b), "\n") {
			if re.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", p, i+1, strings.TrimSpace(line)))
			}
		}
		// Watch/wait also compare local aliases and normalised expressions.
		// Parse comparisons so wrapping a line or reversing its operands cannot
		// hide another copy of the idle rule (#2019).
		if filepath.Dir(p) == filepath.Join(root, "roundwatch") || p == filepath.Join(root, "worker", "wait.go") {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, p, b, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				cmp, ok := n.(*ast.BinaryExpr)
				if !ok || (cmp.Op != token.EQL && cmp.Op != token.NEQ) {
					return true
				}
				for _, operand := range []ast.Expr{cmp.X, cmp.Y} {
					if lit, ok := operand.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if value, err := strconv.Unquote(lit.Value); err == nil && strings.EqualFold(value, "idle") {
							hits = append(hits, fmt.Sprintf("%s: literal idle compare", fset.Position(cmp.Pos())))
						}
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		t.Errorf("literal slot-state compare, use a worker predicate: %s", h)
	}
}
