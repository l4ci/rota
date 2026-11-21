package landing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
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
					switch x := vs.Values[i].(type) {
					case *ast.BasicLit:
						if x.Kind == token.STRING {
							found[n.Name], _ = strconv.Unquote(x.Value)
						}
					case *ast.SelectorExpr: // = blockedby.X: the shared spelling
						if v, ok := blockedByConsts(t)[x.Sel.Name]; ok {
							found[n.Name] = v
						}
					}
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
		if m.Success && m.Refusal {
			t.Errorf("%q succeeds yet refuses or bounces", v)
		}
		if m.BlockedBy != "" && !m.Refusal {
			t.Errorf("%q names blockedBy %q but is no refusal", v, m.BlockedBy)
		}
		if m.Notify && m.Hold {
			t.Errorf("%q goes back to a worker, so nobody holds it", v)
		}
	}
	if m := ClassifyVerdict("unheard-of"); m.Success || m.Refusal || m.Notify || !m.Hold {
		t.Errorf("an unclassified verdict should be a plain held failure, got %+v", m)
	}
}

// A refusal's blockedBy is a declared value. Verdicts that share a key with
// blockedby are that constant, so no pin test is needed for the spelling.
func TestVerdictRefusalsNameDeclaredBlockedBy(t *testing.T) {
	declared := map[string]bool{}
	for _, v := range blockedby.All() {
		declared[v] = true
	}
	for v, m := range verdictMeanings {
		if m.BlockedBy == "" {
			continue
		}
		if !declared[m.BlockedBy] {
			t.Errorf("%q names blockedBy %q, which blockedby.All() does not declare", v, m.BlockedBy)
		}
	}
}

func TestClaimLostIsHeldRefusal(t *testing.T) {
	got := ClassifyVerdict(GateClaimLost)
	want := VerdictMeaning{Refusal: true, BlockedBy: "claim-lost", Hold: true}
	if got != want {
		t.Fatalf("claim-lost meaning %+v, want %+v", got, want)
	}
	if !(GateResult{Verdict: GateClaimLost}).Refused(nil) {
		t.Fatal("claim-lost must be an exit-4 refusal")
	}
}

// A stale merge and a provenance failure are bookkeeping the worker repairs,
// not review findings: they go back to the worker notify-only and are never
// held for a person (#2072).
func TestStaleAndProvenanceFailAreNotifyOnly(t *testing.T) {
	for _, v := range []string{GateStale, GateProvenanceFail} {
		if m := ClassifyVerdict(v); !m.Notify || m.Hold || m.Refusal || m.Success {
			t.Errorf("%q must be notify-only: %+v", v, m)
		}
	}
}

// blockedByConsts maps each string constant of the blockedby package to its value.
func blockedByConsts(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../blockedby/blockedby.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					out[n.Name], _ = strconv.Unquote(lit.Value)
				}
			}
		}
	}
	return out
}
