package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// gateVerdictConsts reads the package's own source for every string constant
// named Gate*, the verdict constants, so a new one cannot dodge the table.
func gateVerdictConsts(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if !strings.HasPrefix(n.Name, "Gate") || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, _ := strconv.Unquote(lit.Value)
					found[n.Name] = v
				}
			}
		}
	}
	return found
}

func TestVerdictsAreClassified(t *testing.T) {
	consts := gateVerdictConsts(t)
	if len(consts) < 20 {
		t.Fatalf("found only %d Gate* verdict constants; the scan is broken", len(consts))
	}
	for name, v := range consts {
		if _, ok := verdictMeanings[v]; !ok {
			t.Errorf("%s (%q) has no entry in verdictMeanings", name, v)
		}
	}
	for v := range verdictMeanings {
		found := false
		for _, cv := range consts {
			found = found || cv == v
		}
		if !found {
			t.Errorf("verdictMeanings has %q, which no Gate* constant names", v)
		}
	}
}

func TestVerdictMeaningsAreCoherent(t *testing.T) {
	for v, m := range verdictMeanings {
		if m.Success && (m.Refusal || m.Bounce) {
			t.Errorf("%q succeeds yet refuses or bounces", v)
		}
		if m.Refusal && m.Bounce {
			t.Errorf("%q both refuses and bounces", v)
		}
		if m.BlockedBy != "" && !m.Refusal {
			t.Errorf("%q names blockedBy %q but is no refusal", v, m.BlockedBy)
		}
		if m.Bounce && m.Hold {
			t.Errorf("%q goes back to a worker, so nobody holds it", v)
		}
	}
	if m := ClassifyVerdict("unheard-of"); m.Success || m.Refusal || m.Bounce || !m.Hold {
		t.Errorf("an unclassified verdict should be a plain held failure, got %+v", m)
	}
}
